import { computed, onScopeDispose, ref, watch } from 'vue'
import type { Api } from '../../shared/types'
import type { ResourceSnapshot } from '../../shared/v2'

export function resourceRates(current: ResourceSnapshot, previous?: ResourceSnapshot | null) {
  const valid = previous && current.collectedAt > previous.collectedAt && current.uptime >= previous.uptime
  const elapsed = valid ? (current.collectedAt - previous.collectedAt) / 1000 : 0
  const total = valid ? current.cpu.total - previous.cpu.total : 0
  const idle = valid ? current.cpu.idle - previous.cpu.idle : 0
  const cpu = total > 0 && idle >= 0 && idle <= total ? (1 - idle / total) * 100 : null
  const network = current.network.map(net => {
    const before = valid ? previous.network.find(item => item.name === net.name) : undefined
    return { name: net.name,
      received: before && elapsed > 0 && net.received >= before.received ? (net.received - before.received) / elapsed : null,
      sent: before && elapsed > 0 && net.sent >= before.sent ? (net.sent - before.sent) / elapsed : null }
  })
  return { cpu, network }
}

export function useResources(source: () => { id: string; identity: string; connected: boolean; interval: number }, api: Pick<Api, 'resourceStatus' | 'cancelResourceStatus'>) {
  const snapshot = ref<ResourceSnapshot | null>(null), previous = ref<ResourceSnapshot | null>(null)
  const error = ref(''), busy = ref(false), paused = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined, generation = 0, pendingId = '', identity = '', disposed = false
  const enabled = () => !disposed && !!source().id && source().connected
  function stop(): void {
    generation++; clearTimeout(timer); timer = undefined
    if (pendingId) void api.cancelResourceStatus(pendingId).catch(() => {})
    pendingId = ''; busy.value = false
  }
  async function refresh(): Promise<void> {
    if (!enabled() || busy.value) return
    clearTimeout(timer); timer = undefined
    const token = generation, id = source().id
    busy.value = true; pendingId = id; error.value = ''
    try {
      const result = await api.resourceStatus(id)
      if (token !== generation) return
      if (!result.ok) throw new Error(result.error)
      previous.value = snapshot.value; snapshot.value = result.value
    } catch (cause) { if (token === generation) { error.value = (cause as Error).message; paused.value = true } }
    finally {
      if (token === generation) {
        busy.value = false; pendingId = ''
        if (enabled() && !paused.value) timer = setTimeout(() => { void refresh() }, source().interval * 1000)
      }
    }
  }
  watch(() => [source().id, source().identity, source().connected, source().interval, paused.value], () => {
    stop()
    const key = JSON.stringify([source().id, source().identity])
    if (identity !== key || !source().connected) { identity = key; snapshot.value = null; previous.value = null; error.value = ''; paused.value = false }
    if (enabled() && !paused.value) void refresh()
  }, { immediate: true, flush: 'sync' })
  onScopeDispose(() => { disposed = true; stop() })
  const rates = computed(() => snapshot.value ? resourceRates(snapshot.value, previous.value) : { cpu: null, network: [] })
  return { snapshot, rates, error, busy, paused, refresh }
}
