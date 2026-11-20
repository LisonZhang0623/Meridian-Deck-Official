import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventEmitter } from 'node:events'
import { effectScope, reactive } from 'vue'
import type { Client, ClientChannel } from 'ssh2'
import { parseResources, queryResources, ResourceService, RESOURCE_COMMAND } from '../src/main/resources'
import { resourceRates, useResources } from '../src/renderer/src/useResources'
import { RESOURCE_SAMPLE, createFixture } from './fixture'
import { SshService } from '../src/main/ssh'
import type { AppEvent } from '../src/shared/types'

function channelFixture(delayed = false) {
  const stream = Object.assign(new EventEmitter(), { stderr: new EventEmitter(), signal: vi.fn(), close: vi.fn(), destroy: vi.fn() })
  let accept!: () => void
  const client = { exec: vi.fn((_cmd, callback) => { accept = () => callback(null, stream); if (!delayed) accept() }) } as unknown as Client
  return { client, stream, accept: () => accept() }
}
afterEach(() => vi.useRealTimers())

describe('Linux resource snapshots', () => {
  it('parses counters without double counting guest CPU time, in bytes, omitting loopback', () => {
    const result = parseResources(RESOURCE_SAMPLE, 1000)
    expect(result.cpu).toEqual({ total: 10000, idle: 8500 })
    expect(result.memory).toEqual({ total: 32 * 1024 ** 3, available: 16 * 1024 ** 3 })
    expect(result.disk).toEqual({ total: 100 * 1024 ** 3, used: 20 * 1024 ** 3, available: 80 * 1024 ** 3, mount: '/' })
    expect(result.network).toEqual([{ name: 'eth0', received: 100000, sent: 200000 }])
    expect(result.load).toEqual([1.2, 0.8, 0.5])
  })
  it('rejects partial, duplicated, negative, invalid memory and disk records', () => {
    for (const invalid of [RESOURCE_SAMPLE.replace('[load]', '[missing]'), RESOURCE_SAMPLE + '\n[cpu]\ncpu 1 2 3 4', RESOURCE_SAMPLE.replace('1000 0 500', '-1 0 500'), RESOURCE_SAMPLE.replace('16777216 kB', '999999999 kB'), RESOURCE_SAMPLE.replace('20% /', '20% /data')]) expect(() => parseResources(invalid)).toThrow()
  })
  it('calculates interval CPU and network rates and resets them after reboot or counter reset', () => {
    const before = parseResources(RESOURCE_SAMPLE, 1000), after = parseResources(RESOURCE_SAMPLE, 3000)
    after.cpu = { total: 11000, idle: 9350 }; after.network[0].received += 2048; after.network[0].sent += 4096
    expect(resourceRates(after, before).cpu).toBeCloseTo(15)
    expect(resourceRates(after, before).network[0]).toEqual({ name: 'eth0', received: 1024, sent: 2048 })
    expect(resourceRates(after).cpu).toBeNull()
    after.uptime = 1
    expect(resourceRates(after, before)).toEqual({ cpu: null, network: [{ name: 'eth0', received: null, sent: null }] })
    after.uptime = before.uptime; after.network[0].received = 0
    expect(resourceRates(after, before).network[0].received).toBeNull()
  })
  it('requires complete memory and load metrics instead of displaying invented zero availability', () => {
    expect(() => parseResources(RESOURCE_SAMPLE.replace(/MemAvailable:.*\n/, '').replace(/MemFree:.*\n/, ''))).toThrow('可用容量字段缺失')
    expect(() => parseResources(RESOURCE_SAMPLE.replace('1.20 0.80 0.50 1/100 1000', '1.0'))).toThrow('负载字段不完整')
    expect(() => parseResources(RESOURCE_SAMPLE.replace('MemAvailable: 16777216 kB', 'MemAvailable: broken kB'))).toThrow('内存数据')
    expect(() => parseResources(RESOURCE_SAMPLE.replace('104857600 20971520', '9007199254740991 20971520'))).toThrow('磁盘容量')
  })
  it('uses fixed bounded exec, accepts fragmented UTF-8 and requires successful exit', async () => {
    const f = channelFixture(), query = queryResources(f.client)
    expect(f.client.exec).toHaveBeenCalledWith(RESOURCE_COMMAND, expect.any(Function))
    const data = Buffer.from(RESOURCE_SAMPLE)
    f.stream.emit('data', data.subarray(0, 30)); f.stream.emit('data', data.subarray(30)); f.stream.emit('exit', 0); f.stream.emit('close')
    expect((await query.promise).memory.total).toBe(32 * 1024 ** 3)
    const failed = channelFixture(), task = queryResources(failed.client)
    failed.stream.emit('data', data); failed.stream.emit('close')
    await expect(task.promise).rejects.toThrow('采集失败')
  })
  it('cancels slow requests and late channels, and stops excessive output', async () => {
    vi.useFakeTimers()
    const slow = channelFixture(true), task = queryResources(slow.client)
    const timeout = expect(task.promise).rejects.toThrow('超时')
    await vi.advanceTimersByTimeAsync(10000); await timeout; slow.accept()
    expect(slow.stream.destroy).toHaveBeenCalled()
    const large = channelFixture(), tooLarge = queryResources(large.client)
    large.stream.emit('data', Buffer.alloc(512 * 1024 + 1))
    await expect(tooLarge.promise).rejects.toThrow('512 KiB')
  })
  it('deduplicates queries per session and permits a new request after cancellation', async () => {
    const f = channelFixture(), service = new ResourceService(() => f.client)
    const first = service.status('id'), second = service.status('id')
    const checks = Promise.all([expect(first).rejects.toThrow('取消'), expect(second).rejects.toThrow('取消')])
    expect(f.client.exec).toHaveBeenCalledTimes(1); service.cancel('id'); await checks
    const next = service.status('id'); f.stream.emit('data', Buffer.from(RESOURCE_SAMPLE)); f.stream.emit('exit', 0); f.stream.emit('close')
    expect((await next).cpu.total).toBe(10000); expect(f.client.exec).toHaveBeenCalledTimes(2)
  })
  it('polls only the visible mounted session and ignores late old-session results', async () => {
    vi.useFakeTimers()
    const source = reactive({ id: 'a', identity: 'a', connected: true, interval: 2 })
    let resolve!: (value: unknown) => void
    const api = { resourceStatus: vi.fn().mockReturnValueOnce(new Promise(done => { resolve = done })).mockResolvedValue({ ok: true, value: parseResources(RESOURCE_SAMPLE) }), cancelResourceStatus: vi.fn().mockResolvedValue({ ok: true }) }
    const scope = effectScope(), monitor = scope.run(() => useResources(() => source, api))!
    source.id = 'b'; source.identity = 'b'
    await vi.advanceTimersByTimeAsync(1)
    resolve({ ok: true, value: { ...parseResources(RESOURCE_SAMPLE), uptime: 999999 } })
    await vi.advanceTimersByTimeAsync(1)
    expect(monitor.snapshot.value?.uptime).toBe(3661.9)
    monitor.paused.value = true; const count = api.resourceStatus.mock.calls.length
    await vi.advanceTimersByTimeAsync(6000); expect(api.resourceStatus).toHaveBeenCalledTimes(count)
    monitor.paused.value = false; await vi.advanceTimersByTimeAsync(1)
    scope.stop(); const stopped = api.resourceStatus.mock.calls.length
    await vi.advanceTimersByTimeAsync(6000); expect(api.resourceStatus).toHaveBeenCalledTimes(stopped)
    expect(api.cancelResourceStatus).toHaveBeenCalledWith('a')
  })
  it('uses the connected SSH exec transport without sending commands to the user terminal', async () => {
    const fixture = await createFixture(), events: AppEvent[] = []
    const ssh = new SshService(event => { events.push(event); if (event.type === 'hostkey') void ssh.trust(event.sessionId, true) }, { fingerprint: () => undefined, trust: async () => {} })
    const resources = new ResourceService(id => ssh.clientFor(id))
    try {
      ssh.connect('resource-session', { id: 'resource-profile', name: 'Resources', group: '', host: '127.0.0.1', port: fixture.port, username: 'test', authType: 'password', keyPath: '', initialPath: '', timeout: 10, keepalive: true, rememberCredentials: false }, { password: 'test-password' })
      await vi.waitFor(() => expect(ssh.connected('resource-session')).toBe(true))
      const first = await resources.status('resource-session'), second = await resources.status('resource-session')
      expect(second.cpu.total).toBeGreaterThan(first.cpu.total)
      expect(fixture.resources.commands).toHaveLength(2); expect(fixture.shells.commands).toEqual([])
      expect(fixture.metrics.authentications).toBe(1)
    } finally { resources.dispose(); ssh.dispose(); await fixture.close() }
  })
})
