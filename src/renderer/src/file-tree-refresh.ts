import { onMounted, onScopeDispose, watch, type Ref } from 'vue'
import { active, refreshFileTree } from './workbench'

const refreshInterval = 5_000

export function useFileTreeRefresh(enabled: Readonly<Ref<boolean>>): void {
  let timer: number | undefined
  const pending = new Set<string>()
  function refresh(): void {
    const session = active.value
    if (!enabled.value || document.hidden || !session || pending.has(session.id)) return
    const { id } = session
    pending.add(id)
    void refreshFileTree(session, true).finally(() => pending.delete(id))
  }
  watch(() => enabled.value ? active.value?.id : undefined, id => { if (id) refresh() })
  onMounted(() => {
    timer = window.setInterval(refresh, refreshInterval)
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', refresh)
  })
  onScopeDispose(() => {
    window.clearInterval(timer)
    window.removeEventListener('focus', refresh)
    document.removeEventListener('visibilitychange', refresh)
  })
}
