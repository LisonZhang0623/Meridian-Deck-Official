import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { state, type Workspace } from '../src/renderer/src/workbench'
import { useOsc7Setup } from '../src/renderer/src/osc7-setup'

let effects: ReturnType<typeof effectScope>
const settings = ref(false)
const check = vi.fn(), configure = vi.fn()
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
  vi.stubGlobal('window', { setTimeout, clearTimeout, sshTool: { checkOsc7: check, configureOsc7: configure } })
  check.mockReset().mockResolvedValue({ ok: true, value: { shell: 'bash', configured: false, path: '/home/demo/.bashrc' } })
  configure.mockReset().mockResolvedValue({ ok: true, value: { shell: 'bash', configured: true, path: '/home/demo/.bashrc' } })
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
  await Promise.all([ui.configure(), ui.configure()])
  expect(configure.mock.calls).toEqual([['first', '/home/demo/.bashrc']])
  expect(ui.status.value).toContain('新开')
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
