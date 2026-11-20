import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createWailsApi, type WailsRuntime } from '../src/renderer/src/wails'
import type { Api, AppEvent, Profile, Result } from '../src/shared/types'
import { DEFAULT_SETTINGS } from '../src/shared/settings'

type RuntimeListener = (data: unknown) => void
let channels: Map<string, RuntimeListener>
let call: ReturnType<typeof vi.fn<WailsRuntime['call']>>
let runtime: WailsRuntime
let api: Api
let host: EventTarget

function emit(name: string, data: unknown): void { channels.get(name)?.(data) }
function pendingResult() {
  let resolve!: (result: Result<unknown>) => void
  const promise = new Promise<Result<unknown>>(done => { resolve = done })
  return { promise, resolve }
}
async function flushQueue(): Promise<void> { for (let i = 0; i < 16; i++) await Promise.resolve() }
const profile: Profile = { id: 'profile', name: '测试', group: '', host: '127.0.0.1', port: 22, username: 'test', authType: 'password', keyPath: '', initialPath: '', timeout: 20, keepalive: true, rememberCredentials: false }

beforeEach(() => {
  channels = new Map()
  call = vi.fn<WailsRuntime['call']>().mockResolvedValue({ ok: true, value: null })
  runtime = {
    call,
    on: vi.fn((name: string, listener: RuntimeListener) => { channels.set(name, listener); return () => channels.delete(name) }),
  }
  host = new EventTarget()
  vi.stubGlobal('window', host)
  api = createWailsApi(runtime)
})

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('Wails desktop bridge', () => {
  it('maps the existing API to desktop operations while preserving results and credential values', async () => {
    const credentials = { password: '', passphrase: 'key passphrase' }
    const jump = { password: 'jump-password' }
    const snippet = { id: 'cmd', name: '日志', group: '', profileId: '', command: 'tail -- {{path}}', description: '' }
    const options = { path: '/home/test/app.log', lines: 20 }
    const forward = { name: 'web', localPort: 0, remoteHost: '127.0.0.1', remotePort: 6006 }
    const mappings: { run: () => unknown; operation: string; args: unknown[] }[] = [
      { run: () => api.bootstrap(), operation: 'bootstrap', args: [] },
      { run: () => api.checkUpdate!(), operation: 'updates:check', args: [] },
      { run: () => api.openUpdateRelease!('https://github.com/LisonZhang0623/Meridian-Deck-Official/releases/tag/v0.5.8.1'), operation: 'updates:open', args: ['https://github.com/LisonZhang0623/Meridian-Deck-Official/releases/tag/v0.5.8.1'] },
      { run: () => api.saveProfile(profile, credentials), operation: 'profile:save', args: [profile, credentials] },
      { run: () => api.deleteProfile('profile'), operation: 'profile:delete', args: ['profile'] },
      { run: () => api.saveSettings(DEFAULT_SETTINGS), operation: 'settings:save', args: [DEFAULT_SETTINGS] },
      { run: () => api.selectKey(), operation: 'key:select', args: [] },
      { run: () => api.connect('session', 'profile', credentials, jump), operation: 'ssh:connect', args: ['session', 'profile', credentials, jump] },
      { run: () => api.newTerminal('new', 'source', 'profile', '/home/test'), operation: 'ssh:newTerminal', args: ['new', 'source', 'profile', '/home/test'] },
      { run: () => api.disconnect('session'), operation: 'ssh:disconnect', args: ['session'] },
      { run: () => api.trust('session', false), operation: 'ssh:trust', args: ['session', false] },
      { run: () => api.gpuStatus('session'), operation: 'gpu:status', args: ['session'] },
      { run: () => api.cancelGpuStatus('session'), operation: 'gpu:cancel', args: ['session'] },
      { run: () => api.listCommands(), operation: 'commands:list', args: [] },
      { run: () => api.saveCommand(snippet), operation: 'commands:save', args: [snippet] },
      { run: () => api.deleteCommand('cmd'), operation: 'commands:delete', args: ['cmd'] },
      { run: () => api.renderCommand('cmd', { path: '/a b' }), operation: 'commands:render', args: ['cmd', { path: '/a b' }] },
      { run: () => api.startLog('session', options), operation: 'logs:start', args: ['session', options] },
      { run: () => api.stopLog('session'), operation: 'logs:stop', args: ['session'] },
      { run: () => api.listForwards(), operation: 'forwards:list', args: [] },
      { run: () => api.startForward('session', forward), operation: 'forwards:start', args: ['session', forward] },
      { run: () => api.stopForward('forward'), operation: 'forwards:stop', args: ['forward'] },
      { run: () => api.dismissForward!('forward'), operation: 'forwards:dismiss', args: ['forward'] },
      { run: () => api.openForward('forward'), operation: 'forwards:open', args: ['forward'] },
      { run: () => api.resourceStatus('session'), operation: 'resources:status', args: ['session'] },
      { run: () => api.cancelResourceStatus('session'), operation: 'resources:cancel', args: ['session'] },
      { run: () => api.listDirectory('session', '/home/test'), operation: 'files:list', args: ['session', '/home/test'] },
      { run: () => api.preview('session', '/home/test/a.txt'), operation: 'files:preview', args: ['session', '/home/test/a.txt'] },
      { run: () => api.saveText('session', '/a', 'new text', 'old-hash'), operation: 'files:save', args: ['session', '/a', 'new text', 'old-hash'] },
      { run: () => api.createEntry('session', '/home/test', '新目录', true), operation: 'files:create', args: ['session', '/home/test', '新目录', true] },
      { run: () => api.renameEntry('session', '/home/test/a', 'b'), operation: 'files:rename', args: ['session', '/home/test/a', 'b'] },
      { run: () => api.deleteEntry('session', '/home/test/a', false), operation: 'files:delete', args: ['session', '/home/test/a', false] },
      { run: () => api.upload('session', '/home/test', ['native-token']), operation: 'transfer:upload', args: ['session', '/home/test', ['native-token']] },
      { run: () => api.uploadDropped!('session', '/home/test', ['native-token']), operation: 'transfer:dropUpload', args: ['session', '/home/test', ['native-token']] },
      { run: () => api.nasUpload!('session', '/home/test', 'nas-root', ['folder']), operation: 'nas:upload', args: ['session', '/home/test', 'nas-root', ['folder']] },
      { run: () => api.nasDownload!('session', ['/home/test/folder'], 'nas-root', ''), operation: 'nas:download', args: ['session', ['/home/test/folder'], 'nas-root', ''] },
      { run: () => api.download('session', '/home/test/a'), operation: 'transfer:download', args: ['session', '/home/test/a'] },
      { run: () => api.prepareDragDownload('session', '/home/test/a'), operation: 'drag:prepare', args: ['session', '/home/test/a'] },
      { run: () => api.cancelTransfer('transfer'), operation: 'transfer:cancel', args: ['transfer'] },
      { run: () => api.retryTransfer('transfer'), operation: 'transfer:retry', args: ['transfer'] },
    ]
    const result: Result<unknown> = { ok: false, error: 'remote changed', code: 'CONFLICT' }
    call.mockResolvedValue(result)
    for (const mapping of mappings) {
      call.mockClear()
      expect(await mapping.run()).toBe(result)
      expect(call).toHaveBeenCalledExactlyOnceWith(mapping.operation, mapping.args)
    }
  })

  it('exposes native folder export only when the Wails platform supports it', async () => {
    const windowsApi = createWailsApi(runtime, true)
    expect(windowsApi.nativeDragDownload).toBe(true)
    await windowsApi.prepareDragDirectory!('session', '/home/test/folder')
    expect(call).toHaveBeenLastCalledWith('drag:prepareDirectory', ['session', '/home/test/folder'])
    const otherApi = createWailsApi(runtime, false)
    expect(otherApi.nativeDragDownload).toBe(false)
    expect(otherApi.prepareDragDirectory).toBeUndefined()
    expect(otherApi.downloadDirectory).toBeDefined()
  })

  it('normalizes missing optional arguments to null and supplies an empty jump-credential object', async () => {
    await api.connect('session', 'profile', {})
    expect(call).toHaveBeenLastCalledWith('ssh:connect', ['session', 'profile', {}, {}])
    await api.newTerminal('new', 'source', 'profile')
    expect(call).toHaveBeenLastCalledWith('ssh:newTerminal', ['new', 'source', 'profile', null])
    await api.upload('session', '/home/test')
    expect(call).toHaveBeenLastCalledWith('transfer:upload', ['session', '/home/test', null])
    await api.upload('session', '/home/test', [])
    expect(call).toHaveBeenLastCalledWith('transfer:upload', ['session', '/home/test', []])
  })

  it('passes the expected saved connection identity for every fresh SSH connection', async () => {
    const identity = '["profile","127.0.0.1",22,"test","password",""]'
    const hops = { jump: { password: 'test-hop' } }
    await api.connect('session', 'profile', {}, undefined, identity)
    expect(call).toHaveBeenLastCalledWith('ssh:connect', ['session', 'profile', {}, {}, identity])
    await api.connectChain!('session', 'profile', {}, hops, identity)
    expect(call).toHaveBeenLastCalledWith('ssh:connectChain', ['session', 'profile', {}, hops, identity])
    await api.connectTmux!('session', 'profile', {}, hops, 'work', identity)
    expect(call).toHaveBeenLastCalledWith('ssh:connectTmux', ['session', 'profile', {}, hops, 'work', identity])
  })

  it('dispatches only string-array native drop payloads and never reads File.path', () => {
    expect(api.nativeFileDrop).toBe(true)
    const drops: unknown[] = []
    host.addEventListener('sshtool:files-dropped', event => drops.push((event as CustomEvent).detail))
    const tokens = ['opaque-native-token', 'another-token']
    emit('files-dropped', tokens)
    expect(drops).toEqual([tokens])
    for (const invalid of [null, undefined, 'token', { tokens }, [{ path: 'C:\\private.txt' }], ['token', 12], ['token', null]]) emit('files-dropped', invalid)
    expect(drops).toEqual([tokens])
    const file = Object.defineProperty({}, 'path', { get: () => { throw new Error('must not inspect a renderer file path') } }) as File
    expect(api.filePath(file)).toBe('')
  })

  it('subscribes once to runtime channels and lets consumers unsubscribe independently', () => {
    expect(runtime.on).toHaveBeenCalledTimes(2)
    expect(channels.has('app:event')).toBe(true)
    expect(channels.has('files-dropped')).toBe(true)
    const first = vi.fn(), second = vi.fn()
    const off = api.onEvent(first)
    api.onEvent(second)
    const event: AppEvent = { type: 'state', sessionId: 'session', phase: 'connected', message: '已连接' }
    emit('app:event', event)
    expect(first).toHaveBeenCalledWith(event)
    expect(second).toHaveBeenCalledWith(event)
    off(); off()
    emit('app:event', event)
    expect(first).toHaveBeenCalledTimes(1)
    expect(second).toHaveBeenCalledTimes(2)
    for (const invalid of [undefined, null, 'notice', 1, {}]) emit('app:event', invalid)
    expect(second).toHaveBeenCalledTimes(2)
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    api.onEvent(() => { throw new Error('consumer failure') })
    const afterFailure = vi.fn()
    api.onEvent(afterFailure)
    expect(() => emit('app:event', event)).not.toThrow()
    expect(afterFailure).toHaveBeenCalledWith(event)
    expect(consoleError).toHaveBeenCalledTimes(1)
  })

  it('reports user-action failures without interrupting the user for background calls', async () => {
    const listener = vi.fn()
    api.onEvent(listener)
    api.terminalInput('session', 'echo hello\r')
    await vi.waitFor(() => expect(call).toHaveBeenLastCalledWith('terminal:input', ['session', 'echo hello\r']))
    api.terminalResize('session', 120, 40)
    expect(call).toHaveBeenLastCalledWith('terminal:resize', ['session', 120, 40])
    api.workspaceDirty?.(true)
    await vi.waitFor(() => expect(call).toHaveBeenLastCalledWith('window:dirty', [true]))
    call.mockResolvedValueOnce({ ok: false, error: 'connection ended' })
    api.terminalInput('session', 'a')
    await vi.waitFor(() => expect(listener).toHaveBeenCalledWith({ type: 'notice', message: 'connection ended' }))
    const diagnostics = vi.spyOn(console, 'error').mockImplementation(() => {})
    const raw = new Error('Wails: sshtool/internal/desktop.Service.Call')
    call.mockRejectedValueOnce(raw)
    api.terminalResize('session', 120, 40)
    await flushQueue()
    expect(diagnostics).toHaveBeenCalledWith('桌面操作失败', 'terminal:resize', raw)
    expect(listener).toHaveBeenCalledTimes(1)

    call.mockRejectedValueOnce(raw)
    await expect(api.bootstrap()).resolves.toEqual({
      ok: false, error: '暂时无法连接桌面服务，请重试。', code: 'DESKTOP_UNAVAILABLE'
    })
    call.mockRejectedValueOnce(raw)
    api.terminalInput('session', 'b')
    await vi.waitFor(() => expect(listener).toHaveBeenCalledWith({
      type: 'notice', message: '暂时无法连接桌面服务，请重试。'
    }))
  })

  it('serializes input within a terminal while allowing another terminal to progress', async () => {
    const first = pendingResult()
    call.mockImplementation((operation, args) => operation === 'terminal:input' && args[0] === 'one' && args[1] === 'first' ? first.promise : Promise.resolve({ ok: true, value: null }))
    api.terminalInput('one', 'first')
    api.terminalInput('one', 'second')
    api.terminalInput('two', 'independent')
    await flushQueue()
    expect(call.mock.calls.filter(([operation]) => operation === 'terminal:input')).toEqual([
      ['terminal:input', ['one', 'first']], ['terminal:input', ['two', 'independent']],
    ])
    first.resolve({ ok: true, value: null })
    await vi.waitFor(() => expect(call).toHaveBeenCalledWith('terminal:input', ['one', 'second']))
    expect(call.mock.calls.filter(([operation]) => operation === 'terminal:input').map(([, args]) => args)).toEqual([
      ['one', 'first'], ['two', 'independent'], ['one', 'second'],
    ])
  })

  it('drops queued input after a disconnected state without blocking a new connection', async () => {
    const old = pendingResult()
    call.mockImplementation((operation, args) => operation === 'terminal:input' && args[1] === 'in flight' ? old.promise : Promise.resolve({ ok: true, value: null }))
    api.terminalInput('old-session', 'in flight')
    api.terminalInput('old-session', 'must not reach server')
    await flushQueue()
    expect(call).toHaveBeenCalledExactlyOnceWith('terminal:input', ['old-session', 'in flight'])
    emit('app:event', { type: 'state', sessionId: 'old-session', phase: 'failed', message: 'lost connection' })
    api.terminalInput('new-session', 'new connection')
    await flushQueue()
    expect(call).toHaveBeenCalledWith('terminal:input', ['new-session', 'new connection'])
    old.resolve({ ok: true, value: null })
    await flushQueue()
    expect(call).not.toHaveBeenCalledWith('terminal:input', ['old-session', 'must not reach server'])
  })

  it('bounds pending UTF-8 input to one MiB and permits new input after the queue drains', async () => {
    const first = pendingResult(), listener = vi.fn()
    api.onEvent(listener)
    call.mockImplementation((operation, args) => operation === 'terminal:input' && typeof args[1] === 'string' && args[1].length > 100 ? first.promise : Promise.resolve({ ok: true, value: null }))
    const nearLimit = '中'.repeat(349_525) // 1,048,575 UTF-8 bytes.
    api.terminalInput('session', nearLimit)
    api.terminalInput('session', 'a') // Exact 1 MiB boundary remains valid.
    api.terminalInput('session', 'overflow')
    await flushQueue()
    expect(call.mock.calls.filter(([operation]) => operation === 'terminal:input').map(([, args]) => (args[1] as string).length)).toEqual([nearLimit.length])
    expect(listener).toHaveBeenCalledWith({ type: 'notice', message: expect.stringContaining('缓冲已满') })
    first.resolve({ ok: true, value: null })
    await flushQueue()
    expect(call).toHaveBeenCalledWith('terminal:input', ['session', 'a'])
    expect(call).not.toHaveBeenCalledWith('terminal:input', ['session', 'overflow'])
    api.terminalInput('session', 'after draining')
    await flushQueue()
    expect(call).toHaveBeenCalledWith('terminal:input', ['session', 'after draining'])
  })

  it('serializes dirty-state updates so an older clean result cannot overwrite a newer edit', async () => {
    const clean = pendingResult(), applied: boolean[] = []
    call.mockImplementation((operation, args) => {
      if (operation !== 'window:dirty') return Promise.resolve({ ok: true, value: null })
      if (args[0] === false) return clean.promise.then(result => { applied.push(false); return result })
      applied.push(true)
      return Promise.resolve({ ok: true, value: null })
    })
    api.workspaceDirty?.(false)
    await flushQueue()
    api.workspaceDirty?.(true)
    await flushQueue()
    expect(call).toHaveBeenCalledExactlyOnceWith('window:dirty', [false])
    expect(applied).toEqual([])
    clean.resolve({ ok: true, value: null })
    await flushQueue()
    expect(call.mock.calls).toEqual([['window:dirty', [false]], ['window:dirty', [true]]])
    expect(applied).toEqual([false, true])
  })

  it.each(['backend error', 'transport rejection'] as const)('releases the matching native drag marker after %s', async failure => {
    const listener = vi.fn(), failed = vi.fn()
    api.onEvent(listener)
    host.addEventListener('sshtool:drag-out-failed', failed)
    if (failure === 'backend error') call.mockResolvedValueOnce({ ok: false, error: 'drag unavailable' })
    else {
      vi.spyOn(console, 'error').mockImplementation(() => {})
      call.mockRejectedValueOnce(new Error('Wails: drag unavailable'))
    }
    api.startDragDownload('source-session', 'prepared-token')
    expect(call).toHaveBeenCalledExactlyOnceWith('drag:start', ['source-session', 'prepared-token'])
    await vi.waitFor(() => expect(failed).toHaveBeenCalledTimes(1))
    expect((failed.mock.calls[0][0] as CustomEvent).detail).toEqual({ sessionId: 'source-session', token: 'prepared-token' })
    expect(listener).toHaveBeenCalledWith({ type: 'notice', message: failure === 'backend error' ? 'drag unavailable' : '暂时无法连接桌面服务，请重试。', dragToken: 'prepared-token' })
  })

  it('publishes unmounted terminal frames before automatically acknowledging them', () => {
    const trace: string[] = []
    api.onEvent(event => { if (event.type === 'terminal') trace.push('retained') })
    call.mockImplementation(async (operation, args) => { if (operation === 'terminal:ack') trace.push(`ack:${args[1]}`); return { ok: true, value: null } })
    const event: AppEvent = { type: 'terminal', sessionId: 'unmounted', data: '5Lit5paH', sequence: 'frame-7' }
    emit('app:event', event)
    expect(trace).toEqual(['retained', 'ack:frame-7'])
    expect(call).toHaveBeenCalledExactlyOnceWith('terminal:ack', ['unmounted', 'frame-7'])
    call.mockClear()
    emit('app:event', { type: 'terminal', sessionId: 'legacy', data: 'YQ==' })
    expect(call).not.toHaveBeenCalled()
  })

  it('waits for a mounted terminal consumer to acknowledge parsed frames and resumes auto-ack after unmount', () => {
    const frame: AppEvent = { type: 'terminal', sessionId: 'mounted', data: 'YQ==', sequence: 'frame-3' }
    const listener = vi.fn()
    api.onEvent(listener)
    api.terminalConsumer?.('mounted', true)
    emit('app:event', frame)
    expect(listener).toHaveBeenCalledWith(frame)
    expect(call).not.toHaveBeenCalled()
    api.ackTerminal?.('mounted', 'frame-3')
    expect(call).toHaveBeenCalledExactlyOnceWith('terminal:ack', ['mounted', 'frame-3'])
    call.mockClear()
    emit('app:event', { ...frame, sessionId: 'not-mounted', sequence: 'frame-4' })
    expect(call).toHaveBeenCalledExactlyOnceWith('terminal:ack', ['not-mounted', 'frame-4'])
    call.mockClear()
    api.terminalConsumer?.('mounted', false)
    emit('app:event', { ...frame, sequence: 'frame-5' })
    expect(call).toHaveBeenCalledExactlyOnceWith('terminal:ack', ['mounted', 'frame-5'])
  })
})
