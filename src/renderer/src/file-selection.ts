import { computed, ref } from 'vue'
import type { RemoteEntry } from '../../shared/types'

export function useFileSelection(entries: (directory: string) => RemoteEntry[]) {
  const directory = ref(''), anchor = ref(''), paths = ref(new Set<string>())
  const selected = computed(() => entries(directory.value).filter(entry => paths.value.has(entry.path)))
  function reset(parent = '') {
    directory.value = parent
    paths.value = new Set()
    anchor.value = ''
  }
  function select(parent: string, path: string, toggle = false, range = false) {
    if (directory.value !== parent) reset(parent)
    const rows = entries(parent)
    const start = rows.findIndex(row => row.path === anchor.value)
    const end = rows.findIndex(row => row.path === path)
    if (end < 0) return
    if (range && start >= 0) {
      paths.value = new Set(rows.slice(Math.min(start, end), Math.max(start, end) + 1).map(row => row.path))
    } else {
      const next = toggle ? new Set(selected.value.map(row => row.path)) : new Set<string>()
      if (next.has(path)) next.delete(path)
      else next.add(path)
      paths.value = next
      anchor.value = path
    }
  }
  function all(parent: string) {
    reset(parent)
    const rows = entries(parent)
    paths.value = new Set(rows.map(row => row.path))
    anchor.value = rows[0]?.path || ''
  }
  function prune() {
    paths.value = new Set(selected.value.map(row => row.path))
    if (!paths.value.has(anchor.value)) anchor.value = ''
  }
  return { selected, paths, directory, reset, select, all, prune }
}
