import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { state, type Workspace } from '../src/renderer/src/workbench'
import { useOsc7Setup } from '../src/renderer/src/osc7-setup'

let effects: ReturnType<typeof effectScope>
const settings = ref(false)
const check = vi.fn(), configure = vi.fn(), prepare = vi.fn(), input = vi.fn()
function session(id: string): Workspace {
  return { id, profileId: id, connectionKey: id, endpoint: id + '@localhost:22', name: id, phase: 'connected',
    message: '', home: '/', directory: '/', terminalNumber: 1, activeDocument: 'terminal', documents: [],
    tree: {}, expanded: new Set(), loading: new Set(), terminalBuffer: [], terminalBytes: 0 }
}
beforeEach(() => {
  vi.useFakeTimers()
  effects = effectScope()
  settings.value = false
  const prefs = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (k: string) => prefs.get(k), setItem: (k: string, v: string) => prefs.set(k, v) })
  vi.stubGlobal('window', { setTimeout, clearTimeout, sshTool: { checkOsc7: check, configureOsc7: configure, prepareOsc7: prepare, terminalInput: input } })
  check.mockReset().mockResolvedValue({ ok: true, value: { shell: 'bash', configured: false, path: '/home/demo/.bashrc' } })
  configure.mockReset().mockResolvedValue({ ok: true, value: { shell: 'bash', configured: true, path: '/home/demo/.bashrc' } })
  prepare.mockReset().mockImplementation(configure)
  input.mockReset()
  state.sessions = [session('first')]
  state.activeId = 'first'
})
afterEach(() => { effects.stop(); vi.clearAllTimers(); vi.useRealTimers(); vi.unstubAllGlobals() })

it('only reads on connection, remembers dismissal, and configures explicitly in settings', async () => {
  const ui = effects.run(() => useOsc7Setup(settings))!
  await vi.advanceTimersByTimeAsync(2600)
  expect(ui.offer.value).toBe(true)
  expect(configure).not.toHaveBeenCalled()
  ui.dismiss()
  expect(ui.offer.value).toBe(false)
  settings.value = true
  await nextTick()
  await vi.advanceTimersByTimeAsync(1)
  expect(ui.canConfigure.value).toBe(true)
  await Promise.all([ui.configure(false), ui.configure(false)])
  expect(configure.mock.calls).toEqual([['first', '/home/demo/.bashrc']])
  expect(ui.status.value).toContain('启动配置已保存')
  expect(input).not.toHaveBeenCalled()
})

it('enables a saved or new hook in the original shell and waits for a live report', async () => {
  state.sessions[0].terminalAtPrompt = true
  const ui = effects.run(() => useOsc7Setup(settings))!
  await vi.advanceTimersByTimeAsync(2600)
  await ui.configure()
  expect(input.mock.calls).toEqual([['first', ' builtin source "$HOME/.meridian-osc7-v1.sh"\r']])
  expect(ui.status.value).toContain('等待当前终端')
  state.sessions[0].terminalDirectory = '/lab'
  expect(ui.status.value).toContain('已在报告目录')
})

it.each(['alternate', 'typing', 'switch', 'custom'] as const)('avoids terminal input during %s', async reason => {
  const session = state.sessions[0]
  session.terminalAtPrompt = reason !== 'custom'
  let finish!: (value: unknown) => void
  prepare.mockImplementationOnce(() => new Promise(done => { finish = done }))
  const ui = effects.run(() => useOsc7Setup(settings))!
  await vi.advanceTimersByTimeAsync(2600)
  const busy = ui.configure()
  if (reason === 'alternate') session.terminalAlternate = true
  if (reason === 'typing') { session.terminalInputRevision = 1; session.terminalInputPending = true }
  if (reason === 'switch') state.activeId = 'other'
  finish({ ok: true, value: { shell: 'zsh', path: '/custom/.zshrc', configured: true } })
  await busy
  expect(input).not.toHaveBeenCalled()
  if (reason !== 'switch') expect(ui.manualCommand.value).toContain('ZDOTDIR')
})

it('enables a configuration saved by an earlier build', async () => {
  check.mockResolvedValue({ ok: true, value: { shell: 'bash', configured: true, path: '/home/demo/.bashrc' } })
  state.sessions[0].terminalAtPrompt = true
  const ui = effects.run(() => useOsc7Setup(settings))!
  await vi.advanceTimersByTimeAsync(2600)
  expect(ui.canConfigure.value).toBe(false)
  expect(ui.canEnable.value).toBe(true)
  await ui.configure()
  expect(input).toHaveBeenCalledTimes(1)
})

it('uses live OSC 7 and ignores the previous server’s outstanding result', async () => {
  let resolve!: (result: unknown) => void
  check.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const ui = effects.run(() => useOsc7Setup(settings))!
  await vi.advanceTimersByTimeAsync(2600)
  state.sessions.push({ ...session('second'), terminalDirectory: '/work' })
  state.activeId = 'second'
  await nextTick()
  resolve({ ok: true, value: { shell: 'bash', configured: false, path: '/old' } })
  await vi.advanceTimersByTimeAsync(3000)
  expect(ui.status.value).toContain('已在报告目录')
  expect(ui.offer.value).toBe(false)
  expect(check).toHaveBeenCalledTimes(1)
  await ui.configure()
  expect(configure).not.toHaveBeenCalled()
})
