import { onScopeDispose, ref, watch } from 'vue'
import type { Api, GpuDevice } from '../../shared/types'

export interface GpuSource { id: string; identity: string; connected: boolean; visible: boolean; interval: number }
export function useGpuMonitor(source: () => GpuSource, api: Pick<Api, 'gpuStatus' | 'cancelGpuStatus'>) {
  const devices = ref<GpuDevice[]>([]), collectedAt = ref(0), warning = ref(''), error = ref(''), busy = ref(false), paused = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined, generation = 0, pendingId = '', identity = ''
  let disposed = false
  const enabled = () => { const s = source(); return !disposed && !!s.id && s.connected && s.visible }
  function stop(): void {
    generation++; clearTimeout(timer); timer = undefined
    if (pendingId) void api.cancelGpuStatus(pendingId).catch(() => {})
    pendingId = ''; busy.value = false
  }
  async function refresh(): Promise<void> {
    if (!enabled() || busy.value) return
    clearTimeout(timer); timer = undefined
    const token = generation, id = source().id
    pendingId = id; busy.value = true; error.value = ''
    try {
      const result = await api.gpuStatus(id)
      if (token !== generation) return
      if (!result.ok) throw new Error(result.error)
      devices.value = result.value.devices; collectedAt.value = result.value.collectedAt; warning.value = result.value.warning || ''
    } catch (cause) {
      if (token !== generation) return
      error.value = (cause as Error).message || '读取显卡状态失败'
      paused.value = true
    } finally {
      if (token === generation) {
        pendingId = ''; busy.value = false
        if (enabled() && !paused.value) timer = setTimeout(() => { void refresh() }, source().interval * 1000)
      }
    }
  }
  watch(() => [source().id, source().identity, source().connected, source().visible, source().interval, paused.value], () => {
    stop()
    const key = JSON.stringify([source().id, source().identity])
    if (key !== identity) { identity = key; devices.value = []; collectedAt.value = 0; warning.value = ''; error.value = ''; paused.value = false }
    if (enabled() && !paused.value) void refresh()
  }, { immediate: true, flush: 'sync' })
  onScopeDispose(() => { disposed = true; stop() })
  return { devices, collectedAt, warning, error, busy, paused, refresh }
}
