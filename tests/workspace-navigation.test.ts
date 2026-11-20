import { afterEach, describe, expect, it, vi } from 'vitest'
import { protectWorkspaceFromReload } from '../src/renderer/src/workspace-navigation'

afterEach(() => vi.unstubAllGlobals())

function keydown(key: string, modifiers: KeyboardEventInit = {}) {
  return Object.assign(new Event('keydown', { cancelable: true }), { key, ...modifiers })
}

describe('workspace reload protection', () => {
  it('prevents browser shortcuts while letting the focused control handle the same keys', () => {
    const target = new EventTarget()
    protectWorkspaceFromReload(target as unknown as Window)
    const handleKey = vi.fn()
    target.addEventListener('keydown', handleKey)

    const reloads = [
      keydown('F5'),
      keydown('F5', { ctrlKey: true }),
      keydown('BrowserRefresh'),
      keydown('r', { ctrlKey: true }),
      keydown('R', { ctrlKey: true, shiftKey: true }),
      keydown('r', { metaKey: true }),
      keydown('R', { metaKey: true, shiftKey: true })
    ]
    for (const event of reloads) {
      expect(target.dispatchEvent(event)).toBe(false)
      expect(event.cancelBubble).toBe(false)
      expect(handleKey).toHaveBeenLastCalledWith(event)
    }

    const otherKeys = [
      keydown('r'),
      keydown('c', { ctrlKey: true }),
      keydown('r', { ctrlKey: true, altKey: true }),
      keydown('F5', { altKey: true })
    ]
    for (const event of otherKeys) {
      expect(target.dispatchEvent(event)).toBe(true)
    }
  })

  it('cancels reload navigation when the WebView exposes the Navigation API', () => {
    const navigation = new EventTarget()
    const target = Object.assign(new EventTarget(), { navigation })
    protectWorkspaceFromReload(target as unknown as Window)

    for (const navigationType of ['reload', 'push', 'replace', 'traverse']) {
      const event = Object.assign(new Event('navigate', { cancelable: true }), { navigationType })
      expect(navigation.dispatchEvent(event)).toBe(navigationType !== 'reload')
    }
  })

  it('hides the page context menu while allowing text and application menus', () => {
    class Element {
      constructor(readonly isContentEditable = false) {}
    }
    class Input extends Element {
      constructor(readonly type: string, readonly disabled = false) { super() }
    }
    class TextArea extends Element {
      readonly disabled = false
    }
    vi.stubGlobal('HTMLElement', Element)
    vi.stubGlobal('HTMLInputElement', Input)
    vi.stubGlobal('HTMLTextAreaElement', TextArea)

    const plain = new Element()
    const elementFromPoint = vi.fn<() => Element>().mockReturnValue(plain)
    const selection = {
      isCollapsed: false,
      rangeCount: 1,
      getRangeAt: () => ({ getClientRects: () => [{ left: 5, top: 5, right: 15, bottom: 15 }] })
    }
    const getSelection = vi.fn<() => typeof selection | null>().mockReturnValue(null)
    const target = Object.assign(new EventTarget(), { document: { elementFromPoint }, getSelection })
    protectWorkspaceFromReload(target as unknown as Window)

    function contextMenu(element: Element, x = 10) {
      const event = Object.assign(new Event('contextmenu', { cancelable: true }), { clientX: x, clientY: 10 })
      Object.defineProperty(event, 'target', { value: element })
      return event
    }

    expect(target.dispatchEvent(contextMenu(plain))).toBe(false)
    expect(target.dispatchEvent(contextMenu(new Input('checkbox')))).toBe(false)
    expect(target.dispatchEvent(contextMenu(new Input('text')))).toBe(true)
    expect(target.dispatchEvent(contextMenu(new Element(true)))).toBe(true)

    elementFromPoint.mockReturnValueOnce(new TextArea())
    expect(target.dispatchEvent(contextMenu(plain))).toBe(true)

    getSelection.mockReturnValue(selection)
    expect(target.dispatchEvent(contextMenu(plain))).toBe(true)
    expect(target.dispatchEvent(contextMenu(plain, 20))).toBe(false)

    const fileMenu = contextMenu(plain)
    fileMenu.preventDefault()
    elementFromPoint.mockClear()
    expect(target.dispatchEvent(fileMenu)).toBe(false)
    expect(elementFromPoint).not.toHaveBeenCalled()
  })
})
