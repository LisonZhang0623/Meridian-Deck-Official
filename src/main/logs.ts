import { randomUUID } from 'node:crypto'
import { StringDecoder } from 'node:string_decoder'
import type { Client, ClientChannel } from 'ssh2'
import type { LogEvent, LogOptions } from '../shared/v2'
import { remotePath, shellQuote } from './validation'

const FLUSH_MS = 100
const MAX_PENDING = 64 * 1024
const MAX_LINE = 16 * 1024
const MAX_BYTES_SECOND = 512 * 1024
interface Watch { sessionId: string; id: string; identity: string; stop(): void }

export function logCommand(options: LogOptions): string {
  if (!options || typeof options !== 'object') throw new Error('无效的日志设置')
  const path = remotePath(options.path)
  if (!path.startsWith('/')) throw new Error('请输入日志文件的绝对路径，例如 /var/log/app.log')
  if (!Number.isInteger(options.lines) || options.lines < 0 || options.lines > 2000) throw new Error('初始日志行数必须是 0–2000 的整数')
  const quoted = shellQuote(path)
  return `if [ ! -f ${quoted} ] || [ ! -r ${quoted} ]; then printf '%s\\n' '日志文件不存在或不可读' >&2; exit 1; fi; exec tail -n ${options.lines} -F -- ${quoted}`
}

// Each watcher owns a single read-only exec channel, never the interactive PTY.
export class LogService {
  private watches = new Map<string, Watch>()
  private disposed = false
  constructor(private getClient: (sessionId: string) => Client, private getIdentity: (sessionId: string) => string, private emit: (event: LogEvent) => void) {}

  start(sessionId: string, options: LogOptions): Promise<string> {
    if (this.disposed) return Promise.reject(new Error('日志追踪已停止'))
    const command = logCommand(options), identity = this.getIdentity(sessionId), client = this.getClient(sessionId)
    this.stop(sessionId)
    const watchId = randomUUID()
    let channel: ClientChannel | undefined, finished = false, accepted = false
    let pending = '', diagnostic = '', lineLength = 0, skippingLine = false, bytes = 0, rateStart = Date.now()
    let flushTimer: ReturnType<typeof setTimeout> | undefined
    const stdout = new StringDecoder('utf8'), stderr = new StringDecoder('utf8')
    let accept!: (id: string) => void, reject!: (error: Error) => void
    const ready = new Promise<string>((resolve, failure) => { accept = resolve; reject = failure })
    const current = () => !finished && this.watches.get(sessionId)?.id === watchId
    const send = (kind: LogEvent['kind'], data: string) => this.emit({ type: 'log', sessionId, watchId, kind, data })
    function closeChannel(): void {
      try { channel?.signal('TERM') } catch { /* Some SSH servers do not implement signals. */ }
      try { channel?.close() } catch { /* Closing an already closed channel is harmless. */ }
      channel?.destroy()
    }
    const flush = () => {
      clearTimeout(flushTimer); flushTimer = undefined
      if (!current() || !pending) return
      const data = pending; pending = ''; send('data', data)
    }
    const finish = (kind: 'end' | 'error', message: string) => {
      if (!current()) return
      flush(); finished = true; clearTimeout(startTimer); clearTimeout(flushTimer)
      this.watches.delete(sessionId)
      if (!accepted) reject(new Error(message))
      send(kind, message); closeChannel()
    }
    const append = (text: string) => {
      if (!current() || !text) return
      let output = ''
      for (const char of text) {
        if (char === '\n' || char === '\r') { output += char; lineLength = 0; skippingLine = false; continue }
        if (skippingLine) continue
        if (++lineLength > MAX_LINE) { output += '… [单行超过 16K 字符，已截断]\n'; skippingLine = true; continue }
        output += char === '\0' ? '�' : char
      }
      pending += output
      if (pending.length > MAX_PENDING) { finish('error', '日志输出过快，已停止追踪；可减少初始行数后重试。'); return }
      flushTimer ||= setTimeout(flush, FLUSH_MS)
    }
    const receive = (data: Buffer, isError: boolean) => {
      if (!current()) return
      try { if (this.getIdentity(sessionId) !== identity) { finish('end', '连接已变化，日志追踪已停止'); return } }
      catch { finish('end', '连接已断开，日志追踪已停止'); return }
      const now = Date.now()
      if (now - rateStart >= 1000) { rateStart = now; bytes = 0 }
      bytes += data.length
      if (bytes > MAX_BYTES_SECOND) { finish('error', '日志每秒输出超过 512 KiB，已停止追踪；请过滤服务器日志后重试。'); return }
      const text = (isError ? stderr : stdout).write(data)
      if (isError) { diagnostic = (diagnostic + text).slice(-4000); append('[tail] ' + text) }
      else append(text)
    }
    const startTimer = setTimeout(() => finish('error', '打开日志通道超时（10 秒），请检查连接后重试。'), 10000)
    this.watches.set(sessionId, { sessionId, id: watchId, identity, stop: () => finish('end', '日志追踪已停止') })
    try {
      client.exec(command, (error, stream) => {
        if (error) { finish('error', '无法打开日志追踪：' + error.message); return }
        channel = stream
        stream.on('error', (error: Error) => finish('error', '日志通道错误：' + error.message))
        stream.stderr.on('error', (error: Error) => finish('error', '日志通道错误：' + error.message))
        if (!current()) { closeChannel(); return }
        accepted = true; clearTimeout(startTimer); accept(watchId)
        let exitCode: number | undefined, exitSignal: string | undefined
        stream.on('data', (data: Buffer) => receive(Buffer.from(data), false))
        stream.stderr.on('data', (data: Buffer) => receive(Buffer.from(data), true))
        stream.on('exit', (code: number | null, signal?: string) => { if (typeof code === 'number') exitCode = code; exitSignal = signal })
        stream.on('close', (code?: number) => {
          if (!current()) return
          if (typeof code === 'number') exitCode = code
          append(stdout.end()); const detail = stderr.end(); if (detail) { diagnostic += detail; append('[tail] ' + detail) }
          const reason = exitCode === 127 ? '服务器未找到 tail 命令。'
            : exitCode === 0 && !exitSignal ? '日志追踪已结束。'
            : exitCode === undefined ? '日志连接中断，请重新开始追踪。'
            : `日志命令执行失败（退出码 ${exitCode}${exitSignal ? '，信号 ' + exitSignal : ''}）。`
          finish(exitCode === 0 && !exitSignal ? 'end' : 'error', reason + (diagnostic.trim() ? '\n' + diagnostic.trim().slice(-4000) : ''))
        })
      })
    } catch (error) { finish('error', '无法打开日志追踪：' + (error as Error).message) }
    return ready
  }
  stop(sessionId: string): void { this.watches.get(sessionId)?.stop() }
  dispose(): void { this.disposed = true; for (const watch of [...this.watches.values()]) watch.stop() }
}
