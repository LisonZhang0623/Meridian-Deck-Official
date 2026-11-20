import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { useNasDrag } from '../src/renderer/src/nas-drag'

const files = { rootId: 'selected-root', paths: ['data/训练集.txt', 'checkpoint.bin'] }
let scope: EffectScope

function pointer(type: string, clientX: number, clientY: number, buttons = 1): PointerEvent {
  return Object.assign(new Event(type, { cancelable: true }), { pointerId: 1, isPrimary: true, button: 0, buttons, clientX, clientY }) as PointerEvent
}

function fixture() {
  const host = new EventTarget(), upload = vi.fn()
  scope = effectScope()
  const drag = scope.run(() => useNasDrag((x, y) => x > 50 && y < 100 ? '/remote/folder' : undefined, upload, host as unknown as Window))!
  return { host, upload, drag }
}

afterEach(() => scope.stop())

describe('NAS file drag', () => {
  it('uses a local gesture and uploads the selected files to the folder under the release point', () => {
    const { host, upload, drag } = fixture()
    drag.start(pointer('pointerdown', 80, 150), files)
    const inputBlur = new Event('blur')
    Object.defineProperty(inputBlur, 'target', { value: new EventTarget() })
    host.dispatchEvent(inputBlur)
    host.dispatchEvent(pointer('pointermove', 82, 148))
    expect(drag.dragging.value).toBeUndefined()
    host.dispatchEvent(pointer('pointermove', 80, 90))
    expect(drag.dragging.value).toMatchObject({ count: 2, destination: '/remote/folder' })
    host.dispatchEvent(pointer('pointerup', 80, 90, 0))
    expect(upload).toHaveBeenCalledExactlyOnceWith(files, '/remote/folder')
    expect(drag.dragging.value).toBeUndefined()
    const click = Object.assign(new Event('click', { cancelable: true }), { detail: 1 })
    host.dispatchEvent(click)
    expect(click.defaultPrevented).toBe(true)
  })

  it('lets clicks select files and ignores drags cancelled or released outside the remote tree', () => {
    const { host, upload, drag } = fixture()
    drag.start(pointer('pointerdown', 80, 150), files)
    host.dispatchEvent(pointer('pointerup', 81, 150, 0))
    const click = Object.assign(new Event('click', { cancelable: true }), { detail: 1 })
    host.dispatchEvent(click)
    expect(click.defaultPrevented).toBe(false)
    drag.start(pointer('pointerdown', 80, 150), files)
    host.dispatchEvent(pointer('pointermove', 80, 90))
    host.dispatchEvent(Object.assign(new Event('keydown'), { key: 'Escape' }))
    host.dispatchEvent(pointer('pointerup', 80, 90, 0))
    drag.start(pointer('pointerdown', 80, 150), files)
    host.dispatchEvent(pointer('pointermove', 40, 90))
    host.dispatchEvent(pointer('pointerup', 40, 90, 0))
    drag.start(pointer('pointerdown', 80, 150), files)
    host.dispatchEvent(pointer('pointermove', 80, 90))
    host.dispatchEvent(new Event('blur'))
    host.dispatchEvent(pointer('pointerup', 80, 90, 0))
    expect(upload).not.toHaveBeenCalled()
    expect(drag.dragging.value).toBeUndefined()
  })
})
