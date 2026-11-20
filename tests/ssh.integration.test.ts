import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { mkdtemp, writeFile, readFile, readdir } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { SshService } from '../src/main/ssh'
import { RemoteFiles } from '../src/main/files'
import { Transfers } from '../src/main/transfers'
import { createFixture, GPU_COMMAND, GPU_SAMPLE } from './fixture'
import type { AppEvent, Profile, Transfer } from '../src/shared/types'
import { connectionIdentity } from '../src/shared/connection'

describe('real SSH and SFTP against a loopback server', () => {
  let fixture: Awaited<ReturnType<typeof createFixture>>, service: SshService, files: RemoteFiles, profile: Profile
  let events: AppEvent[], hosts: Map<string, string>
  let recorded: { profileId: string; identity: string }[]
  let recordFails: boolean
  async function waitFor<T>(get: () => T | undefined): Promise<T> {
    const deadline = Date.now() + 10000
    while (Date.now() < deadline) { const value = get(); if (value !== undefined) return value; await new Promise(resolve => setTimeout(resolve, 20)) }
    throw new Error('Timed out waiting for event: ' + JSON.stringify(events.filter(e => e.type !== 'terminal')))
  }
  async function connect() {
    service.connect('test-session', profile, { password: 'test-password' })
    await waitFor(() => events.find(e => e.type === 'hostkey'))
    await service.trust('test-session', true)
    await waitFor(() => events.find(e => e.type === 'state' && e.phase === 'connected'))
  }
  beforeEach(async () => {
    fixture = await createFixture(); events = []; hosts = new Map(); recorded = []; recordFails = false
    service = new SshService(event => events.push(event), { fingerprint: host => hosts.get(host), trust: async (host, fingerprint) => { hosts.set(host, fingerprint) }, recordConnected: async (profileId, identity) => { if (recordFails) throw new Error('test history write failed'); recorded.push({ profileId, identity }) } })
    files = new RemoteFiles(id => service.sftp(id))
    profile = { id: 'fixture', name: 'Fixture', group: '', host: '127.0.0.1', port: fixture.port, username: 'test', authType: 'password', keyPath: '', initialPath: '', timeout: 5, keepalive: true, rememberCredentials: false }
  })
  afterEach(async () => { service.dispose(); await fixture.close() })
  it('waits for host-key approval, opens a PTY, and reads real SFTP files', async () => {
    await connect()
    expect(hosts.size).toBe(1)
    service.input('test-session', 'hello\r')
    await waitFor(() => events.find(e => e.type === 'terminal' && Buffer.from(e.data, 'base64').toString().includes('received: hello')))
    const listing = await files.list('test-session', '.')
    expect(listing.path).toBe('/home/test')
    expect(listing.entries.find(e => e.name === 'configs')?.type).toBe('directory')
    const preview = await files.preview('test-session', '/home/test/configs/config.json')
    expect(preview.content).toContain('世界')
    expect((await files.preview('test-session', '/home/test/images/pixel.png')).kind).toBe('image')
  })
  it('rejects changed host keys until explicitly accepted and supports cancelling', async () => {
    hosts.set(`[127.0.0.1]:${fixture.port}`, 'SHA256:old-key')
    service.connect('cancel-session', profile, { password: 'test-password' })
    const pending = await waitFor(() => events.find(e => e.type === 'hostkey'))
    expect(pending.type === 'hostkey' && pending.previous).toBe('SHA256:old-key')
    service.disconnect('cancel-session')
    await service.trust('cancel-session', true)
    expect(events.some(e => e.type === 'state' && e.phase === 'connected')).toBe(false)
    expect(hosts.get(`[127.0.0.1]:${fixture.port}`)).toBe('SHA256:old-key')
  })
  it('opens independent terminals without authenticating again, and keeps peers alive when the original closes', async () => {
    await connect()
    const originalIdentity = service.sessionIdentity('test-session')
    service.newTerminal('second-session', 'test-session', profile)
    await waitFor(() => events.find(e => e.type === 'state' && e.sessionId === 'second-session' && e.phase === 'connected'))
    service.disconnect('test-session')
    service.input('second-session', 'second-only\r')
    await waitFor(() => events.find(e => e.type === 'terminal' && e.sessionId === 'second-session' && Buffer.from(e.data, 'base64').toString().includes('received: second-only')))
    expect((await files.list('second-session', '.')).path).toBe('/home/test')
    service.newTerminal('third-session', 'second-session', profile)
    await waitFor(() => events.find(e => e.type === 'state' && e.sessionId === 'third-session' && e.phase === 'connected'))
    service.disconnect('second-session')
    expect((await files.list('third-session', '.')).path).toBe('/home/test')
    expect(fixture.metrics).toEqual({ connections: 1, authentications: 1 })
    expect(events.filter(e => e.type === 'hostkey')).toHaveLength(1)
    expect(recorded).toEqual(Array(3).fill({ profileId: profile.id, identity: connectionIdentity(profile) }))
    const thirdIdentity = service.sessionIdentity('third-session')
    expect(thirdIdentity.length).toBeGreaterThan(0)
    expect(service.sessionIdentity('third-session')).toBe(thirdIdentity)
    expect(thirdIdentity).not.toBe(originalIdentity)
    expect(() => service.sessionIdentity('test-session')).toThrow('连接已断开')
  })
  it('invalidates the drag identity after the same session id reconnects to the same saved server', async () => {
    await connect()
    const firstIdentity = service.sessionIdentity('test-session')
    expect(firstIdentity.length).toBeGreaterThan(0)
    expect(service.sessionIdentity('test-session')).toBe(firstIdentity)
    service.disconnect('test-session')
    expect(() => service.sessionIdentity('test-session')).toThrow('连接已断开')
    service.connect('test-session', profile, { password: 'test-password' })
    expect(() => service.sessionIdentity('test-session')).toThrow('连接已断开')
    await waitFor(() => service.connected('test-session') ? true : undefined)
    const secondIdentity = service.sessionIdentity('test-session')
    expect(secondIdentity.length).toBeGreaterThan(0)
    expect(secondIdentity).not.toBe(firstIdentity)
    expect(service.sessionIdentity('test-session')).toBe(secondIdentity)
    expect((await files.list('test-session', '.')).path).toBe('/home/test')
    expect(recorded).toEqual(Array(2).fill({ profileId: profile.id, identity: connectionIdentity(profile) }))
    service.disconnect('test-session')
    expect(() => service.sessionIdentity('test-session')).toThrow('连接已断开')
  })
  it('does not interrupt existing terminals if a new shell is refused or the configured identity has changed', async () => {
    await connect()
    expect(() => service.newTerminal('wrong-server', 'test-session', { ...profile, username: 'another-user' })).toThrow('配置已变化')
    fixture.behavior.rejectNextShell = true
    service.newTerminal('refused-shell', 'test-session', profile)
    await waitFor(() => events.find(e => e.type === 'state' && e.sessionId === 'refused-shell' && e.phase === 'failed'))
    expect(service.connected('test-session')).toBe(true)
    expect((await files.list('test-session', '.')).path).toBe('/home/test')
    expect(recorded).toHaveLength(1)
  })
  it('opens a directory in an isolated terminal using a literal quoted canonical path', async () => {
    await connect()
    const directory = "/home/test/实验 it's $(touch pwned); [data]"
    fixture.files.set(directory, { data: Buffer.alloc(0), mode: 0o40755, mtime: 1700000000 })
    service.newTerminal('directory-session', 'test-session', profile, directory)
    const connected = await waitFor(() => events.find(e => e.type === 'state' && e.sessionId === 'directory-session' && e.phase === 'connected'))
    expect(connected.type === 'state' && connected.home).toBe(directory)
    const command = await waitFor(() => fixture.shells.commands.find(item => item.shell === 2))
    expect(command.command).toBe("cd -- '/home/test/实验 it'\\''s $(touch pwned); [data]' || exit")
    expect(fixture.shells.commands.some(item => item.shell === 1)).toBe(false)
    expect(profile.initialPath).toBe('')
    expect((await files.list('test-session', '.')).path).toBe('/home/test')
    expect((await files.list('directory-session', directory)).path).toBe(directory)
    expect(fixture.metrics).toEqual({ connections: 1, authentications: 1 })
    expect(recorded).toHaveLength(2)
    service.disconnect('directory-session')
    expect(service.connected('test-session')).toBe(true)
  })
  it('rejects invalid directory shortcuts before opening a PTY and leaves the source terminal usable', async () => {
    await connect()
    expect(() => service.newTerminal('newline-directory', 'test-session', profile, '/home/test\nexit')).toThrow('换行')
    for (const [id, directory] of [['missing-directory', '/home/test/removed'], ['not-directory', '/home/test/hello.txt']]) {
      service.newTerminal(id, 'test-session', profile, directory)
      const failed = await waitFor(() => events.find(e => e.type === 'state' && e.sessionId === id && e.phase === 'failed'))
      expect(failed.type === 'state' && failed.message).toContain('无法在此目录打开终端')
      expect(events.some(e => e.type === 'state' && e.sessionId === id && e.phase === 'connected')).toBe(false)
    }
    expect(fixture.shells.opened).toBe(1)
    expect(recorded).toHaveLength(1)
    service.input('test-session', 'still-here\r')
    await waitFor(() => fixture.shells.commands.find(item => item.shell === 1 && item.command === 'still-here'))
    expect((await files.list('test-session', '.')).path).toBe('/home/test')
  })
  it('uses the resolved directory rather than an unnormalized shortcut path', async () => {
    await connect()
    service.newTerminal('canonical-directory', 'test-session', profile, '/home/test/configs/../images')
    const connected = await waitFor(() => events.find(e => e.type === 'state' && e.sessionId === 'canonical-directory' && e.phase === 'connected'))
    expect(connected.type === 'state' && connected.home).toBe('/home/test/images')
    const command = await waitFor(() => fixture.shells.commands.find(item => item.shell === 2))
    expect(command.command).toBe("cd -- '/home/test/images' || exit")
  })
  it('keeps an authenticated terminal usable when recent-history persistence fails', async () => {
    recordFails = true
    const log = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      await connect()
      expect(service.connected('test-session')).toBe(true)
      expect((await files.list('test-session', '.')).path).toBe('/home/test')
      expect(recorded).toHaveLength(0)
      expect(log).toHaveBeenCalledWith('记录最近连接失败：', expect.any(Error))
    } finally { log.mockRestore() }
  })
  it('reports transport loss to all terminals sharing the connection', async () => {
    await connect()
    service.newTerminal('second-session', 'test-session', profile)
    await waitFor(() => events.find(e => e.type === 'state' && e.sessionId === 'second-session' && e.phase === 'connected'))
    fixture.disconnectClients()
    for (const id of ['test-session', 'second-session']) {
      await waitFor(() => events.find(e => e.type === 'state' && e.sessionId === id && ['failed', 'disconnected'].includes(e.phase)))
      expect(service.connected(id)).toBeFalsy()
    }
  })
  it('reports authentication failure without accepting bad credentials', async () => {
    service.connect('test-session', profile, { password: 'wrong-password' })
    await waitFor(() => events.find(e => e.type === 'hostkey')); await service.trust('test-session', true)
    const failed = await waitFor(() => events.find(e => e.type === 'state' && e.phase === 'failed'))
    expect(failed.type === 'state' && failed.message).toContain('身份验证失败')
    expect(recorded).toHaveLength(0)
  })
  it('queries GPUs on a separate exec channel and deduplicates concurrent reads', async () => {
    await connect(); fixture.gpu.delayMs = 30
    const [first, same] = await Promise.all([service.gpuStatus('test-session'), service.gpuStatus('test-session')])
    expect(first).toEqual(same)
    expect(first.devices).toHaveLength(2)
    expect(first.devices[0]).toMatchObject({ index: 0, name: 'NVIDIA RTX 4090', utilization: 78, memoryUsedMiB: 12288, temperatureC: 62, powerDrawW: 245.5 })
    expect(first.devices[1]).toMatchObject({ index: 1, utilization: 3, memoryUsedMiB: 512 })
    expect(first.collectedAt).toBeGreaterThan(0)
    expect(fixture.gpu.commands).toEqual([GPU_COMMAND])
    expect(events.filter(e => e.type === 'terminal').some(e => Buffer.from(e.data, 'base64').toString().includes('NVIDIA'))).toBe(false)
    await service.gpuStatus('test-session')
    expect(fixture.gpu.commands).toHaveLength(2)
    expect(fixture.metrics.authentications).toBe(1)
    expect((await files.list('test-session', '.')).path).toBe('/home/test')
  })
  it('reports missing GPU commands without breaking the terminal, then allows retry', async () => {
    await connect(); fixture.gpu.exitCode = 127; fixture.gpu.output = ''; fixture.gpu.stderr = 'nvidia-smi: command not found'
    await expect(service.gpuStatus('test-session')).rejects.toThrow('未找到 nvidia-smi')
    expect(service.connected('test-session')).toBe(true)
    fixture.gpu.exitCode = 0; fixture.gpu.output = GPU_SAMPLE; fixture.gpu.stderr = ''
    expect((await service.gpuStatus('test-session')).devices).toHaveLength(2)
  })
  it('cancels pending GPU reads on request or disconnect while preserving peer sessions', async () => {
    await connect(); fixture.gpu.hang = true
    const cancelled = expect(service.gpuStatus('test-session')).rejects.toThrow('已取消')
    await waitFor(() => fixture.gpu.commands.length === 1 ? true : undefined)
    service.cancelGpuStatus('test-session'); await cancelled
    expect(service.connected('test-session')).toBe(true)
    service.newTerminal('gpu-peer', 'test-session', profile)
    await waitFor(() => service.connected('gpu-peer') ? true : undefined)
    const disconnected = expect(service.gpuStatus('test-session')).rejects.toThrow('已取消')
    service.disconnect('test-session'); await disconnected
    expect(service.connected('gpu-peer')).toBe(true)
    fixture.gpu.hang = false
    expect((await service.gpuStatus('gpu-peer')).devices[0].name).toContain('NVIDIA RTX')
    await expect(service.gpuStatus('test-session')).rejects.toThrow('请先连接')
  })
  it('detects external edits and atomically saves a matching revision', async () => {
    await connect()
    const path = '/home/test/hello.txt'
    const original = await files.preview('test-session', path)
    fixture.files.get(path)!.data = Buffer.from('Changed by another editor')
    await expect(files.save('test-session', path, 'My edit', original.version)).rejects.toThrow('CONFLICT:')
    expect(fixture.files.get(path)!.data.toString()).toBe('Changed by another editor')
    const latest = await files.preview('test-session', path)
    const updated = await files.save('test-session', path, 'Saved 世界\n', latest.version)
    expect(updated.content).toBe('Saved 世界\n')
    expect([...fixture.files.keys()].some(path => path.endsWith('.tmp'))).toBe(false)
  })
  it('creates, renames and deletes files without overwriting existing names', async () => {
    await connect()
    await files.create('test-session', '/home/test', 'new.txt', false)
    await expect(files.rename('test-session', '/home/test/new.txt', 'hello.txt')).rejects.toThrow('已存在')
    await files.rename('test-session', '/home/test/new.txt', 'renamed.txt')
    await files.delete('test-session', '/home/test/renamed.txt', false)
    expect(fixture.files.has('/home/test/renamed.txt')).toBe(false)
    await expect(files.delete('test-session', '/', true)).rejects.toThrow('根目录')
  })
  it('uploads and downloads binary bytes with progress and temporary-file cleanup', async () => {
    await connect()
    const directory = await mkdtemp(join(tmpdir(), 'sshtool-transfer-'))
    const payload = Buffer.alloc(200000, 183)
    await writeFile(join(directory, 'upload.bin'), payload)
    const updates: Transfer[] = []
    const transfers = new Transfers(id => service.sftp(id), transfer => updates.push(transfer), async () => true)
    transfers.enqueue('test-session', 'upload', join(directory, 'upload.bin'), '/home/test/upload.bin')
    await waitFor(() => updates.find(t => t.direction === 'upload' && ['completed', 'failed'].includes(t.status)))
    expect(updates.at(-1)?.error).toBeUndefined()
    expect(fixture.files.get('/home/test/upload.bin')?.data.equals(payload)).toBe(true)
    transfers.enqueue('test-session', 'download', join(directory, 'download.bin'), '/home/test/upload.bin')
    await waitFor(() => updates.find(t => t.direction === 'download' && ['completed', 'failed'].includes(t.status)))
    expect((await readFile(join(directory, 'download.bin'))).equals(payload)).toBe(true)
    expect((await readdir(directory)).filter(p => p.endsWith('.part'))).toHaveLength(0)
    transfers.dispose()
  })
  it('keeps the original on cancelled overwrite and preserves its permissions on retry', async () => {
    await connect()
    const directory = await mkdtemp(join(tmpdir(), 'sshtool-overwrite-'))
    const localPath = join(directory, 'replacement.txt')
    const remotePath = '/home/test/hello.txt'
    await writeFile(localPath, 'Replacement contents')
    fixture.files.get(remotePath)!.mode = 0o100750
    const updates: Transfer[] = []
    let allowOverwrite = false
    const transfers = new Transfers(id => service.sftp(id), transfer => updates.push(transfer), async () => allowOverwrite)
    transfers.enqueue('test-session', 'upload', localPath, remotePath)
    const cancelled = await waitFor(() => updates.find(t => t.status === 'cancelled'))
    expect(fixture.files.get(remotePath)!.data.toString()).toBe('Hello SSH\n')
    allowOverwrite = true
    transfers.retry(cancelled.id)
    const finished = await waitFor(() => updates.find(t => ['completed', 'failed'].includes(t.status)))
    expect(finished.status).toBe('completed')
    expect(fixture.files.get(remotePath)!.data.toString()).toBe('Replacement contents')
    expect(fixture.files.get(remotePath)!.mode & 0o777).toBe(0o750)
    expect([...fixture.files.keys()].some(path => path.endsWith('.part'))).toBe(false)
    transfers.dispose()
  })
})
