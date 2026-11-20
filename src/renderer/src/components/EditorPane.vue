<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState, Compartment } from '@codemirror/state'
import { LanguageDescription, syntaxHighlighting } from '@codemirror/language'
import { languages } from '@codemirror/language-data'
import { openSearchPanel } from '@codemirror/search'
import { indentWithTab } from '@codemirror/commands'
import { codeExtension, textChange } from '../editor-sync'
import { documentWindow, type EditorPosition, type PreviewView } from '../document-window'
import { keymap } from '@codemirror/view'
import { Braces, ChevronLeft, ChevronRight, LayoutGrid, Maximize, RotateCw, Save, ZoomIn, ZoomOut } from '@lucide/vue'
import type { RemoteEntry } from '../../../shared/types'
import { formatJson } from '../document-formats'
import { editorHighlight } from '../editor-highlight'
import { attempt, saveDocument, reloadDocument, sizeLabel, openFile, parentPath, state, type DocumentTab, type Workspace } from '../workbench'
import StaticHtmlPreview from './StaticHtmlPreview.vue'
import { previewResources, type PreviewResources } from '../preview-resources'
import DocumentReadView from './DocumentReadView.vue'
import ImageGallery from './ImageGallery.vue'
import PagedTextPreview from './PagedTextPreview.vue'

const props = defineProps<{ doc: DocumentTab; session: Workspace; dark: boolean; detached?: boolean; dirty?: boolean }>()
const emit = defineEmits<{ save: []; reload: []; position: [value: EditorPosition] }>()
const dirty = computed(() => props.detached ? props.dirty : props.doc.content !== props.doc.saved)
function save() { if (props.detached) emit('save'); else void saveDocument(props.session, props.doc) }
function reload() { if (props.detached) emit('reload'); else void attempt(() => reloadDocument(props.session, props.doc)) }
const modKey = /Mac/i.test(navigator.platform) ? '⌘' : 'Ctrl'
const editorElement = ref<HTMLDivElement>()
const pane = ref<HTMLElement>()
const galleryOpen = ref(false), formatError = ref('')
const zoom = ref(100), fitImage = ref(true)
const imageWidth = ref(0), imageHeight = ref(0), imageError = ref(false)
const connected = computed(() => props.session.phase === 'connected')
const focused = computed(() => props.session.id === state.activeId && props.session.activeDocument === props.doc.path)
const extension = computed(() => props.doc.path.split('/').pop()?.split('.').pop()?.toLowerCase())
const readMode = computed(() => {
  if (extension.value === 'html' || extension.value === 'htm') return 'html'
  if (extension.value === 'csv' || extension.value === 'tsv') return extension.value
  if (['md', 'markdown', 'mdown', 'mkd'].includes(extension.value || '')) return 'markdown'
  return undefined
})
type ViewMode = 'source' | 'preview' | 'split'
const viewMode = ref<ViewMode>(readMode.value === 'html' && !props.doc.preview.page ? 'preview' : 'source')
const interactiveHtml = ref(true), htmlHasScripts = ref(false)
const assets = shallowRef<PreviewResources>()
watch(() => [props.session.id, connected.value, props.doc.path, viewMode.value !== 'source', props.doc.preview.version] as const,
  ([id, online, path, reading], _, cleanup) => {
    const resources = online && reading ? previewResources(window.sshTool, id, path) : undefined
    assets.value = resources
    cleanup(() => resources?.dispose())
  }, { immediate: true })
let sourceScroll = { top: 0, left: 0 }
async function showMode(next: ViewMode) {
  if (viewMode.value !== 'preview' && editor) sourceScroll = { top: editor.scrollDOM.scrollTop, left: editor.scrollDOM.scrollLeft }
  viewMode.value = next
  await nextTick()
  if (viewMode.value !== next) return
  if (next === 'preview') pane.value?.focus({ preventScroll: true })
  else if (editor) {
    editor.requestMeasure()
    editor.focus()
    editor.scrollDOM.scrollTo(sourceScroll.left, sourceScroll.top)
  }
}
function togglePreview() { void showMode(viewMode.value === 'preview' ? 'source' : 'preview') }
function markdownShortcut(event: KeyboardEvent) {
  if (readMode.value !== 'markdown' || props.doc.preview.page || event.altKey || !event.shiftKey || !(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 'v') return
  if (event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement) return
  event.preventDefault()
  event.stopPropagation()
  if (!event.repeat) togglePreview()
}
const siblingImages = computed(() => (props.session.tree[parentPath(props.doc.path)] || []).filter(entry => entry.type !== 'directory' && /\.(png|jpe?g|gif|webp|bmp)$/i.test(entry.name)))
const imageIndex = computed(() => siblingImages.value.findIndex(entry => entry.path === props.doc.path))

function imageLoaded(event: Event): void {
  const img = event.target as HTMLImageElement
  imageWidth.value = img.naturalWidth
  imageHeight.value = img.naturalHeight
  imageError.value = false
}
function selectImage(entry: RemoteEntry): void {
  galleryOpen.value = false
  void attempt(() => openFile(props.session, entry))
}
function nextImage(offset: number): void {
  const entry = siblingImages.value[imageIndex.value + offset]
  if (entry) selectImage(entry)
}
function formatCurrentJson(): void {
  try {
    props.doc.content = formatJson(props.doc.content, props.doc.preview.encoding)
    formatError.value = ''
  } catch (error) {
    formatError.value = `JSON 格式化失败。${(error as Error).message}`
  }
}

let fromSharedDraft = false
let editor: EditorView | undefined
function position(): EditorPosition | undefined {
  if (!editor) return
  const { anchor, head } = editor.state.selection.main
  return { anchor, head, top: editor.scrollDOM.scrollTop, left: editor.scrollDOM.scrollLeft }
}
function restore(p: EditorPosition) {
  if (!editor) return
  const length = editor.state.doc.length
  editor.dispatch({ selection: { anchor: Math.min(p.anchor, length), head: Math.min(p.head, length) } })
  editor.scrollDOM.scrollTo(p.left, p.top)
}
function sendPosition() {
  const current = position()
  if (props.detached && current) emit('position', current)
}
function view(): PreviewView {
  return { mode: viewMode.value, interactive: interactiveHtml.value, zoom: zoom.value, fit: fitImage.value }
}
function restoreView(value: PreviewView) {
  viewMode.value = value.mode
  zoom.value = value.zoom
  fitImage.value = value.fit
  galleryOpen.value = false
  interactiveHtml.value = value.interactive
}
defineExpose({ position, restore, sendPosition, view, restoreView })
const separate = props.detached ? undefined : documentWindow(props.session, props.doc, () => props.dark, position, restore, view, restoreView)
const imagePanel = ref<HTMLElement>(), image = ref<HTMLImageElement>()
function imageZoom(step: number, event?: WheelEvent) {
  const panel = imagePanel.value, img = image.value
  if (!panel || !img || !imageWidth.value) return
  const before = img.getBoundingClientRect(), box = panel.getBoundingClientRect()
  const x = event?.clientX ?? box.left + panel.clientWidth / 2
  const y = event?.clientY ?? box.top + panel.clientHeight / 2
  const fx = (x - before.left) / before.width, fy = (y - before.top) / before.height
  zoom.value = Math.max(10, Math.min(500, Math.round(before.width / imageWidth.value * 100) + step))
  fitImage.value = false
  void nextTick(() => {
    const after = img.getBoundingClientRect()
    panel.scrollBy(after.left + after.width * fx - x, after.top + after.height * fy - y)
  })
}
function wheel(event: WheelEvent) {
  if (!event.ctrlKey || !event.deltaY) return
  event.preventDefault()
  imageZoom(event.deltaY < 0 ? 10 : -10, event)
}
function previewKey(event: KeyboardEvent) {
  if (event.defaultPrevented || event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement) return
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f' && editor) {
    viewMode.value = 'source'
    void nextTick(() => { if (editor) openSearchPanel(editor) })
  } else if (event.key === 'Escape' && (!props.detached || viewMode.value !== 'source')) {
    if (galleryOpen.value) galleryOpen.value = false
    else if (viewMode.value !== 'source') void showMode('source')
    else props.session.activeDocument = 'terminal'
  } else if (props.doc.preview.kind === 'image' && !galleryOpen.value && !event.altKey) {
    if ((event.ctrlKey || event.metaKey) && event.key === '0') { zoom.value = 100; fitImage.value = false }
    else if (!event.ctrlKey && !event.metaKey && event.key.toLowerCase() === 'f') fitImage.value = true
    else return
  } else return
  event.preventDefault()
}
function editMode(allowed: boolean) { return [EditorState.readOnly.of(!allowed), EditorView.editable.of(allowed)] }

const language = new Compartment(), appearance = new Compartment(), editable = new Compartment()
function theme() {
  return EditorView.theme({
    '&': { height: '100%', backgroundColor: 'var(--bg-canvas)', color: 'var(--text)' },
    '.cm-scroller': { overflow: 'auto', fontFamily: 'var(--font-mono)', fontSize: 'var(--editor-font-size)' },
    '.cm-gutters': { backgroundColor: 'var(--bg-surface)', color: 'var(--muted)', borderColor: 'var(--border-soft)' },
    '.cm-activeLine': { backgroundColor: 'var(--highlight)' },
    '.cm-activeLineGutter': { backgroundColor: 'var(--highlight)' },
    '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': { backgroundColor: 'var(--selection)' },
    '.cm-cursor': { borderLeftColor: 'var(--blue)' },
  }, { dark: props.dark })
}
watch(editorElement, async element => {
  editor?.destroy()
  editor = undefined
  if (!element) return
  const view = new EditorView({
    parent: element,
    state: EditorState.create({
      doc: props.doc.content,
      extensions: [
        keymap.of([{ key: 'Mod-s', run: () => { save(); return true } }, indentWithTab]),
        basicSetup,
        syntaxHighlighting(editorHighlight),
        language.of([]),
        appearance.of(theme()),
        editable.of(editMode(connected.value)),
        EditorView.updateListener.of(update => {
          if (update.docChanged && !fromSharedDraft) {
            const value = update.state.doc.toString()
            props.doc.content = props.doc.content.includes('\r\n') ? value.replace(/\n/g, '\r\n') : value
          }
        }),
      ],
    }),
  })
  editor = view
  const ext = codeExtension(props.doc.path)
  const lang = ext && LanguageDescription.matchFilename(languages, 'file.' + ext)
  if (lang) {
    const support = await lang.load().catch(() => null)
    if (editor === view && support) view.dispatch({ effects: language.reconfigure(support) })
  }
}, { flush: 'post' })
watch(() => props.doc.content, value => {
  formatError.value = ''
  const normalized = value.replace(/\r\n/g, '\n')
  if (editor && normalized !== editor.state.doc.toString()) {
    fromSharedDraft = true
    try { editor.dispatch({ changes: textChange(editor.state.doc.toString(), normalized) }) }
    finally { fromSharedDraft = false }
  }
})
watch(() => props.doc.preview, value => {
  if (value.page) viewMode.value = 'source'
  if (value.kind !== 'image') galleryOpen.value = false
  imageError.value = false
})
watch(() => props.dark, () => editor?.dispatch({ effects: appearance.reconfigure(theme()) }))
watch(() => [state.settings.editorFontSize, state.settings.uiZoom], () => editor?.requestMeasure(), { flush: 'post' })
watch(connected, value => editor?.dispatch({ effects: editable.reconfigure(editMode(value)) }))
watch(viewMode, mode => { if (mode !== 'preview') editor?.requestMeasure() }, { flush: 'post' })
watch(focused, value => { if (value) editor?.requestMeasure() }, { flush: 'post' })
onBeforeUnmount(() => { separate?.dispose(); editor?.destroy(); editor = undefined })
</script>

<template>
  <div ref="pane" class="editor-pane" tabindex="-1" @keydown.capture="markdownShortcut" @keydown="previewKey" @focusout="sendPosition" :style="{ '--editor-font-size': state.settings.editorFontSize + 'px' }">
    <div class="document-toolbar preview-toolbar">
      <span class="document-path" :title="doc.path">{{ doc.path }}</span>
      <span class="muted preview-meta">{{ sizeLabel(doc.preview.size) }}<template v-if="doc.preview.kind === 'text'"> · {{ doc.preview.encoding || 'UTF-8' }}</template></span>
      <template v-if="doc.preview.kind === 'text'">
        <template v-if="!doc.preview.page">
          <button v-if="readMode" :title="readMode === 'markdown' ? modKey + '+Shift+V' : undefined" @click="togglePreview">{{ viewMode === 'preview' ? '编辑文本' : readMode === 'markdown' ? 'Markdown 预览' : readMode === 'html' ? 'HTML 预览' : '表格预览' }}</button>
          <button v-if="readMode === 'html' && viewMode === 'preview' && htmlHasScripts" @click="interactiveHtml = !interactiveHtml">{{ interactiveHtml ? '停止运行' : '运行交互' }}</button>
          <button v-if="readMode === 'markdown'" :aria-pressed="viewMode === 'split'" @click="showMode(viewMode === 'split' ? 'source' : 'split')">分栏预览</button>
          <button v-if="extension === 'json'" :disabled="doc.busy || !connected" @click="formatCurrentJson"><Braces :size="14"/>格式化 JSON</button>
          <button :disabled="doc.busy || !dirty || !connected" :title="'保存 (' + modKey + '+S)'" @click="save"><Save :size="14"/>{{ doc.busy ? '保存中…' : '保存' }}</button>
        </template>
        <button v-if="!doc.conflict" class="icon-button" title="重新加载" aria-label="重新加载" :disabled="doc.busy || !connected" @click="reload"><RotateCw :size="15"/></button>
      </template>
      <template v-else>
        <button v-if="!detached" :aria-pressed="galleryOpen" @click="galleryOpen = !galleryOpen"><LayoutGrid :size="14"/>{{ galleryOpen ? '返回单图' : '图片网格' }}</button>
        <template v-if="!galleryOpen">
          <span v-if="imageWidth" class="muted preview-meta">{{ imageWidth }} × {{ imageHeight }} · {{ fitImage ? '适应窗口' : zoom + '%' }}</span>
          <button v-if="!detached && siblingImages.length > 1" class="icon-button" aria-label="上一张图片" :disabled="imageIndex <= 0 || !connected" @click="nextImage(-1)"><ChevronLeft :size="15"/></button>
          <button v-if="!detached && siblingImages.length > 1" class="icon-button" aria-label="下一张图片" :disabled="imageIndex < 0 || imageIndex >= siblingImages.length - 1 || !connected" @click="nextImage(1)"><ChevronRight :size="15"/></button>
          <button class="icon-button" aria-label="缩小" :disabled="!imageWidth || imageError || !fitImage && zoom <= 10" @click="imageZoom(-25)"><ZoomOut :size="15"/></button>
          <button :title="'恢复原始尺寸 (' + modKey + '+0)'" :aria-pressed="!fitImage && zoom === 100" :disabled="!imageWidth || imageError" @click="fitImage = false; zoom = 100">原始尺寸</button>
          <button class="icon-button" aria-label="放大" :disabled="!imageWidth || imageError || !fitImage && zoom >= 500" @click="imageZoom(25)"><ZoomIn :size="15"/></button>
          <button class="icon-button" aria-label="适应窗口" title="适应窗口 (F)" :aria-pressed="fitImage" :disabled="!imageWidth || imageError" @click="fitImage = true"><Maximize :size="15"/></button>
        </template>
      </template>
    </div>
    <div v-if="doc.error || formatError" class="inline-error" role="alert">
      {{ doc.error || formatError }}
      <button v-if="doc.conflict" :disabled="doc.busy || !connected" @click="reload">重新加载</button>
    </div>
    <template v-if="doc.preview.kind === 'text'">
      <PagedTextPreview v-if="doc.preview.page" :doc="doc" :session="session" />
      <div v-else class="document-views" :class="{ split: viewMode === 'split' }">
        <div v-show="viewMode !== 'preview'" ref="editorElement" class="code-editor"></div>
        <StaticHtmlPreview v-if="!doc.detached && readMode === 'html' && viewMode === 'preview'" :source="doc.content" :assets="assets" :interactive="interactiveHtml" @scripts="htmlHasScripts = $event" />
        <DocumentReadView v-else-if="viewMode !== 'source' && readMode && readMode !== 'html'" :source="doc.content" :mode="readMode" :path="doc.path" :assets="assets" />
      </div>
    </template>
    <ImageGallery v-else-if="galleryOpen" :session="session" :current="doc.path" :active="focused" @open="selectImage" />
    <div v-else ref="imagePanel" class="image-preview" tabindex="0" @pointerdown="imagePanel?.focus()" @wheel="wheel">
      <p v-if="imageError" class="danger" role="alert">无法解码这张图片，请下载后检查文件。</p>
      <img ref="image" v-show="!imageError" :src="doc.preview.content" :alt="doc.path" :class="{ fit: fitImage }" :style="fitImage ? {} : { width: imageWidth ? imageWidth * zoom / 100 + 'px' : 'auto', maxWidth: 'none', maxHeight: 'none' }" @load="imageLoaded" @error="imageError = true">
    </div>
  </div>
</template>

<style scoped>
.document-views { flex: 1; display: flex; min-height: 0; min-width: 0; }
.document-views > * { min-width: 0; }
.document-views.split { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); grid-template-rows: minmax(0, 1fr); }
.document-views.split .code-editor { border-inline-end: 1px solid var(--border); }
.preview-toolbar{flex-wrap:wrap}
.preview-toolbar .document-path{flex:1;min-width:100px}
.preview-meta{white-space:nowrap}
</style>
