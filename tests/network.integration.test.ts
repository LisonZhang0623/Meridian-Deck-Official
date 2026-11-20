import { afterEach, describe, expect, it, vi } from 'vitest'
import { Server as SshServer, type Connection } from 'ssh2'
import { createServer, connect as tcpConnect, type Socket, type AddressInfo } from 'node:net'
import { generateKeyPairSync } from 'node:crypto'
import { mkdtemp, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { SshService } from '../src/main/ssh'
import { ForwardsService, forwardConfig } from '../src/main/forwards'
import { Storage } from '../src/main/storage'
import { connectionIdentity } from '../src/shared/connection'
import type { AppEvent, Profile } from '../src/shared/types'

const cleanups: (() => Promise<void> | void)[] = []
afterEach(async () => { for (const cleanup of cleanups.splice(0).reverse()) await cleanup() })
async function sshFixture() {
  const key = generateKeyPairSync('rsa', { modulusLength: 2048, privateKeyEncoding: { type: 'pkcs1', format: 'pem' }, publicKeyEncoding: { type: 'pkcs1', format: 'pem' } }).privateKey
  const clients = new Set<Connection>(), sockets = new Set<Socket>()
  const metrics = { connections: 0, auth: 0, shells: 0, forwards: 0 }
  const server = new SshServer({ hostKeys: [key] }, client => {
    clients.add(client); metrics.connections++
    client.on('error', () => {}); client.on('close', () => clients.delete(client))
    client.on('authentication', context => { if (context.method === 'password' && context.username === 'test' && context.password === 'test-password') { metrics.auth++; context.accept() } else context.reject() })
    client.on('ready', () => {
      client.on('tcpip', (accept, reject, info) => {
        metrics.forwards++
        const socket = tcpConnect(info.destPort, info.destIP)
        sockets.add(socket); socket.on('close', () => sockets.delete(socket))
        socket.once('error', () => { reject(); socket.destroy() })
        socket.once('connect', () => { const stream = accept(); stream.on('error', () => socket.destroy()); socket.on('error', () => stream.destroy()); stream.pipe(socket); socket.pipe(stream); stream.on('close', () => socket.destroy()) })
      })
      client.on('session', accept => {
        const session = accept()
        session.on('pty', accept => accept())
        session.on('shell', accept => { metrics.shells++; const stream = accept(); stream.on('error', () => {}); stream.write('network fixture ready\r\n'); stream.on('data', (data: Buffer) => stream.write(data)) })
        session.on('sftp', accept => { const sftp = accept(); sftp.on('REALPATH', (id, _path) => sftp.name(id, [{ filename: '/home/test', longname: '/home/test', attrs: { mode: 0o40755, size: 0, uid: 1000, gid: 1000, atime: 1, mtime: 1 } }])) })
      })
    })
  })
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = (server.address() as AddressInfo).port
  cleanups.push(async () => { for (const socket of sockets) socket.destroy(); for (const client of clients) client.end(); await new Promise<void>(resolve => server.close(() => resolve())) })
  const profile: Profile = { id: 'server-' + port, name: 'Server', group: '', host: '127.0.0.1', port, username: 'test', authType: 'password', keyPath: '', initialPath: '', timeout: 5, keepalive: true, rememberCredentials: false }
  return { port, profile, clients, metrics }
}
function serviceFixture() {
  const events: AppEvent[] = [], hosts = new Map<string, string>()
  const service = new SshService(event => { events.push(event); if (event.type === 'hostkey') void service.trust(event.sessionId, true) }, { fingerprint: host => hosts.get(host), trust: async (host, fingerprint) => { hosts.set(host, fingerprint) } })
  cleanups.push(() => service.dispose())
  return { service, events, hosts }
}
async function echoFixture() {
  const sockets = new Set<Socket>()
  const server = createServer(socket => { sockets.add(socket); socket.on('error', () => {}); socket.on('close', () => sockets.delete(socket)); socket.pipe(socket) })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  cleanups.push(async () => { for (const socket of sockets) socket.destroy(); await new Promise<void>(resolve => server.close(() => resolve())) })
  return (server.address() as AddressInfo).port
}
async function exchange(port: number, data: Buffer): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const socket = tcpConnect(port, '127.0.0.1'), chunks: Buffer[] = []
    const timer = setTimeout(() => { socket.destroy(); reject(new Error('echo timed out')) }, 5000)
    socket.on('error', error => { clearTimeout(timer); reject(error) })
    socket.on('connect', () => socket.write(data))
    socket.on('data', chunk => { chunks.push(Buffer.from(chunk)); const all = Buffer.concat(chunks); if (all.length >= data.length) { clearTimeout(timer); socket.destroy(); resolve(all) } })
  })
}

describe('single jump and local port forwarding', () => {
  it('verifies both hosts, reuses the tunneled transport, and keeps its jump alive until the last terminal closes', async () => {
    const gateway = await sshFixture(), target = await sshFixture(), { service, events, hosts } = serviceFixture()
    const profile = { ...target.profile, jumpProfileId: gateway.profile.id, jumpIdentity: connectionIdentity(gateway.profile) }
    service.connect('jump-session', profile, { password: 'test-password' }, { profile: gateway.profile, credentials: { password: 'test-password' } })
    await vi.waitFor(() => expect(service.connected('jump-session')).toBe(true), { timeout: 7000 })
    expect(hosts.size).toBe(2); expect(events.filter(event => event.type === 'hostkey')).toHaveLength(2)
    service.newTerminal('jump-peer', 'jump-session', profile)
    await vi.waitFor(() => expect(service.connected('jump-peer')).toBe(true))
    service.disconnect('jump-session'); expect(gateway.clients.size).toBe(1)
    service.input('jump-peer', 'peer-still-alive')
    await vi.waitFor(() => expect(events.some(event => event.type === 'terminal' && event.sessionId === 'jump-peer' && Buffer.from(event.data, 'base64').toString().includes('peer-still-alive'))).toBe(true))
    expect(gateway.metrics.auth).toBe(1); expect(target.metrics.auth).toBe(1); expect(target.metrics.shells).toBe(2)
    service.disconnect('jump-peer')
    await vi.waitFor(() => expect(gateway.clients.size).toBe(0))
  })
  it('fails a wrong jump login without reaching the target and rejects missing or chained jumps', async () => {
    const gateway = await sshFixture(), target = await sshFixture(), { service, events } = serviceFixture()
    const profile = { ...target.profile, jumpProfileId: gateway.profile.id }
    service.connect('bad-jump', profile, {}, { profile: gateway.profile, credentials: { password: 'wrong' } })
    await vi.waitFor(() => expect(events.some(event => event.type === 'state' && event.sessionId === 'bad-jump' && event.phase === 'failed')).toBe(true))
    expect(target.metrics.connections).toBe(0)
    expect(() => service.connect('missing', profile, {})).toThrow('跳板机配置无效')
    expect(() => service.connect('chain', profile, {}, { profile: { ...gateway.profile, jumpProfileId: 'another' }, credentials: {} })).toThrow('单跳')
    await vi.waitFor(() => expect(gateway.clients.size).toBe(0))
  })
  it('cancels either host-key prompt without leaving jump or target transports open', async () => {
    const gateway = await sshFixture(), target = await sshFixture(), events: AppEvent[] = [], hosts = new Map<string, string>()
    const service = new SshService(event => events.push(event), { fingerprint: host => hosts.get(host), trust: async (host, fingerprint) => { hosts.set(host, fingerprint) } })
    cleanups.push(() => service.dispose())
    const profile = { ...target.profile, jumpProfileId: gateway.profile.id }
    const connect = (id: string) => service.connect(id, profile, { password: 'test-password' }, { profile: gateway.profile, credentials: { password: 'test-password' } })
    connect('cancel-gateway')
    await vi.waitFor(() => expect(events.some(event => event.type === 'hostkey' && event.sessionId === 'cancel-gateway')).toBe(true))
    await service.trust('cancel-gateway', false)
    await vi.waitFor(() => expect(gateway.clients.size).toBe(0))
    expect(target.metrics.connections).toBe(0)
    connect('cancel-target')
    await vi.waitFor(() => expect(events.some(event => event.type === 'hostkey' && event.sessionId === 'cancel-target' && event.host.endsWith(':' + gateway.port))).toBe(true))
    await service.trust('cancel-target', true)
    await vi.waitFor(() => expect(events.some(event => event.type === 'hostkey' && event.sessionId === 'cancel-target' && event.host.endsWith(':' + target.port))).toBe(true))
    await service.trust('cancel-target', false)
    await vi.waitFor(() => expect(gateway.clients.size + target.clients.size).toBe(0))
    expect(service.connected('cancel-target')).toBe(false)
  })
  it('forwards real TCP bytes through SSH on loopback only and closes its listener when stopped', async () => {
    const target = await sshFixture(), echoPort = await echoFixture(), { service } = serviceFixture()
    service.connect('forward-session', target.profile, { password: 'test-password' })
    await vi.waitFor(() => expect(service.connected('forward-session')).toBe(true))
    const forwards = new ForwardsService(id => service.clientFor(id), id => service.sessionIdentity(id), () => {})
    cleanups.push(() => forwards.dispose())
    const forward = await forwards.start('forward-session', { name: 'Echo', localPort: 0, remoteHost: '127.0.0.1', remotePort: echoPort })
    expect(forward.localPort).toBeGreaterThan(0)
    const bytes = Buffer.from('hello\0世界\xff', 'utf8')
    expect(await exchange(forward.localPort, bytes)).toEqual(bytes)
    expect(target.metrics.forwards).toBe(1)
    forwards.stop(forward.id)
    await expect(exchange(forward.localPort, Buffer.from('closed'))).rejects.toMatchObject({ code: 'ECONNREFUSED' })
    expect(forwards.list()[0].status).toBe('stopped')
  })
  it('reports occupied ports, isolates session cleanup and rejects invalid forwarding input', async () => {
    const target = await sshFixture(), { service } = serviceFixture()
    service.connect('forward-session', target.profile, { password: 'test-password' })
    await vi.waitFor(() => expect(service.connected('forward-session')).toBe(true))
    const forwards = new ForwardsService(id => service.clientFor(id), id => service.sessionIdentity(id), () => {})
    cleanups.push(() => forwards.dispose())
    const config = { name: 'Port', localPort: 0, remoteHost: '127.0.0.1', remotePort: 6006 }
    const first = await forwards.start('forward-session', config)
    await expect(forwards.start('forward-session', { ...config, localPort: first.localPort })).rejects.toThrow('占用')
    forwards.stopSession('another'); expect(forwards.list().find(f => f.id === first.id)?.status).toBe('listening')
    forwards.stopSession('forward-session'); expect(forwards.list().find(f => f.id === first.id)?.status).toBe('stopped')
    for (const value of [{ ...config, localPort: -1 }, { ...config, remotePort: 0 }, { ...config, remoteHost: 'bad host' }]) expect(() => forwardConfig(value)).toThrow()
  })
  it('settles cancellation while the local listener is still starting', async () => {
    const forwards = new ForwardsService(() => ({} as never), () => 'connected-session', () => {})
    cleanups.push(() => forwards.dispose())
    const starting = forwards.start('session', { name: 'Cancelled', localPort: 0, remoteHost: '127.0.0.1', remotePort: 6006 })
    forwards.stopSession('session')
    await expect(starting).rejects.toThrow('取消')
    expect(forwards.list()[0].status).toBe('stopped')
  })
  it('persists jump references, rejects cycles and deletion, and changes dependent connection identities after editing a jump', async () => {
    const root = await mkdtemp(join(tmpdir(), 'sshtool-jump-storage-'))
    const store = new Storage(root, { encrypt: async value => value, decrypt: async value => value }); await store.init()
    const base: Profile = { id: 'gateway', name: 'Gateway', group: '', host: 'gateway.test', port: 22, username: 'test', authType: 'password', keyPath: '', initialPath: '', timeout: 5, keepalive: true, rememberCredentials: false }
    await store.saveProfile(base, {})
    await store.saveProfile({ ...base, id: 'target', host: 'target.test', jumpProfileId: base.id }, {})
    const previous = connectionIdentity(store.get('target'))
    await store.recordConnected('target', previous, 1000)
    await expect(store.delete(base.id)).rejects.toThrow('用作跳板机')
    await expect(store.saveProfile({ ...base, jumpProfileId: 'target' }, {})).rejects.toThrow('单跳')
    await expect(store.saveProfile({ ...base, id: 'self', jumpProfileId: 'self' }, {})).rejects.toThrow()
    await store.saveProfile({ ...base, host: 'new-gateway.test' }, {})
    expect(connectionIdentity(store.get('target'))).not.toBe(previous)
    expect(store.get('target').lastConnectedAt).toBeUndefined()
    expect(await readFile(join(root, 'settings.json'), 'utf8')).toContain('jumpProfileId')
    expect(await readFile(join(root, 'settings.json'), 'utf8')).not.toContain('jumpIdentity')
  })
})
