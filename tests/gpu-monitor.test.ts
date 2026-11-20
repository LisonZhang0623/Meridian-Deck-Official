import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, reactive, type EffectScope } from 'vue'
import { useGpuMonitor, type GpuSource } from '../src/renderer/src/useGpuMonitor'
import type { GpuSnapshot, Result } from '../src/shared/types'

let scope: EffectScope
const snapshot = (name: string): Result<GpuSnapshot> => ({ ok: true, value: { devices: [{ index: 0, uuid: 'GPU-test', name, utilization: 0, memoryUsedMiB: 0, memoryTotalMiB: 1024, temperatureC: 40, powerDrawW: 10, powerLimitW: 100, fanPercent: null, performanceState: 'P8' }], collectedAt: 1000 } })
function setup() {
  const source = reactive<GpuSource>({ id: 'one', identity: 'server-one', connected: true, visible: true, interval: 2 })
  const api = { gpuStatus: vi.fn(async (_id: string): Promise<Result<GpuSnapshot>> => snapshot('GPU one')), cancelGpuStatus: vi.fn(async () => ({ ok: true as const, value: undefined })) }
  scope = effectScope()
  const monitor = scope.run(() => useGpuMonitor(() => source, api))!
  return { source, api, monitor }
}
beforeEach(() => vi.useFakeTimers())
afterEach(() => { scope?.stop(); vi.useRealTimers() })
describe('GPU panel refresh lifecycle', () => {
  it('refreshes at the selected interval and stops while hidden or paused', async () => {
    const { source, api, monitor } = setup()
    await vi.advanceTimersByTimeAsync(0)
    expect(monitor.devices.value[0].name).toBe('GPU one')
    await vi.advanceTimersByTimeAsync(2000); expect(api.gpuStatus).toHaveBeenCalledTimes(2)
    source.visible = false
    await vi.advanceTimersByTimeAsync(10000); expect(api.gpuStatus).toHaveBeenCalledTimes(2)
    source.visible = true
    await vi.advanceTimersByTimeAsync(0); expect(api.gpuStatus).toHaveBeenCalledTimes(3)
    monitor.paused.value = true
    await vi.advanceTimersByTimeAsync(10000); expect(api.gpuStatus).toHaveBeenCalledTimes(3)
    await monitor.refresh(); expect(api.gpuStatus).toHaveBeenCalledTimes(4)
    expect(monitor.paused.value).toBe(true)
    source.interval = 5; monitor.paused.value = false
    await vi.advanceTimersByTimeAsync(4999); expect(api.gpuStatus).toHaveBeenCalledTimes(5)
    await vi.advanceTimersByTimeAsync(1); expect(api.gpuStatus).toHaveBeenCalledTimes(6)
  })
  it('never overlaps reads and ignores late responses from the previous server', async () => {
    const { source, api, monitor } = setup()
    await vi.advanceTimersByTimeAsync(0)
    let resolveOld!: (value: Result<GpuSnapshot>) => void
    api.gpuStatus.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const pending = monitor.refresh()
    await vi.advanceTimersByTimeAsync(10000); await monitor.refresh()
    expect(api.gpuStatus).toHaveBeenCalledTimes(2)
    api.gpuStatus.mockResolvedValue(snapshot('GPU two'))
    source.id = 'two'
    await vi.advanceTimersByTimeAsync(0)
    expect(api.cancelGpuStatus).toHaveBeenCalledWith('one')
    resolveOld(snapshot('stale GPU one')); await pending
    expect(monitor.devices.value[0].name).toBe('GPU two')
  })
  it('pauses on failure, retries explicitly, and cancels on disconnect and disposal', async () => {
    const { source, api, monitor } = setup()
    await vi.advanceTimersByTimeAsync(0)
    api.gpuStatus.mockResolvedValueOnce({ ok: false, error: 'No driver' })
    await monitor.refresh()
    expect(monitor.paused.value).toBe(true); expect(monitor.error.value).toBe('No driver')
    await vi.advanceTimersByTimeAsync(10000); expect(api.gpuStatus).toHaveBeenCalledTimes(2)
    monitor.paused.value = false
    await vi.advanceTimersByTimeAsync(0); expect(monitor.error.value).toBe('')
    api.gpuStatus.mockImplementation(() => new Promise(() => {}))
    void monitor.refresh(); source.connected = false
    expect(api.cancelGpuStatus).toHaveBeenCalledWith('one')
    const count = api.gpuStatus.mock.calls.length
    await vi.advanceTimersByTimeAsync(10000); expect(api.gpuStatus).toHaveBeenCalledTimes(count)
    source.connected = true
    scope.stop()
    expect(monitor.busy.value).toBe(false)
    await vi.advanceTimersByTimeAsync(10000); expect(api.gpuStatus).toHaveBeenCalledTimes(count + 1)
  })
  it('replaces cards on refresh and clears old devices when the new response has no GPUs', async () => {
    const { source, api, monitor } = setup()
    await vi.advanceTimersByTimeAsync(0)
    expect(monitor.devices.value).toHaveLength(1)
    api.gpuStatus.mockResolvedValue({ ok: true, value: { devices: [], collectedAt: 2000, warning: 'No devices' } })
    await monitor.refresh()
    expect(monitor.devices.value).toEqual([])
    expect(monitor.collectedAt.value).toBe(2000)
    expect(monitor.warning.value).toBe('No devices')
    source.visible = false; source.id = 'other'
    expect(monitor.collectedAt.value).toBe(0)
    expect(monitor.warning.value).toBe('')
  })
})
