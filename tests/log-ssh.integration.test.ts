import { describe, expect, it, vi } from 'vitest'
import { createFixture } from './fixture'
import { SshService } from '../src/main/ssh'
import { LogService } from '../src/main/logs'
import type { AppEvent, Profile } from '../src/shared/types'
import type { LogEvent } from '../src/shared/v2'

describe('log cancellation over a real SSH channel', () => {
  it('closes the simulated log process on stop and disposal while preserving the original PTY', async () => {
    const fixture = await createFixture(), events: AppEvent[] = [], logEvents: LogEvent[] = [], hosts = new Map<string, string>()
    const profile: Profile = { id: 'log-probe', name: 'Log probe', group: '', host: '127.0.0.1', port: fixture.port, username: 'test', authType: 'password', keyPath: '', initialPath: '', timeout: 5, keepalive: true, rememberCredentials: false }
    const ssh = new SshService(event => { events.push(event); if (event.type === 'hostkey') void ssh.trust(event.sessionId, true) }, { fingerprint: host => hosts.get(host), trust: async (host, fingerprint) => { hosts.set(host, fingerprint) } })
    const logs = new LogService(id => ssh.clientFor(id), id => ssh.sessionIdentity(id), event => logEvents.push(event))
    try {
      ssh.connect('session', profile, { password: 'test-password' })
      await vi.waitFor(() => expect(ssh.connected('session')).toBe(true))
      const first = await logs.start('session', { path: '/home/test/app.log', lines: 200 })
      await vi.waitFor(() => expect(logEvents.some(event => event.watchId === first && event.kind === 'data' && event.data.includes('training started'))).toBe(true))
      expect(fixture.logs.active).toBe(1)
      logs.stop('session')
      await vi.waitFor(() => expect(fixture.logs.active).toBe(0))
      expect(logEvents.some(event => event.watchId === first && event.kind === 'end')).toBe(true)
      expect(ssh.connected('session')).toBe(true)
      ssh.input('session', 'pty-after-log-stop\r')
      await vi.waitFor(() => expect(events.some(event => event.type === 'terminal' && Buffer.from(event.data, 'base64').toString().includes('received: pty-after-log-stop'))).toBe(true))
      const second = await logs.start('session', { path: '/home/test/app.log', lines: 0 })
      await vi.waitFor(() => expect(fixture.logs.active).toBe(1))
      expect(second).not.toBe(first)
      logs.dispose()
      await vi.waitFor(() => expect(fixture.logs.active).toBe(0))
      expect(ssh.connected('session')).toBe(true)
      expect(fixture.metrics).toEqual({ connections: 1, authentications: 1 })
      expect(fixture.logs.commands).toHaveLength(2)
    } finally { logs.dispose(); ssh.dispose(); await fixture.close() }
  })
})
