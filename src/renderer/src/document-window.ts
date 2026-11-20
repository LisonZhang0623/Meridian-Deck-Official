import { watch, type WatchStopHandle } from 'vue'
import { clearTabDrag, fileWindows, insideWindow, overFileTabs, showTabDrag, windowClosed, workbenchPoint } from './document-tab-drag'
import { draftReplica } from './editor-sync'
import { reloadDocument, saveDocument, state, unwrap, type DocumentTab, type Workspace } from './workbench'

export interface EditorPosition { anchor: number; head: number; top: number; left: number }
export interface PreviewView { mode: 'source' | 'preview' | 'split'; interactive: boolean; zoom: number; fit: boolean }
export type WindowStatus = ReturnType<typeof documentStatus>
function documentStatus(session: Workspace, doc: DocumentTab, dark: boolean) {
  return { kind: doc.preview.kind, path: doc.path, sessionId: session.id, phase: session.phase, dirty: doc.content !== doc.saved,
    busy: doc.busy, conflict: doc.conflict, error: doc.error, version: doc.preview.version,
    size: doc.preview.size, encoding: doc.preview.encoding, page: doc.preview.page && { ...doc.preview.page },
    dark, font: state.settings.editorFontSize, zoom: state.settings.uiZoom }
}
export function documentWindow(session: Workspace, doc: DocumentTab, dark: () => boolean,
  position: () => EditorPosition | undefined, restore: (position: EditorPosition) => void,
  view: () => PreviewView, restoreView: (value: PreviewView) => void) {
  const token = crypto.randomUUID()
  let channel: BroadcastChannel | undefined
  let stop: WatchStopHandle[] = []
  let ready = false
  function showTab(select = false) {
    if (doc.detached || select) {
      doc.detached = false
      if (select) state.activeId = session.id
      if (select || session.activeDocument === 'terminal') session.activeDocument = doc.path
    }
    clearTabDrag(token)
  }
  function hideTab() {
    doc.detached = true
    if (session.activeDocument === doc.path) session.activeDocument = 'terminal'
  }
  function dispose() {
    clearTabDrag(token)
    windowClosed.delete(token)
    fileWindows.delete(doc)
    ready = false
    stop.forEach(fn => fn())
    stop = []
    channel?.postMessage({ type: 'close' })
    channel?.close()
    channel = undefined
  }
  async function open() {
    const api = window.sshTool.openEditorWindow
    if (!api) throw new Error('当前桌面版本无法打开独立窗口')
    if (!channel) {
      const port = new BroadcastChannel('meridian-editor-' + token)
      channel = port
      windowClosed.set(token, () => { ready = false; showTab() })
      let applying = false, replica = draftReplica(true)
      port.onmessage = async ({ data }) => {
        switch (data.type) {
          case 'ready':
            replica = draftReplica(true)
            port.postMessage({ type: 'init', text: doc.content, position: position(), view: view(), status: documentStatus(session, doc, dark()) })
            ready = true
            hideTab()
            break
          case 'leave': ready = false; showTab(); break
          case 'drag': {
            const point = data.point && workbenchPoint(data.point)
            if (ready && point && insideWindow(point)) showTabDrag(token, doc.path, point, 'dock', overFileTabs(point))
            else clearTabDrag(token)
            break
          }
          case 'dock':
            if (data.point && !overFileTabs(workbenchPoint(data.point))) break
            showTab(true)
            if (data.view) restoreView(data.view)
            if (data.position) restore(data.position)
            port.postMessage({ type: 'close' })
            break
          case 'edit':
            applying = true
            try { doc.content = replica.receive(data.changes, doc.content) } finally { applying = false }
            port.postMessage({ type: 'ack' })
            break
          case 'ack': replica.acknowledge(); break
          case 'save': await saveDocument(session, doc); break
          case 'reload':
            if (doc.content !== data.content || doc.content !== doc.saved && !data.confirmedDirty) break
            await reloadDocument(session, doc, async () => true)
            if (doc.preview.page) port.postMessage({ type: 'page', preview: { ...doc.preview, page: { ...doc.preview.page } } })
            break
          case 'position': restore(data.position); break
        }
      }
      stop = [
        watch(() => doc.content, (value, old) => {
          if (ready && !applying) port.postMessage({ type: 'edit', changes: replica.change(old, value) })
        }, { flush: 'sync' }),
        watch(() => doc.preview.kind, () => { showTab(); dispose(); fileWindows.set(doc, open) }),
        watch(() => documentStatus(session, doc, dark()), status => {
          if (ready) port.postMessage({ type: 'status', status })
        }, { flush: 'post' }),
      ]
    }
    try {
      unwrap(await api(token))
      if (ready) hideTab()
    } catch (error) { showTab(); dispose(); fileWindows.set(doc, open); throw error }
  }
  fileWindows.set(doc, open)
  return { dispose }
}
