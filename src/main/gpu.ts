import type { Client, ClientChannel } from 'ssh2'
import type { GpuSnapshot } from '../shared/types'
import { GPU_QUERY_COMMAND, parseGpuDevices } from './gpu-data'

export interface GpuQuery { promise: Promise<GpuSnapshot>; cancel(): void }
const MAX_OUTPUT_BYTES = 256 * 1024

// One bounded, read-only exec channel. Never writes commands into the user's PTY.
export function queryNvidiaSmi(client: Client, timeoutMs = 10000): GpuQuery {
  let channel: ClientChannel | undefined, settled = false
  let cancel = () => {}
  const promise = new Promise<GpuSnapshot>((resolve, reject) => {
    const stdout: Buffer[] = [], stderr: Buffer[] = []
    let bytes = 0, exitCode: number | undefined, exitSignal: string | undefined
    function closeChannel(): void {
      try { channel?.signal('TERM') } catch { /* The remote server may not support signals. */ }
      try { channel?.close() } catch { /* It may already be closed. */ }
      channel?.destroy()
    }
    function fail(message: string): void {
      if (settled) return
      settled = true; clearTimeout(timer)
      reject(new Error(message)); closeChannel()
    }
    const timer = setTimeout(() => fail('读取显卡状态超时（10 秒），请检查服务器或驱动后重试。'), timeoutMs)
    cancel = () => fail('显卡状态采集已取消')
    function collect(target: Buffer[], data: Buffer): void {
      if (settled) return
      bytes += data.length
      if (bytes > MAX_OUTPUT_BYTES) { fail('显卡状态输出超过 256 KiB，已停止本次采集。'); return }
      target.push(Buffer.from(data))
    }
    try {
      client.exec(GPU_QUERY_COMMAND, (error, stream) => {
        if (error) { fail('无法读取显卡状态：' + error.message); return }
        channel = stream
        stream.on('error', (error: Error) => fail('显卡状态通道错误：' + error.message))
        stream.stderr.on('error', (error: Error) => fail('显卡状态通道错误：' + error.message))
        if (settled) { closeChannel(); return }
        stream.on('data', (data: Buffer) => collect(stdout, data))
        stream.stderr.on('data', (data: Buffer) => collect(stderr, data))
        stream.on('exit', (code: number | null, signal?: string) => { if (typeof code === 'number') exitCode = code; exitSignal = signal })
        stream.on('close', (code?: number) => {
          if (settled) return
          if (typeof code === 'number') exitCode = code
          const output = Buffer.concat(stdout).toString('utf8').trimEnd()
          const detail = Buffer.concat(stderr).toString('utf8').trim()
          if (exitCode !== 0 || exitSignal) {
            const reason = exitCode === 127 ? '服务器未找到 nvidia-smi，请确认已安装 NVIDIA 驱动且命令在 PATH 中。'
              : exitCode === 126 ? '服务器无法执行 nvidia-smi，请检查执行权限。'
              : exitCode === 9 ? 'NVIDIA 驱动未加载。'
              : exitCode === undefined ? '显卡采集通道已中断，未收到成功退出状态。'
              : `nvidia-smi 执行失败（退出码 ${exitCode}）。`
            fail(reason + ((detail || output) ? '\n' + (detail || output).slice(0, 4000) : '')); return
          }
          let devices: GpuSnapshot['devices']
          try { devices = parseGpuDevices(output) }
          catch (error) { fail((error as Error).message); return }
          settled = true; clearTimeout(timer)
          resolve({ devices, collectedAt: Date.now(), ...(detail ? { warning: detail.slice(0, 4000) } : {}) })
        })
      })
    } catch (error) { fail('无法读取显卡状态：' + (error as Error).message) }
  })
  return { promise, cancel: () => cancel() }
}
