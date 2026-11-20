import { onScopeDispose, ref, watch } from 'vue'

export function hasDraggedFiles(event: DragEvent): boolean {
  const transfer = event.dataTransfer
  return !!transfer && (Array.from(transfer.types || []).includes('Files') || Array.from(transfer.items || []).some(item => item.kind === 'file'))
}

// OS file drags and Electron native drags do not reliably emit DOM dragend.
// Hover is only an indication of a current external file drag, never a latch.
export function useFileDrop(context: () => { enabled: boolean; key: string }, zone: () => HTMLElement | undefined, host: Window = window) {
  const dragging = ref(false)
  const ignoredDrops = new WeakSet<Event>()
  let internal = false, nativeKey = '', timer: ReturnType<typeof setTimeout> | undefined
  const hide = () => { dragging.value = false; clearTimeout(timer); timer = undefined }
  const reset = () => { internal = false; nativeKey = ''; hide() }
  const recover = () => { hide(); if (!nativeKey) internal = false }
  const inside = (event: DragEvent): boolean => {
    const element = zone()
    if (!element) return false
    const rect = element.getBoundingClientRect()
    return event.clientX >= rect.left && event.clientX < rect.right && event.clientY >= rect.top && event.clientY < rect.bottom
  }
  function dragOver(event: DragEvent): void {
    if (!context().enabled || internal || !hasDraggedFiles(event)) { hide(); return }
    event.preventDefault()
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
    dragging.value = true
    clearTimeout(timer)
    // Also recovers when the OS consumes the final drop/cancel event entirely.
    timer = setTimeout(hide, 500)
  }
  function dragLeave(event: DragEvent): void {
    const element = zone(), next = event.relatedTarget
    if (next && element) {
      try { if (element.contains(next as Node)) return } catch { /* relatedTarget can be a Window. */ }
      hide(); return
    }
    // relatedTarget is often null between descendants during a file drag.
    if (!inside(event)) hide()
  }
  function acceptDrop(event: DragEvent): boolean {
    const accept = context().enabled && !internal && !ignoredDrops.has(event) && hasDraggedFiles(event)
    recover()
    return accept
  }
  const startInternal = () => { internal = true; hide() }
  const startNative = (event: Event) => {
    const detail = (event as CustomEvent).detail
    if (!detail || typeof detail.sessionId !== 'string' || typeof detail.token !== 'string') return
    nativeKey = JSON.stringify([detail.sessionId, detail.token]); startInternal()
  }
  const nativeEnded = (sessionId: string, token: string) => { if (nativeKey === JSON.stringify([sessionId, token])) reset() }
  const nativeFailed = (event: Event) => { const detail = (event as CustomEvent).detail; if (detail) nativeEnded(detail.sessionId, detail.token) }
  const globalDrop = (event: Event) => { if (internal) ignoredDrops.add(event); recover() }
  const globalOver = (event: DragEvent) => { if (!inside(event) || !hasDraggedFiles(event)) hide() }
  const pointerMove = (event: MouseEvent) => { if (event.buttons === 0) recover() }
  const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') recover() }
  const visibility = () => { if (host.document.hidden) recover() }
  const listeners: [EventTarget, string, EventListener][] = [
    [host, 'dragstart', startInternal], [host, 'sshtool:drag-out-start', startNative], [host, 'sshtool:drag-out-failed', nativeFailed], [host, 'dragend', recover], [host, 'drop', globalDrop],
    [host, 'dragover', globalOver as EventListener], [host, 'blur', recover], [host, 'focus', recover],
    [host, 'pointerdown', recover], [host, 'pointerup', recover], [host, 'pointercancel', recover],
    [host, 'mouseup', recover], [host, 'mousemove', pointerMove as EventListener],
    [host, 'keydown', escape as EventListener], [host.document, 'visibilitychange', visibility]
  ]
  const capture = { capture: true }
  for (const [target, name, handler] of listeners) target.addEventListener(name, handler, capture)
  watch(() => [context().enabled, context().key], reset, { flush: 'sync' })
  onScopeDispose(() => { reset(); for (const [target, name, handler] of listeners) target.removeEventListener(name, handler, capture) })
  return { dragging, dragOver, dragLeave, acceptDrop, reset, nativeEnded }
}
