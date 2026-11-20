import { afterEach, describe, expect, it, vi } from 'vitest'
import { connect as tcpConnect, type Server } from 'node:net'
import { Duplex } from 'node:stream'
import type { Client, ClientChannel } from 'ssh2'
import { ForwardsService } from '../src/main/forwards'

const state = vi.hoisted(() => ({ controlled: false, servers: [] as any[] }))
vi.mock('node:net', async importOriginal => {
  const actual = await importOriginal<typeof import('node:net')>()
  const { EventEmitter } = await import('node:events')
  return { ...actual, createServer: (...args: unknown[]) => {
    if (!state.controlled) return Reflect.apply(actual.createServer, actual, args)
    const server = Object.assign(new EventEmitter(), {
      listen: vi.fn(), close: vi.fn(), address: vi.fn(() => ({ address: '127.0.0.1', family: 'IPv4', port: 50000 }))
    })
    server.listen.mockReturnValue(server); server.close.mockReturnValue(server)
    state.servers.push(server)
    return server as unknown as Server
  } }
})

const services: ForwardsService[] = []
const config = { name: 'Lifecycle test', localPort: 0, remoteHost: '127.0.0.1', remotePort: 6006 }
function service(client: Client = {} as Client) {
  const instance = new ForwardsService(() => client, () => 'session-generation', () => {})
  services.push(instance); return instance
}
afterEach(() => { for (const item of services.splice(0)) item.dispose(); state.controlled = false; state.servers = [] })

describe('forward listener lifecycle', () => {
  it.each(['stop', 'stopSession', 'dispose'] as const)('settles %s during bind and consumes late bind errors or listening callbacks', async action => {
    state.controlled = true
    const forwards = service(), starting = forwards.start('session', config)
    const rejected = expect(starting).rejects.toThrow('取消')
    const listener = state.servers[0]
    if (action === 'stop') forwards.stop(forwards.list()[0].id)
    else if (action === 'stopSession') forwards.stopSession('session')
    else forwards.dispose()
    await rejected
    expect(() => listener.emit('error', Object.assign(new Error('late bind error'), { code: 'EADDRINUSE' }))).not.toThrow()
    expect(() => listener.emit('listening')).not.toThrow()
    expect(listener.listen).toHaveBeenCalledTimes(1)
    expect(listener.close).toHaveBeenCalled()
    expect(forwards.list()[0].status).toBe('stopped')
    if (action === 'dispose') await expect(forwards.start('session', config)).rejects.toThrow('已停止')
  })

  it('reports an initial bind error and safely consumes subsequent errors', async () => {
    state.controlled = true
    const forwards = service(), starting = forwards.start('session', config)
    const rejected = expect(starting).rejects.toThrow('端口已被占用')
    const listener = state.servers[0]
    listener.emit('error', Object.assign(new Error('occupied'), { code: 'EADDRINUSE' }))
    await rejected
    expect(forwards.list()[0].status).toBe('failed')
    expect(() => listener.emit('error', new Error('late second error'))).not.toThrow()
  })

  it('drains a large response to a slow real TCP client after a normal SSH channel close', async () => {
    const payload = Buffer.alloc(8 * 1024 * 1024, 0x5a)
    Buffer.from('slow response 中文\n').copy(payload)
    const requests: Buffer[] = []
    const client = { forwardOut: vi.fn((_sourceHost: string, _sourcePort: number, _host: string, _port: number, accept: (error: Error | null, channel: ClientChannel) => void) => {
      const channel = new Duplex({
        allowHalfOpen: true,
        read() {},
        write(chunk, _encoding, callback) { requests.push(Buffer.from(chunk)); callback() },
        final(callback) { this.push(payload); this.push(null); callback() }
      })
      accept(null, channel as unknown as ClientChannel)
    }) } as unknown as Client
    const forwards = service(client), forward = await forwards.start('session', config)
    const result = await new Promise<Buffer>((resolve, reject) => {
      const socket = tcpConnect(forward.localPort, '127.0.0.1'), chunks: Buffer[] = []
      const timer = setTimeout(() => { socket.destroy(); reject(new Error('slow response timeout')) }, 10000)
      socket.on('error', error => { clearTimeout(timer); reject(error) })
      socket.once('connect', () => { socket.end('request-complete\n'); socket.pause(); setTimeout(() => socket.resume(), 75) })
      socket.on('data', chunk => { chunks.push(Buffer.from(chunk)); socket.pause(); setTimeout(() => socket.resume(), 2) })
      socket.once('end', () => { clearTimeout(timer); resolve(Buffer.concat(chunks)) })
    })
    expect(Buffer.concat(requests).toString()).toBe('request-complete\n')
    expect(result).toEqual(payload)
    expect(forwards.list()[0].status).toBe('listening')
  })
})
