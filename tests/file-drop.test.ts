import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, reactive, type EffectScope } from 'vue'
import { hasDraggedFiles, useFileDrop } from '../src/renderer/src/useFileDrop'

let scope: EffectScope
function drag(type = 'dragover', file = true, extras: Record<string, unknown> = {}): DragEvent {
  const event = new Event(type, { cancelable: true })
  Object.defineProperties(event, Object.fromEntries(Object.entries({ clientX: 50, clientY: 50, relatedTarget: null, dataTransfer: { types: file ? ['Files'] : ['text/plain'], items: [], files: [], dropEffect: 'none' }, ...extras }).map(([key, value]) => [key, { value, configurable: true }])))
  return event as DragEvent
}
function fixture() {
  const document = Object.assign(new EventTarget(), { hidden: false })
  const host = Object.assign(new EventTarget(), { document })
  const context = reactive({ enabled: true, key: 'session-a:/home/test' })
  const child = {}, element = { getBoundingClientRect: () => ({ left: 10, top: 10, right: 210, bottom: 310 }), contains: (target: unknown) => target === child } as unknown as HTMLElement
  scope = effectScope()
  const drop = scope.run(() => useFileDrop(() => context, () => element, host as unknown as Window))!
  return { document, host, context, child, drop }
}
beforeEach(() => vi.useFakeTimers())
afterEach(() => { scope?.stop(); vi.useRealTimers() })

describe('upload drag hover recovery', () => {
  it('only accepts external file payloads and clears hover before processing a drop', () => {
    const { drop } = fixture(), text = drag('drop', false), file = drag('drop')
    drop.dragOver(text); expect(drop.dragging.value).toBe(false); expect(drop.acceptDrop(text)).toBe(false)
    drop.dragOver(file); expect(drop.dragging.value).toBe(true); expect(file.dataTransfer?.dropEffect).toBe('copy')
    expect(drop.acceptDrop(file)).toBe(true); expect(drop.dragging.value).toBe(false)
    expect(hasDraggedFiles(drag('dragover', false, { dataTransfer: { types: [], items: [{ kind: 'file' }] } }))).toBe(true)
  })
  it('keeps hover between nested children and clears it on a true leave, including a child-originated leave', () => {
    const { drop, child, host } = fixture()
    drop.dragOver(drag()); drop.dragLeave(drag('dragleave', true, { relatedTarget: child }))
    expect(drop.dragging.value).toBe(true)
    drop.dragLeave(drag('dragleave')); expect(drop.dragging.value).toBe(true)
    drop.dragLeave(drag('dragleave', true, { clientX: 300 })); expect(drop.dragging.value).toBe(false)
    drop.dragOver(drag()); host.dispatchEvent(drag('dragover', true, { clientY: 400 })); expect(drop.dragging.value).toBe(false)
  })
  it('recovers after a drop outside the file panel and permits the next external upload', () => {
    const { host, drop } = fixture()
    drop.dragOver(drag()); host.dispatchEvent(drag('drop')); expect(drop.dragging.value).toBe(false)
    drop.dragOver(drag()); expect(drop.dragging.value).toBe(true); expect(drop.acceptDrop(drag('drop'))).toBe(true)
  })
  it('clears cancelled OS drags without relying on DOM dragend', () => {
    const { host, document, drop } = fixture()
    for (const event of [new Event('blur'), new Event('focus'), Object.assign(new Event('keydown'), { key: 'Escape' }), new Event('pointerdown'), new Event('pointerup'), new Event('pointercancel'), new Event('mouseup'), Object.assign(new Event('mousemove'), { buttons: 0 })]) {
      drop.dragOver(drag()); expect(drop.dragging.value).toBe(true)
      host.dispatchEvent(event); expect(drop.dragging.value).toBe(false)
    }
    drop.dragOver(drag()); document.hidden = true; document.dispatchEvent(new Event('visibilitychange')); expect(drop.dragging.value).toBe(false)
  })
  it('expires abandoned hover and extends it while real dragover events keep arriving', async () => {
    const { drop } = fixture()
    drop.dragOver(drag()); await vi.advanceTimersByTimeAsync(400); drop.dragOver(drag())
    await vi.advanceTimersByTimeAsync(400); expect(drop.dragging.value).toBe(true)
    await vi.advanceTimersByTimeAsync(100); expect(drop.dragging.value).toBe(false)
  })
  it('ignores internal native files until the matching native completion, then accepts a new external drag', async () => {
    const { host, drop } = fixture()
    drop.dragOver(drag()); host.dispatchEvent(new CustomEvent('sshtool:drag-out-start', { detail: { sessionId: 'a', token: 'first' } }))
    expect(drop.dragging.value).toBe(false)
    for (const name of ['blur', 'focus', 'dragend', 'mouseup']) host.dispatchEvent(new Event(name))
    await vi.advanceTimersByTimeAsync(20000)
    drop.dragOver(drag()); expect(drop.dragging.value).toBe(false)
    const ownDrop = drag('drop'); host.dispatchEvent(ownDrop); expect(drop.acceptDrop(ownDrop)).toBe(false)
    drop.nativeEnded('a', 'old-token'); drop.dragOver(drag()); expect(drop.dragging.value).toBe(false)
    drop.nativeEnded('a', 'first'); drop.dragOver(drag()); expect(drop.dragging.value).toBe(true)
    expect(drop.acceptDrop(drag('drop'))).toBe(true)
  })
  it('recovers from a cancelled preparation or a bridge error even without dragend', () => {
    const { host, drop } = fixture()
    host.dispatchEvent(new Event('dragstart')); drop.dragOver(drag()); expect(drop.dragging.value).toBe(false)
    host.dispatchEvent(new Event('pointerup')); drop.dragOver(drag()); expect(drop.dragging.value).toBe(true)
    host.dispatchEvent(new CustomEvent('sshtool:drag-out-start', { detail: { sessionId: 'a', token: 'failed' } }))
    host.dispatchEvent(new CustomEvent('sshtool:drag-out-failed', { detail: { sessionId: 'a', token: 'failed' } }))
    drop.dragOver(drag()); expect(drop.dragging.value).toBe(true)
  })
  it('clears state on session/path/availability changes and disposes timers and listeners', () => {
    const { host, context, drop } = fixture()
    drop.dragOver(drag()); context.key = 'session-b:/tmp'; expect(drop.dragging.value).toBe(false)
    drop.dragOver(drag()); context.enabled = false; expect(drop.dragging.value).toBe(false)
    expect(drop.acceptDrop(drag('drop'))).toBe(false)
    context.enabled = true; drop.dragOver(drag()); scope.stop()
    expect(drop.dragging.value).toBe(false); expect(vi.getTimerCount()).toBe(0)
    host.dispatchEvent(new Event('dragstart')); drop.dragOver(drag()); expect(drop.dragging.value).toBe(true)
    drop.reset()
  })
})
