import { afterEach, describe, expect, it, vi } from 'vitest'
import { generateKeyPairSync } from 'node:crypto'
import { connect as connectTcp, type AddressInfo, type Socket } from 'node:net'
import { Server, utils, type Connection, type ParsedKey } from 'ssh2'
import { SshService } from '../src/main/ssh'
import { connectionIdentity } from '../src/shared/connection'
import type { AppEvent, Profile } from '../src/shared/types'

const cleanup: (() => void | Promise<void>)[] = []
afterEach(async () => { for (const close of cleanup.splice(0).reverse()) await close() })
function makeKey(): string { return generateKeyPairSync('rsa', { modulusLength: 2048, privateKeyEncoding: { type: 'pkcs1', format: 'pem' }, publicKeyEncoding: { type: 'pkcs1', format: 'pem' } }).privateKey }
async function fixture() {
  const clients = new Set<Connection>(), sockets = new Set<Socket>()
  const server = new Server({ hostKeys: [makeKey()] }, client => {
    clients.add(client); client.on('error', () => {}); client.on('close', () => clients.delete(client))
    client.on('authentication', auth => auth.method === 'password' && auth.username === 'test' && auth.password === 'test-only-password' ? auth.accept() : auth.reject())
    client.on('ready', () => {
      client.on('session', accept => {
        const session = accept()
        session.on('pty', accept => accept())
        session.on('shell', accept => { const stream = accept(); stream.on('error', () => {}); stream.on('data', (data: Buffer) => stream.write(data)) })
        session.on('sftp', accept => { const sftp = accept(); sftp.on('REALPATH', (id, _path) => sftp.name(id, [{ filename: '/home/test', longname: '/home/test', attrs: { mode: 0o40755, size: 0, uid: 1000, gid: 1000, atime: 1, mtime: 1 } }])) })
      })
      client.on('tcpip', (accept, reject, info) => {
        const socket = connectTcp(info.destPort, info.destIP); sockets.add(socket); socket.on('close', () => sockets.delete(socket))
        socket.once('error', () => { reject(); socket.destroy() })
        socket.once('connect', () => {
          const stream = accept(); stream.on('error', () => socket.destroy()); socket.on('error', () => stream.destroy())
          stream.pipe(socket); socket.pipe(stream); stream.on('close', () => socket.destroy())
        })
      })
    })
  })
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = (server.address() as AddressInfo).port
  cleanup.push(async () => { for (const socket of sockets) socket.destroy(); for (const client of clients) client.end(); await new Promise<void>(resolve => server.close(() => resolve())) })
  const profile: Profile = { id: 'rekey-' + port, name: 'Rekey fixture', group: '', host: '127.0.0.1', port, username: 'test', authType: 'password', keyPath: '', initialPath: '', timeout: 5, keepalive: true, rememberCredentials: false }
  return { clients, profile, get connection() { const connection = [...clients][0]; if (!connection) throw new Error('Server not connected'); return connection } }
}
function clientFixture() {
  const events: AppEvent[] = [], hosts = new Map<string, string>()
  const service = new SshService(event => { events.push(event); if (event.type === 'hostkey') void service.trust(event.sessionId, true) }, { fingerprint: host => hosts.get(host), trust: async (host, fingerprint) => { hosts.set(host, fingerprint) } })
  cleanup.push(() => service.dispose())
  return { events, service, hosts }
}
const password = { password: 'test-only-password' }
async function rekey(connection: Connection): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('Rekey timed out')), 5000)
    connection.rekey(() => { clearTimeout(timer); resolve() })
  })
}
function rotateKey(connection: Connection): void {
  const parsed = utils.parseKey(makeKey())
  if (parsed instanceof Error || Array.isArray(parsed)) throw new Error('Invalid fixture key')
  // The fixture intentionally simulates a server changing its key during an
  // already established connection. ssh2 exposes rekey publicly, but not key rotation.
  const protocol = (connection as unknown as { _protocol: { _hostKeys: Record<string, ParsedKey> } })._protocol
  for (const algorithm of Object.keys(protocol._hostKeys)) protocol._hostKeys[algorithm] = parsed
}
async function expectEcho(service: SshService, events: AppEvent[], id: string, value: string): Promise<void> {
  service.input(id, value)
  await vi.waitFor(() => expect(events.some(event => event.type === 'terminal' && event.sessionId === id && Buffer.from(event.data, 'base64').toString().includes(value))).toBe(true))
}

describe('transport verification during SSH rekey', () => {
  it('keeps phases stable through rekey, including after the original terminal closes', async () => {
    const server = await fixture(), { service, events } = clientFixture()
    service.connect('original', server.profile, password)
    await vi.waitFor(() => expect(service.connected('original')).toBe(true))
    service.newTerminal('peer', 'original', server.profile)
    await vi.waitFor(() => expect(service.connected('peer')).toBe(true))
    let before = events.length
    await rekey(server.connection)
    expect(events.slice(before).filter(event => event.type === 'state' || event.type === 'hostkey')).toEqual([])
    expect(service.connected('original')).toBe(true); expect(service.connected('peer')).toBe(true)
    service.disconnect('original'); before = events.length
    await rekey(server.connection)
    expect(events.slice(before).filter(event => event.type === 'state' || event.type === 'hostkey')).toEqual([])
    expect(service.connected('peer')).toBe(true)
    await expectEcho(service, events, 'peer', 'peer-survives-rekey')
    service.disconnect('peer')
    await vi.waitFor(() => expect(server.clients.size).toBe(0))
    expect(() => service.clientFor('peer')).toThrow('请先连接服务器')
  })

  it('keeps separate verified keys for the gateway and target while a peer owns the shared transport', async () => {
    const gateway = await fixture(), target = await fixture(), { service, events, hosts } = clientFixture()
    const profile = { ...target.profile, jumpProfileId: gateway.profile.id, jumpIdentity: connectionIdentity(gateway.profile) }
    service.connect('original', profile, password, { profile: gateway.profile, credentials: password })
    await vi.waitFor(() => expect(service.connected('original')).toBe(true), { timeout: 7000 })
    service.newTerminal('peer', 'original', profile)
    await vi.waitFor(() => expect(service.connected('peer')).toBe(true))
    service.disconnect('original'); const before = events.length
    await rekey(gateway.connection); await rekey(target.connection)
    expect(events.slice(before).filter(event => event.type === 'state' || event.type === 'hostkey')).toEqual([])
    expect(service.connected('peer')).toBe(true); expect(hosts.size).toBe(2)
    await expectEcho(service, events, 'peer', 'both-transports-survive-rekey')
  })

  it.each(['gateway', 'target'] as const)('rejects an unexpected %s key during rekey and asks for a fresh connection', async changed => {
    const gateway = await fixture(), target = await fixture(), { service, events, hosts } = clientFixture()
    const profile = { ...target.profile, jumpProfileId: gateway.profile.id, jumpIdentity: connectionIdentity(gateway.profile) }
    service.connect('original', profile, password, { profile: gateway.profile, credentials: password })
    await vi.waitFor(() => expect(service.connected('original')).toBe(true), { timeout: 7000 })
    service.newTerminal('peer', 'original', profile)
    await vi.waitFor(() => expect(service.connected('peer')).toBe(true))
    service.disconnect('original')
    const remembered = [...hosts.entries()], prompts = events.filter(event => event.type === 'hostkey').length
    const connection = changed === 'gateway' ? gateway.connection : target.connection
    rotateKey(connection); connection.rekey()
    await vi.waitFor(() => expect(events.some(event => event.type === 'state' && event.sessionId === 'peer' && event.phase === 'failed' && event.message.includes('请重新连接'))).toBe(true))
    expect(service.connected('peer')).toBe(false); expect([...hosts.entries()]).toEqual(remembered)
    expect(events.filter(event => event.type === 'hostkey')).toHaveLength(prompts)
    await vi.waitFor(() => expect(gateway.clients.size + target.clients.size).toBe(0))
  })
})
