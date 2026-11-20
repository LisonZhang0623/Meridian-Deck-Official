function hasTextMenu(element: EventTarget | null): boolean {
  if (element instanceof HTMLTextAreaElement) return !element.disabled
  if (element instanceof HTMLInputElement) {
    return !element.disabled && ['text', 'search', 'url', 'tel', 'email', 'password', 'number'].includes(element.type)
  }
  return element instanceof HTMLElement && element.isContentEditable
}

export function protectWorkspaceFromReload(target: Window): void {
  target.addEventListener('keydown', event => {
    const refreshKey = event.key === 'BrowserRefresh' || (event.key === 'F5' && !event.altKey && !event.metaKey)
    const reloadShortcut = event.key.toLowerCase() === 'r' && (event.ctrlKey || event.metaKey) && !event.altKey
    // Suppress the browser action while allowing the focused terminal to handle the key.
    if (refreshKey || reloadShortcut) event.preventDefault()
  }, { capture: true })

  target.navigation?.addEventListener('navigate', event => {
    if (event.navigationType === 'reload') event.preventDefault()
  })

  target.addEventListener('contextmenu', event => {
    if (event.defaultPrevented || hasTextMenu(event.target)) return

    const { clientX, clientY } = event
    // xterm moves its clipboard textarea under the cursor before the event bubbles here.
    if (hasTextMenu(target.document.elementFromPoint(clientX, clientY))) return

    const selection = target.getSelection()
    if (selection && !selection.isCollapsed) {
      for (let index = 0; index < selection.rangeCount; index++) {
        const rects = selection.getRangeAt(index).getClientRects()
        if ([...rects].some(rect => clientX >= rect.left && clientX <= rect.right && clientY >= rect.top && clientY <= rect.bottom)) return
      }
    }

    // The native page menu can reload the WebView without a keyboard or navigate event.
    event.preventDefault()
  })
}
