<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { Window } from '@wailsio/runtime'
import { draftReplica } from '../editor-sync'
import { clearTabDrag, insideWindow, showTabDrag, useFileTabDrag } from '../document-tab-drag'
import { useTabMenu } from '../tab-context-menu'
import TabDragFeedback from './TabDragFeedback.vue'
import WindowControls from './WindowControls.vue'
import type { WindowStatus } from '../document-window'
import { state, type DocumentTab, type Workspace } from '../workbench'
import { confirmAction, pendingConfirmation, resolveConfirmation } from '../confirmation'
import ConfirmationDialog from './ConfirmationDialog.vue'
import EditorPane from './EditorPane.vue'

const { tabMenu, tabMenuPanel, openTabMenu, tabMenuKey } = useTabMenu<void>()
const dragFileTab = useFileTabDrag()
const props = defineProps<{ token: string; captionWindow?: typeof Window }>()
const port = new BroadcastChannel('meridian-editor-' + props.token)
const replica = draftReplica(false)
const pane = ref<InstanceType<typeof EditorPane>>()
const doc = ref<DocumentTab>(), status = ref<WindowStatus>()
const session = reactive<Workspace>({ id: '', profileId: '', connectionKey: '', endpoint: '', name: '', phase: 'connecting',
  message: '', home: '', directory: '', terminalNumber: 0, activeDocument: '', documents: [], tree: {}, expanded: new Set(), loading: new Set(), terminalBuffer: [], terminalBytes: 0 })
let applying = false
function applyStatus(value: WindowStatus) {
  status.value = value
  session.id = value.sessionId
  session.phase = value.phase
  session.activeDocument = value.path
  state.settings.editorFontSize = value.font
  state.settings.uiZoom = value.zoom
  if (doc.value) {
    Object.assign(doc.value, { busy: value.busy, error: value.error, conflict: value.conflict })
    if (!value.dirty) doc.value.saved = doc.value.content
    Object.assign(doc.value.preview, { version: value.version, size: value.size, encoding: value.encoding, page: value.page })
  }
  void Window.SetTitle((value.dirty ? '● ' : '') + value.path + ' · 子午线')
}
port.onmessage = async ({ data }) => {
  if (data.type === 'close') { await Window.Close(); return }
  if (data.type === 'init') {
    const s: WindowStatus = data.status
    doc.value = { path: s.path, preview: { path: s.path, kind: s.kind, version: s.version, size: s.size, modified: 0, content: data.text, encoding: s.encoding, page: s.page },
      content: data.text, saved: data.text, busy: false, conflict: false, error: '' }
    applyStatus(s)
    await nextTick()
    if (data.view) await pane.value?.restoreView(data.view)
    if (data.position) pane.value?.restore(data.position)
  }
  if (data.type === 'page' && doc.value) doc.value.preview = data.preview
  if (data.type === 'status') applyStatus(data.status)
  if (data.type === 'ack') replica.acknowledge()
  if (data.type === 'edit' && doc.value) {
    applying = true
    try { doc.value.content = replica.receive(data.changes, doc.value.content) } finally { applying = false }
    port.postMessage({ type: 'ack' })
  }
}
watch(() => doc.value?.content, (value, old) => {
  if (!applying && value !== undefined && old !== undefined) port.postMessage({ type: 'edit', changes: replica.change(old, value) })
}, { flush: 'sync' })
async function reload() {
  if (!doc.value || !status.value) return
  const content = doc.value.content
  const confirmedDirty = status.value.dirty || doc.value.content !== doc.value.saved
  if (confirmedDirty && !await confirmAction({ title: '重新加载文件？', message: '当前未保存的修改将丢失。', action: '重新加载', danger: true })) return
  if (doc.value.content === content) port.postMessage({ type: 'reload', content, confirmedDirty })
}
function dock(point?: { x: number; y: number }) {
  port.postMessage({ type: 'dock', point, view: pane.value?.view(), position: pane.value?.position() })
}
function startReturn(event: PointerEvent) {
  if (!doc.value) return
  const path = doc.value.path
  const outside = (e: PointerEvent) => !insideWindow({ x: e.clientX, y: e.clientY })
  dragFileTab(event, e => {
    if (e && !outside(e)) showTabDrag(props.token, path, { x: e.clientX, y: e.clientY }, 'dock', false)
    else clearTabDrag(props.token)
    port.postMessage({ type: 'drag', point: e && outside(e) ? { x: e.screenX, y: e.screenY } : undefined })
  }, end => { if (outside(end)) dock({ x: end.screenX, y: end.screenY }) })
}
function blurred() { pane.value?.sendPosition() }
function leave() { blurred(); port.postMessage({ type: 'leave' }) }
onMounted(() => {
  window.addEventListener('blur', blurred)
  window.addEventListener('pagehide', leave)
  port.postMessage({ type: 'ready' })
})
onBeforeUnmount(() => { window.removeEventListener('blur', blurred); window.removeEventListener('pagehide', leave); port.close() })
</script>

<template>
  <main class="app-shell" :data-theme="status?.dark ? 'dark' : 'light'">
    <header class="preview-caption">
      <button v-if="doc" class="preview-tab-handle" title="拖到工作台的文件标签栏" aria-haspopup="menu" :aria-expanded="!!tabMenu" @pointerdown="startReturn" @contextmenu="openTabMenu($event)" @keydown="openTabMenu($event)">{{ doc.path.split('/').pop() }}<span v-if="status?.dirty"> ●</span></button>
      <span class="preview-caption-space" @dblclick="captionWindow?.ToggleMaximise()"></span>
      <WindowControls v-if="captionWindow" :native-window="captionWindow" />
    </header>
    <EditorPane v-if="doc && status" ref="pane" :doc="doc" :session="session" :dark="status.dark" :dirty="status.dirty" detached
      @save="port.postMessage({ type: 'save' })" @reload="reload" @position="position => port.postMessage({ type: 'position', position })" />
    <TabDragFeedback />
    <div v-if="tabMenu" ref="tabMenuPanel" role="menu" aria-label="文件标签" tabindex="-1" class="context-menu" :style="{ left: tabMenu.x + 'px', top: tabMenu.y + 'px' }" @contextmenu.prevent @keydown="tabMenuKey">
      <button type="button" role="menuitem" tabindex="-1" @click="tabMenu = null; dock()">收回到工作台</button>
    </div>
    <ConfirmationDialog v-if="pendingConfirmation" :request="pendingConfirmation" @resolve="resolveConfirmation" />
  </main>
</template>

<style scoped>
.app-shell > .editor-pane { position: relative; inset: auto; flex: 1; }
.preview-caption { flex-shrink: 0; }
.preview-caption { display: flex; align-items: stretch; min-height: 36px; background: var(--bg-surface); color: var(--text); border-bottom: 1px solid var(--border); }
.preview-caption-space { flex: 1; --wails-draggable: drag; }
.preview-tab-handle { background: transparent; color: inherit; border: 0; padding: 0 12px; }
.preview-tab-handle { max-width: 50%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: grab; touch-action: none; user-select: none; }
.preview-tab-handle:active { cursor: grabbing; }
</style>
