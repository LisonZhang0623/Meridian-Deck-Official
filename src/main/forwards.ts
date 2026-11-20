import { createServer, type Server, type Socket } from 'node:net'
import { randomUUID } from 'node:crypto'
import type { Client, ClientChannel } from 'ssh2'
import type { ForwardConfig, PortForward } from '../shared/v2'
import { text } from './validation'

interface Tunnel { value: PortForward; server: Server; identity: string; active: boolean; sockets: Set<Socket>; channels: Set<ClientChannel>; cancelStart?: () => void }
export function forwardConfig(input: ForwardConfig): ForwardConfig {
  if (!input || typeof input !== 'object') throw new Error('端口转发配置无效')
  const name = text(input.name, '转发名称', 100).trim()
  const remoteHost = text(input.remoteHost, '远程主机', 255).trim()
  if (!name || !remoteHost || /[\s/\\\x00-\x1f]/.test(remoteHost)) throw new Error('请填写转发名称和有效的远程主机')
  if (!Number.isInteger(input.localPort) || input.localPort < 0 || input.localPort > 65535) throw new Error('本地端口必须为 0–65535 的整数')
  if (!Number.isInteger(input.remotePort) || input.remotePort < 1 || input.remotePort > 65535) throw new Error('远程端口必须为 1–65535 的整数')
  return { name, localPort: input.localPort, remoteHost, remotePort: input.remotePort }
}

// Local forwarding only: never listens on a LAN address and never enables a remote listener.
export class ForwardsService {
  private tunnels = new Map<string, Tunnel>()
  private disposed = false
  constructor(private getClient: (id: string) => Client, private getIdentity: (id: string) => string, private emit: (forwards: PortForward[]) => void) {}
  list(): PortForward[] { return [...this.tunnels.values()].map(item => ({ ...item.value })) }
  private publish(): void { this.emit(this.list()) }
  async start(sessionId: string, input: ForwardConfig): Promise<PortForward> {
    if (this.disposed) throw new Error('端口转发服务已停止')
    const config = forwardConfig(input), identity = this.getIdentity(sessionId)
    this.getClient(sessionId)
    if ([...this.tunnels.values()].filter(t => t.active).length >= 20) throw new Error('最多同时运行 20 个端口转发')
    if (this.tunnels.size >= 100) for (const [id, old] of this.tunnels) { if (!old.active) this.tunnels.delete(id); if (this.tunnels.size < 80) break }
    const value: PortForward = { ...config, id: randomUUID(), sessionId, status: 'listening' }
    const server = createServer({ allowHalfOpen: true }, socket => this.accept(tunnel, socket))
    const tunnel: Tunnel = { value, server, identity, active: true, sockets: new Set(), channels: new Set() }
    this.tunnels.set(value.id, tunnel)
    // Keep one listener for the whole lifetime, including cancelled pending binds.
    // A late EADDRINUSE must not become an unhandled process-level error.
    server.on('error', error => {
      if (!tunnel.active || tunnel.cancelStart) return
      value.error = error.message; this.close(tunnel, 'failed'); this.publish()
    })
    server.on('listening', () => { if (!tunnel.active) server.close() })
    try {
      await new Promise<void>((resolve, reject) => {
        const failed = (error: Error) => { server.off('listening', ready); server.off('error', failed); tunnel.cancelStart = undefined; reject(error) }
        const ready = () => { server.off('error', failed); tunnel.cancelStart = undefined; resolve() }
        tunnel.cancelStart = () => failed(new Error('端口转发启动已取消'))
        server.once('error', failed); server.once('listening', ready)
        server.listen({ host: '127.0.0.1', port: config.localPort, exclusive: true })
      })
      if (!tunnel.active || this.disposed || this.getIdentity(sessionId) !== identity) throw new Error(value.error || '原连接已结束，端口转发已取消')
      const address = server.address()
      if (!address || typeof address === 'string') throw new Error('无法读取本地监听地址')
      value.localPort = address.port
      this.publish()
      return { ...value }
    } catch (error) {
      value.error = (error as NodeJS.ErrnoException).code === 'EADDRINUSE' ? '本地端口已被占用，请更换端口或填写 0 自动分配' : (error as Error).message
      this.close(tunnel, tunnel.active ? 'failed' : tunnel.value.status === 'stopped' ? 'stopped' : 'failed'); this.publish()
      throw new Error(value.error)
    }
  }
  private accept(tunnel: Tunnel, socket: Socket): void {
    socket.on('error', () => {})
    if (!tunnel.active || tunnel.sockets.size >= 32) { socket.destroy(); return }
    tunnel.sockets.add(socket); socket.pause()
    let channel: ClientChannel | undefined, finished = false
    const finish = () => { if (finished) return; finished = true; clearTimeout(timer); tunnel.sockets.delete(socket); if (channel) tunnel.channels.delete(channel); socket.destroy(); channel?.destroy() }
    const timer = setTimeout(() => { tunnel.value.error = '打开转发通道超时，请检查目标服务'; this.publish(); finish() }, 10000)
    socket.once('close', finish)
    try {
      if (this.getIdentity(tunnel.value.sessionId) !== tunnel.identity) throw new Error('原 SSH 会话已变化')
      const client = this.getClient(tunnel.value.sessionId)
      client.forwardOut('127.0.0.1', socket.remotePort || 0, tunnel.value.remoteHost, tunnel.value.remotePort, (error, stream) => {
        if (error) { if (tunnel.active) { tunnel.value.error = '无法访问远程服务：' + error.message; this.publish() } finish(); return }
        channel = stream
        stream.on('error', finish)
        if (finished || !tunnel.active) { stream.destroy(); return }
        clearTimeout(timer); tunnel.channels.add(stream)
        stream.once('close', () => {
          if (finished) return
          if (!stream.readableEnded || stream.errored || !tunnel.active) { finish(); return }
          // pipe() has queued every readable byte before a normal close. Let the
          // local writable side drain and send FIN; destroy() would discard its
          // pending buffer when the browser/receiver is reading slowly.
          tunnel.channels.delete(stream); socket.unpipe(stream)
          if (!socket.destroyed && !socket.writableEnded) socket.end()
        })
        tunnel.value.error = undefined; this.publish()
        socket.pipe(stream); stream.pipe(socket); socket.resume()
      })
    } catch (error) { tunnel.value.error = (error as Error).message; this.close(tunnel, 'failed'); this.publish(); finish() }
  }
  private close(tunnel: Tunnel, status: 'stopped' | 'failed'): void {
    tunnel.active = false; tunnel.value.status = status
    tunnel.cancelStart?.()
    try { tunnel.server.close() } catch { /* It may still be opening. */ }
    for (const socket of tunnel.sockets) socket.destroy()
    for (const channel of tunnel.channels) channel.destroy()
    tunnel.sockets.clear(); tunnel.channels.clear()
  }
  stop(id: string): void { const tunnel = this.tunnels.get(id); if (!tunnel) return; this.close(tunnel, 'stopped'); this.publish() }
  stopSession(sessionId: string): void { for (const tunnel of this.tunnels.values()) if (tunnel.value.sessionId === sessionId && tunnel.active) this.close(tunnel, 'stopped'); this.publish() }
  dispose(): void { this.disposed = true; for (const tunnel of this.tunnels.values()) this.close(tunnel, 'stopped'); this.publish() }
}
