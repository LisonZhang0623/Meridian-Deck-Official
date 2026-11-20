import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventEmitter } from 'node:events'
import type { Client, ClientChannel } from 'ssh2'
import type { LogEvent } from '../src/shared/v2'
import { LogService, logCommand } from '../src/main/logs'

function fixture(delayed = false) {
  const channels: ReturnType<typeof channel>[] = [], callbacks: ((error: Error | null, stream: ClientChannel) => void)[] = []
  function channel() { return Object.assign(new EventEmitter(), { stderr: new EventEmitter(), signal: vi.fn(), close: vi.fn(), destroy: vi.fn() }) }
  let connected = true, identity = 'one'
  const client = { shell: vi.fn(), exec: vi.fn((_command: string, callback) => {
    const stream = channel(); channels.push(stream); callbacks.push(callback)
    if (!delayed) callback(null, stream)
  }) } as unknown as Client
  const events: LogEvent[] = []
  const service = new LogService(() => client, () => { if (!connected) throw new Error('disconnected'); return identity }, event => events.push(event))
  return { service, client, channels, events, accept(index: number) { callbacks[index](null, channels[index] as unknown as ClientChannel) }, disconnect() { connected = false }, changeIdentity() { identity = 'two' } }
}
afterEach(() => vi.useRealTimers())

describe('bounded remote log tracking', () => {
  it('quotes a literal absolute path and validates the initial line count', () => {
    const command = logCommand({ path: "/tmp/it's $(touch nope).log", lines: 0 })
    expect(command).toContain("exec tail -n 0 -F -- '/tmp/it'\\''s $(touch nope).log'")
    expect(command).toContain('[ ! -f ')
    for (const path of ['', '-', 'relative.log', '/tmp/new\nline']) expect(() => logCommand({ path, lines: 200 })).toThrow()
    for (const lines of [-1, 2001, 1.5, NaN]) expect(() => logCommand({ path: '/valid.log', lines })).toThrow('行数')
  })
  it('batches split UTF-8 output without sending anything to the interactive terminal', async () => {
    vi.useFakeTimers()
    const f = fixture(), id = await f.service.start('session', { path: '/app.log', lines: 200 }), stream = f.channels[0]
    const data = Buffer.from('start\n训练中\n'), index = data.indexOf(Buffer.from('训')) + 1
    stream.emit('data', data.subarray(0, index)); stream.emit('data', data.subarray(index))
    expect(f.events).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(100)
    expect(f.events).toEqual([{ type: 'log', sessionId: 'session', watchId: id, kind: 'data', data: 'start\n训练中\n' }])
    expect((f.client as unknown as { shell: ReturnType<typeof vi.fn> }).shell).not.toHaveBeenCalled()
    f.service.stop('session'); expect(stream.signal).toHaveBeenCalledWith('TERM'); expect(stream.destroy).toHaveBeenCalled()
    const count = f.events.length; stream.emit('data', Buffer.from('late')); await vi.advanceTimersByTimeAsync(1000)
    expect(f.events).toHaveLength(count)
  })
  it('closes channels accepted after cancellation or startup timeout', async () => {
    vi.useFakeTimers()
    for (const timeout of [false, true]) {
      const f = fixture(true), pending = f.service.start('session', { path: '/app.log', lines: 200 })
      const failure = expect(pending).rejects.toThrow(timeout ? '超时' : '已停止')
      if (timeout) await vi.advanceTimersByTimeAsync(10000); else f.service.stop('session')
      await failure; f.accept(0)
      expect(f.channels[0].close).toHaveBeenCalled(); expect(f.channels[0].destroy).toHaveBeenCalled()
      expect(f.events.at(-1)?.kind).toBe(timeout ? 'error' : 'end')
    }
  })
  it('replaces only the same session watch and rejects late output after identity changes', async () => {
    vi.useFakeTimers()
    const f = fixture()
    const first = await f.service.start('session', { path: '/first.log', lines: 200 })
    await f.service.start('other', { path: '/other.log', lines: 200 })
    const second = await f.service.start('session', { path: '/second.log', lines: 200 })
    expect(first).not.toBe(second); expect(f.channels[0].destroy).toHaveBeenCalled(); expect(f.channels[1].destroy).not.toHaveBeenCalled()
    f.channels[0].emit('data', Buffer.from('old message\n')); f.changeIdentity(); f.channels[2].emit('data', Buffer.from('new identity\n'))
    await vi.advanceTimersByTimeAsync(100)
    expect(f.events.some(event => event.kind === 'data')).toBe(false)
    expect(f.events.at(-1)?.data).toContain('连接已变化')
    f.service.dispose(); expect(f.channels[1].destroy).toHaveBeenCalled()
  })
  it('limits long lines and stops output flooding before an unbounded queue builds', async () => {
    vi.useFakeTimers()
    const f = fixture(); await f.service.start('session', { path: '/app.log', lines: 0 })
    f.channels[0].emit('data', Buffer.from('x'.repeat(30_000) + '\nnormal\n'))
    await vi.advanceTimersByTimeAsync(100)
    const text = f.events.filter(event => event.kind === 'data').map(event => event.data).join('')
    expect(text.length).toBeLessThan(17_000); expect(text).toContain('已截断'); expect(text).toContain('normal\n')
    f.channels[0].emit('data', Buffer.alloc(512 * 1024 + 1, 97))
    expect(f.events.at(-1)?.kind).toBe('error'); expect(f.events.at(-1)?.data).toContain('512 KiB')
    expect(f.channels[0].destroy).toHaveBeenCalled()
  })
  it('reports missing tail/permission failures and preserves rotation diagnostics without stopping a live stream', async () => {
    vi.useFakeTimers()
    const f = fixture(); await f.service.start('session', { path: '/app.log', lines: 20 })
    f.channels[0].stderr.emit('data', Buffer.from('file replaced; following new file\n'))
    await vi.advanceTimersByTimeAsync(100)
    expect(f.events.at(-1)?.kind).toBe('data'); expect(f.channels[0].destroy).not.toHaveBeenCalled()
    f.channels[0].stderr.emit('data', Buffer.from('tail: command not found\n')); f.channels[0].emit('exit', 127); f.channels[0].emit('close')
    expect(f.events.at(-1)?.data).toContain('未找到 tail'); expect(f.events.at(-1)?.data).toContain('command not found')
    const broken = fixture(); await broken.service.start('session', { path: '/private.log', lines: 20 })
    broken.channels[0].stderr.emit('data', Buffer.from('日志文件不存在或不可读\n')); broken.channels[0].emit('exit', 1); broken.channels[0].emit('close')
    expect(broken.events.at(-1)?.kind).toBe('error'); expect(broken.events.at(-1)?.data).toContain('不可读')
  })
})
