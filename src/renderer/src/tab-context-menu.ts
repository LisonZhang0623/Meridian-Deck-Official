import { nextTick, onScopeDispose, ref, shallowRef } from 'vue'

export function useTabMenu<T>(beforeOpen?: () => void) {
  const tabMenu = shallowRef<{ target: T; trigger: HTMLElement; x: number; y: number } | null>(null)
  const tabMenuPanel = ref<HTMLElement>()
  async function openTabMenu(event: MouseEvent | KeyboardEvent, target: T): Promise<void> {
    if (event instanceof KeyboardEvent && event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')) return
    event.preventDefault()
    const tab = event.currentTarget as HTMLElement
    const trigger = (event instanceof KeyboardEvent && event.target instanceof HTMLElement ? event.target : tab.querySelector('button')) ?? tab
    const bounds = trigger.getBoundingClientRect()
    const x = event instanceof MouseEvent ? event.clientX : bounds.left
    const y = event instanceof MouseEvent ? event.clientY : bounds.bottom
    beforeOpen?.()
    tabMenu.value = { target, trigger, x, y }
    const opening = tabMenu.value
    await nextTick()
    if (tabMenu.value !== opening || !tabMenuPanel.value) return
    const panel = tabMenuPanel.value.getBoundingClientRect()
    const zoom = panel.width / tabMenuPanel.value.offsetWidth || 1
    tabMenu.value = { ...opening, x: Math.max(8, Math.min(x, innerWidth - panel.width - 8)) / zoom,
      y: Math.max(8, Math.min(y, innerHeight - panel.height - 8)) / zoom }
    const firstItem = tabMenuPanel.value.querySelector<HTMLButtonElement>('button:not(:disabled)')
    const focusTarget = firstItem ?? tabMenuPanel.value
    focusTarget.focus()
  }
  function tabMenuKey(event: KeyboardEvent): void {
    if (event.key === 'Escape' || event.key === 'Tab') {
      if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation() }
      tabMenu.value?.trigger.focus()
      tabMenu.value = null
      return
    }
    if (!['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    const buttons = [...(tabMenuPanel.value?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])]
    if (!buttons.length) return
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
    const down = event.key === 'ArrowDown'
    const start = index < 0 && !down ? 0 : index
    const step = down ? 1 : -1
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (start + step + buttons.length) % buttons.length
    buttons[next]?.focus()
  }

  const dismiss = () => { tabMenu.value = null }
  const outside = (event: PointerEvent) => {
    if (tabMenu.value && !tabMenuPanel.value?.contains(event.target as Node)) dismiss()
  }
  window.addEventListener('pointerdown', outside, true)
  window.addEventListener('blur', dismiss)
  onScopeDispose(() => {
    window.removeEventListener('pointerdown', outside, true)
    window.removeEventListener('blur', dismiss)
  })
  return { tabMenu, tabMenuPanel, openTabMenu, tabMenuKey }
}
