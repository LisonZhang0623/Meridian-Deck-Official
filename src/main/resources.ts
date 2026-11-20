import type { Client, ClientChannel } from 'ssh2'
import type { ResourceSnapshot } from '../shared/v2'

export const RESOURCE_COMMAND = `LC_ALL=C; export LC_ALL
if [ ! -r /proc/stat ] || [ ! -r /proc/meminfo ]; then printf 'Resource monitoring requires Linux /proc.\\n' >&2; exit 1; fi
printf '[cpu]\\n'; head -n 1 /proc/stat || exit 1
printf '[memory]\\n'; cat /proc/meminfo || exit 1
printf '[disk]\\n'; df -Pk / || exit 1
printf '[network]\\n'; cat /proc/net/dev || exit 1
printf '[uptime]\\n'; cat /proc/uptime || exit 1
printf '[load]\\n'; cat /proc/loadavg || exit 1`

function number(value: string | undefined, label: string): number {
  const n = value === undefined || value.trim() === '' ? NaN : Number(value)
  if (!Number.isFinite(n) || n < 0 || n > Number.MAX_SAFE_INTEGER) throw new Error(`资源数据无效：${label}`)
  return n
}
export function parseResources(output: string, collectedAt = Date.now()): ResourceSnapshot {
  const sections = new Map<string, string[]>()
  let section: string[] | undefined
  for (const line of output.split(/\r?\n/)) {
    const marker = /^\[(cpu|memory|disk|network|uptime|load)\]$/.exec(line.trim())
    if (marker) {
      if (sections.has(marker[1])) throw new Error('资源数据包含重复字段')
      section = []; sections.set(marker[1], section)
    } else if (line.trim()) section?.push(line.trim())
  }
  const rows = (name: string): string[] => { const data = sections.get(name); if (!data?.length) throw new Error(`资源数据缺少 ${name}`); return data }
  const cpuRow = rows('cpu')[0].split(/\s+/)
  if (cpuRow.shift() !== 'cpu' || cpuRow.length < 4) throw new Error('CPU 数据格式不正确')
  const ticks = cpuRow.slice(0, 8).map(n => number(n, 'CPU'))
  const cpu = { total: ticks.reduce((a, b) => a + b, 0), idle: ticks[3] + (ticks[4] || 0) }
  if (!Number.isSafeInteger(cpu.total) || !cpu.total) throw new Error('CPU 计数无效')
  const mem = new Map<string, number>()
  for (const line of rows('memory')) {
    const key = /^(\w+):/.exec(line)?.[1]
    if (!key || !['MemTotal', 'MemAvailable', 'MemFree', 'Buffers', 'Cached', 'SReclaimable', 'Shmem'].includes(key)) continue
    const parts = /^(\w+):\s+(\d+)\s+kB$/.exec(line)
    if (!parts || mem.has(key)) throw new Error('内存数据字段损坏或重复')
    const bytes = number(parts[2], '内存') * 1024
    if (!Number.isSafeInteger(bytes)) throw new Error('内存容量超出范围')
    mem.set(key, bytes)
  }
  const total = mem.get('MemTotal') || 0
  if (!mem.has('MemAvailable') && !['MemFree', 'Buffers', 'Cached'].every(key => mem.has(key))) throw new Error('内存可用容量字段缺失')
  const available = mem.get('MemAvailable') ?? ((mem.get('MemFree') || 0) + (mem.get('Buffers') || 0) + (mem.get('Cached') || 0) + (mem.get('SReclaimable') || 0) - (mem.get('Shmem') || 0))
  if (!total || available < 0 || available > total) throw new Error('内存数据格式不正确')
  const diskRow = rows('disk').map(line => /^.+?\s+(\d+)\s+(\d+)\s+(\d+)\s+\d+%\s+(\/)\s*$/.exec(line)).find(Boolean)
  if (!diskRow) throw new Error('无法解析根分区容量')
  const disk = { total: number(diskRow[1], '磁盘') * 1024, used: number(diskRow[2], '磁盘') * 1024, available: number(diskRow[3], '磁盘') * 1024, mount: diskRow[4] }
  if (![disk.total, disk.used, disk.available].every(Number.isSafeInteger) || !disk.total || disk.used > disk.total || disk.available > disk.total) throw new Error('磁盘容量数据无效')
  const seen = new Set<string>()
  const network = rows('network').filter(line => line.includes(':')).map(line => {
    const separator = line.indexOf(':'), name = line.slice(0, separator).trim(), fields = line.slice(separator + 1).trim().split(/\s+/)
    if (!name || seen.has(name) || fields.length < 16) throw new Error('网络接口数据无效')
    seen.add(name)
    return { name, received: number(fields[0], '网络接收'), sent: number(fields[8], '网络发送') }
  }).filter(net => net.name !== 'lo')
  const loadFields = rows('load')[0].split(/\s+/)
  if (loadFields.length < 3) throw new Error('系统负载字段不完整')
  return { collectedAt, cpu, memory: { total, available }, disk, network, uptime: number(rows('uptime')[0].split(/\s+/)[0], '运行时间'), load: loadFields.slice(0, 3).map(n => number(n, '负载')) }
}

interface ResourceQuery { promise: Promise<ResourceSnapshot>; cancel(): void }
export function queryResources(client: Client, timeoutMs = 10000): ResourceQuery {
  let channel: ClientChannel | undefined, settled = false, cancel = () => {}
  const promise = new Promise<ResourceSnapshot>((resolve, reject) => {
    const stdout: Buffer[] = [], stderr: Buffer[] = []
    let size = 0, code: number | undefined
    const close = () => { try { channel?.signal('TERM') } catch {} try { channel?.close() } catch {} channel?.destroy() }
    const fail = (reason: string) => { if (settled) return; settled = true; clearTimeout(timer); reject(new Error(reason)); close() }
    const timer = setTimeout(() => fail('读取服务器资源超时，请重试'), timeoutMs)
    cancel = () => fail('资源采集已取消')
    const collect = (target: Buffer[], chunk: Buffer) => { if (settled) return; size += chunk.length; if (size > 512 * 1024) { fail('资源输出超过 512 KiB，已停止采集'); return } target.push(Buffer.from(chunk)) }
    try {
      client.exec(RESOURCE_COMMAND, (error, stream) => {
        if (error) { fail('无法采集服务器资源：' + error.message); return }
        channel = stream
        stream.on('error', (e: Error) => fail(e.message)); stream.stderr.on('error', (e: Error) => fail(e.message))
        if (settled) { close(); return }
        stream.on('data', (data: Buffer) => collect(stdout, data)); stream.stderr.on('data', (data: Buffer) => collect(stderr, data))
        stream.on('exit', (value: number | null, signal?: string) => { if (typeof value === 'number') code = value; if (signal) fail('资源采集被服务器终止') })
        stream.on('close', (value?: number) => {
          if (settled) return
          if (typeof value === 'number') code = value
          if (code !== 0) { fail('资源采集失败；当前支持 Linux 服务器。' + Buffer.concat(stderr).toString('utf8').slice(0, 2000)); return }
          try { const snapshot = parseResources(Buffer.concat(stdout).toString('utf8')); settled = true; clearTimeout(timer); resolve(snapshot) }
          catch (e) { fail((e as Error).message) }
        })
      })
    } catch (e) { fail((e as Error).message) }
  })
  return { promise, cancel: () => cancel() }
}

export class ResourceService {
  private queries = new Map<string, ResourceQuery>()
  constructor(private getClient: (id: string) => Client) {}
  async status(id: string): Promise<ResourceSnapshot> {
    const client = this.getClient(id)
    let query = this.queries.get(id)
    if (!query) { query = queryResources(client); this.queries.set(id, query) }
    try { return await query.promise } finally { if (this.queries.get(id) === query) this.queries.delete(id) }
  }
  cancel(id: string): void { const query = this.queries.get(id); this.queries.delete(id); query?.cancel() }
  dispose(): void { for (const id of this.queries.keys()) this.cancel(id) }
}
