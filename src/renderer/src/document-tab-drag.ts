import { onBeforeUnmount, shallowRef } from 'vue'
import type { DocumentTab } from './workbench'

export const fileWindows = new WeakMap<DocumentTab, () => Promise<void>>()
export const windowClosed = new Map<string, () => void>()

export interface DragPoint { x: number; y: number }
type DragFeedback = DragPoint & { owner: string; name: string; action: 'detach' | 'dock'; ready: boolean }
export const tabDrag = shallowRef<DragFeedback>()

export function showTabDrag(owner: string, path: string, point: DragPoint, action: DragFeedback['action'], ready: boolean) {
  tabDrag.value = { owner, name: path.slice(path.lastIndexOf('/') + 1), ...point, action, ready }
}
export function clearTabDrag(owner: string) {
  if (tabDrag.value?.owner === owner) tabDrag.value = undefined
}

export function useFileTabDrag() {
  let cancel: (() => void) | undefined
  onBeforeUnmount(() => cancel?.())
  return function start(event: PointerEvent, update: (event?: PointerEvent) => void, release: (event: PointerEvent) => void) {
    if (event.button !== 0 || !event.isPrimary) return
    cancel?.()
    const handle = event.currentTarget as HTMLElement
    const id = event.pointerId
    let dragging = false
    const moved = (e: PointerEvent) => Math.hypot(e.screenX - event.screenX, e.screenY - event.screenY) >= 6
    function clear() {
      cancel = undefined
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', finish)
      handle.removeEventListener('lostpointercapture', clear)
      handle.removeEventListener('pointercancel', clear)
      window.removeEventListener('blur', clear)
      window.removeEventListener('keydown', key)
      if (handle.hasPointerCapture(id)) handle.releasePointerCapture(id)
      if (dragging) update()
    }
    function key(e: KeyboardEvent) { if (e.key === 'Escape') clear() }
    function move(e: PointerEvent) {
      if (e.pointerId !== id || !dragging && !moved(e)) return
      if (!(e.buttons & 1)) { clear(); return }
      dragging = true
      update(e)
    }
    function finish(e: PointerEvent) {
      if (e.pointerId !== id) return
      clear()
      if (dragging || moved(e)) release(e)
    }
    cancel = clear
    handle.setPointerCapture(id)
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', finish)
    handle.addEventListener('lostpointercapture', clear)
    handle.addEventListener('pointercancel', clear)
    window.addEventListener('blur', clear)
    window.addEventListener('keydown', key)
  }
}

export function workbenchPoint(point: DragPoint): DragPoint {
  // WebView zoom and screen coordinates can use different scales.
  const scale = window.outerWidth / window.innerWidth || 1
  return { x: (point.x - window.screenX) / scale,
    y: (point.y - window.screenY - window.outerHeight + window.innerHeight * scale) / scale }
}
export function insideWindow({ x, y }: DragPoint) {
  return x >= 0 && x < window.innerWidth && y >= 0 && y < window.innerHeight
}
export function overFileTabs({ x, y }: DragPoint) {
  const rect = document.querySelector('.document-tabs')?.getBoundingClientRect()
  return !!rect && x >= rect.left && x <= rect.right && y >= rect.top - 5 && y <= rect.bottom + 5
}
