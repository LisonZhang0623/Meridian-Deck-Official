import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, reactive, type EffectScope } from 'vue'
import { useLogMonitor, type LogSource } from '../src/renderer/src/useLogMonitor'
import type { AppEvent, Result } from '../src/shared/types'

let scope: EffectScope
function setup() {
  const source = reactive<LogSource>({ id: 'one', identity: 'server-one', connected: true })
  let callback!: (event: AppEvent) => void
  const unsubscribe = vi.fn()
  const api = {
    startLog: vi.fn(async (): Promise<Result<string>> => ({ ok: true, value: 'watch-one' })),
    stopLog: vi.fn(async () => ({ ok: true as const, value: undefined })),
    onEvent: vi.fn((listener: (event: AppEvent) => void) => { callback = listener; return unsubscribe })
  }
  scope = effectScope()
  const monitor = scope.run(() => useLogMonitor(() => source, api))!
  const send = (data: string, watchId = 'watch-one', sessionId = 'one', kind: 'data' | 'error' | 'end' = 'data') => callback({ type: 'log', sessionId, watchId, kind, data })
  return { monitor, api, source, send, unsubscribe }
}
afterEach(() => scope?.stop())

describe('log panel lifecycle', () => {
  it('does not execute until requested and preserves partial lines across bounded batches', async () => {
    const f = setup(); expect(f.api.startLog).not.toHaveBeenCalled()
    await f.monitor.start('/app.log', 200)
    f.send('line one\npart'); f.send('ial two\n')
    expect(f.monitor.rows.value).toEqual(['line one', 'partial two']); expect(f.monitor.partial.value).toBe('')
    f.send('<script>bad()</script>')
    expect(f.monitor.partial.value).toBe('<script>bad()</script>')
    f.monitor.stop(); expect(f.api.stopLog).toHaveBeenCalledWith('one')
    expect(f.monitor.rows.value).toHaveLength(2)
  })
  it('isolates old watches and servers, cancels on disconnect, and unsubscribes on unmount', async () => {
    const f = setup(); await f.monitor.start('/first.log', 20); f.send('one\n')
    f.source.id = 'two'; f.source.identity = 'server-two'
    expect(f.api.stopLog).toHaveBeenCalledWith('one'); expect(f.monitor.rows.value).toEqual([])
    f.api.startLog.mockResolvedValue({ ok: true, value: 'watch-two' }); await f.monitor.start('/second.log', 0)
    f.send('stale\n'); f.send('wrong-watch\n', 'watch-one', 'two'); f.send('current\n', 'watch-two', 'two')
    expect(f.monitor.rows.value).toEqual(['current'])
    f.source.connected = false; expect(f.api.stopLog).toHaveBeenCalledWith('two'); expect(f.monitor.running.value).toBe(false)
    scope.stop(); expect(f.unsubscribe).toHaveBeenCalledOnce()
  })
  it('waits for the previous stop before switching to another file on the same server', async () => {
    const f = setup()
    await f.monitor.start('/first.log', 20)
    let finishStop!: (result: { ok: true; value: undefined }) => void
    f.api.stopLog.mockImplementationOnce(() => new Promise(resolve => { finishStop = resolve }))
    const change = f.monitor.start('/second.log', 10)
    expect(f.api.startLog).toHaveBeenCalledTimes(1)
    finishStop({ ok: true, value: undefined })
    await change
    expect(f.api.startLog).toHaveBeenLastCalledWith('one', { path: '/second.log', lines: 10 })
  })
  it('ignores late start results after a source change and handles events arriving before the start response', async () => {
    const f = setup()
    let resolve!: (value: Result<string>) => void
    f.api.startLog.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const opening = f.monitor.start('/app.log', 200)
    f.send('early\n')
    resolve({ ok: true, value: 'watch-one' }); await opening
    expect(f.monitor.rows.value).toEqual(['early'])
    f.api.startLog.mockReturnValueOnce(new Promise(done => { resolve = done }))
    const late = f.monitor.start('/other.log', 200)
    f.source.id = 'two'
    resolve({ ok: true, value: 'late-watch' }); await late
    expect(f.monitor.running.value).toBe(false); expect(f.monitor.rows.value).toEqual([])
  })
  it('caps retained lines/text and retains the error until an explicit retry', async () => {
    const f = setup(); await f.monitor.start('/app.log', 0)
    for (let block = 0; block < 10; block++) f.send(Array.from({ length: 300 }, (_, n) => `${block}:${n}`).join('\n') + '\n')
    expect(f.monitor.rows.value).toHaveLength(2000); expect(f.monitor.dropped.value).toBe(true)
    f.send('Permission denied', 'watch-one', 'one', 'error')
    expect(f.monitor.running.value).toBe(false); expect(f.monitor.error.value).toBe('Permission denied')
    f.send('ignored\n'); expect(f.monitor.rows.value).toHaveLength(2000)
    f.api.startLog.mockResolvedValueOnce({ ok: false, error: 'cannot open' }); await f.monitor.start('/bad.log', 0)
    expect(f.monitor.error.value).toBe('cannot open')
    await f.monitor.start('/utf8.log', 0)
    for (let block = 0; block < 20; block++) f.send(('中'.repeat(1000) + '\n').repeat(25))
    expect(new TextEncoder().encode(f.monitor.rows.value.join('\n')).byteLength).toBeLessThanOrEqual(1024 * 1024)
    expect(f.monitor.rows.value.length).toBeLessThan(500); expect(f.monitor.dropped.value).toBe(true)
  })
})
