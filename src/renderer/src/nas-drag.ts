import { onScopeDispose, shallowRef } from 'vue'

export interface NasDrag { rootId: string; paths: string[] }

export function nasPathLabel(root: string, path: string): string {
  if (!path) return root
  const separator = /^[a-z]:|^\\\\/i.test(root) ? '\\' : '/'
  return `${root.endsWith(separator) ? root : root + separator}${path.split('/').join(separator)}`
}

interface NasPointer<T> {
  id: number
  x: number
  y: number
  files: T
  source: Element | null
}

export function useNasDrag<T extends { paths: string[] }, D>(
  destinationAt: (x: number, y: number, files: T) => D | undefined,
  drop: (files: T, destination: D) => void,
  host: Window = window
) {
  const dragging = shallowRef<{ x: number; y: number; count: number; files: T; destination?: D }>()
  let pointer: NasPointer<T> | undefined
  let suppressClick = false

  function cancel(): void {
    const previous = pointer
    pointer = undefined
    dragging.value = undefined
    if (previous?.source?.hasPointerCapture(previous.id)) previous.source.releasePointerCapture(previous.id)
  }

  function start(event: PointerEvent, files: T): void {
    if (!event.isPrimary || event.button !== 0 || !files.paths.length) return
    cancel()
    pointer = {
      id: event.pointerId, x: event.clientX, y: event.clientY,
      source: event.currentTarget as Element | null,
      files: { ...files, paths: [...files.paths] }
    }
  }

  function move(event: PointerEvent): void {
    if (pointer?.id !== event.pointerId) return
    if (!(event.buttons & 1)) { cancel(); return }
    const { clientX: x, clientY: y } = event
    if (!dragging.value) {
      if (Math.hypot(x - pointer.x, y - pointer.y) < 6) return
      suppressClick = true
      pointer.source?.setPointerCapture(pointer.id)
    }
    event.preventDefault()
    dragging.value = { x, y, count: pointer.files.paths.length, files: pointer.files, destination: destinationAt(x, y, pointer.files) }
  }

  function finish(event: PointerEvent): void {
    if (pointer?.id !== event.pointerId) return
    const files = pointer.files
    const moved = dragging.value || Math.hypot(event.clientX - pointer.x, event.clientY - pointer.y) >= 6
    const destination = moved ? destinationAt(event.clientX, event.clientY, files) : undefined
    if (moved) suppressClick = true
    cancel()
    if (destination !== undefined) drop(files, destination)
  }

  const click = (event: MouseEvent) => {
    if (event.detail && suppressClick) { event.preventDefault(); event.stopPropagation() }
    suppressClick = false
  }
  const press = () => { cancel(); suppressClick = false }
  const blur = (event: Event) => { if (event.target === host) cancel() }
  const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') cancel() }
  const listeners: [string, EventListener][] = [
    ['pointerdown', press], ['pointermove', move as EventListener], ['pointerup', finish as EventListener],
    ['pointercancel', cancel], ['blur', blur], ['keydown', escape as EventListener], ['click', click as EventListener]
  ]
  for (const [name, handler] of listeners) host.addEventListener(name, handler, true)
  onScopeDispose(() => {
    cancel()
    for (const [name, handler] of listeners) host.removeEventListener(name, handler, true)
  })
  return { dragging, start, cancel }
}
