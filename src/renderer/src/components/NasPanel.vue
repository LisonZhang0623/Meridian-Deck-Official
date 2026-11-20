<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ArrowUp, Check, ChevronRight, Download, File, Folder, FolderOpen, HardDrive, RefreshCw, Upload, X } from '@lucide/vue'
import type { NasDirectory, NasEntry, Result } from '../../../shared/types'
import { nasPathLabel, type NasDrag } from '../nas-drag'
import { sizeLabel, unwrap } from '../workbench'

const props = defineProps<{ uploadReady: boolean; dropTarget?: { path: string; label: string } }>()
const emit = defineEmits<{ close: []; upload: [payload: NasDrag]; drag: [event: PointerEvent, payload: NasDrag]; view: [directory: NasDirectory | null] }>()
const listing = ref<NasDirectory | null>(null)
const selected = ref(new Set<string>())
const list = ref<HTMLElement>()
const currentPath = ref<HTMLElement>()
const busy = ref(false)
const error = ref('')
const maxSelection = 100
let mounted = true
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let refreshPending = false

const displayPath = computed(() => listing.value ? nasPathLabel(listing.value.root, listing.value.path) : '')

onMounted(() => void openDirectory(''))
watch([listing, busy, error], () => emit('view', busy.value || error.value ? null : listing.value), { flush: 'sync' })
const unsubscribe = window.sshTool.onEvent(event => {
  if (event.type === 'transfer' && event.transfer.purpose === 'nas' && event.transfer.direction === 'download' && event.transfer.status === 'completed') refresh()
})
onBeforeUnmount(() => { mounted = false; clearTimeout(refreshTimer); unsubscribe(); emit('view', null) })

function refresh(): void {
  clearTimeout(refreshTimer)
  refreshTimer = setTimeout(() => {
    if (busy.value) { refreshPending = true; return }
    if (listing.value) void openDirectory(listing.value.path)
  }, 180)
}
defineExpose({ refresh })

async function readDirectory(request: () => Promise<Result<NasDirectory | null>>, choosing = false): Promise<void> {
  if (busy.value) return
  busy.value = true
  try {
    const directory = unwrap(await request())
    if (!mounted || (choosing && !directory)) return
    const changed = directory?.rootId !== listing.value?.rootId || directory?.path !== listing.value?.path
    if (changed) selected.value.clear()
    else {
      const files = new Set(directory?.entries.filter(entry => entry.type === 'file').map(entry => entry.path))
      for (const path of selected.value) if (!files.has(path)) selected.value.delete(path)
    }
    listing.value = directory
    error.value = ''
    if (changed && list.value) list.value.scrollTop = 0
  } catch (cause) {
    if (mounted) error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    if (mounted) {
      busy.value = false
      if (refreshPending) { refreshPending = false; refresh() }
    }
  }
}

async function openDirectory(path: string, focus = false): Promise<void> {
  const { nasList } = window.sshTool
  if (!nasList) return
  await readDirectory(() => nasList(path, listing.value?.rootId))
  if (focus && listing.value?.path === path) {
    await nextTick()
    currentPath.value?.focus()
  }
}

function chooseDirectory(): void {
  const { nasSelect } = window.sshTool
  if (nasSelect) void readDirectory(nasSelect, true)
}

function openParent(): void {
  const parent = listing.value?.parent
  if (parent != null) void openDirectory(parent, true)
}

function select(entry: NasEntry): void {
  if (entry.type === 'directory') { void openDirectory(entry.path, true); return }
  if (selected.value.has(entry.path)) selected.value.delete(entry.path)
  else if (selected.value.size < maxSelection) selected.value.add(entry.path)
}

function drag(event: PointerEvent, entry: NasEntry): void {
  const directory = listing.value
  if (!props.uploadReady || busy.value || !directory || error.value) return
  const paths = selected.value.has(entry.path) ? [...selected.value] : [entry.path]
  emit('drag', event, { rootId: directory.rootId, paths })
}

function uploadSelected(): void {
  const rootId = listing.value?.rootId
  if (props.uploadReady && !busy.value && rootId && selected.value.size) emit('upload', { rootId, paths: [...selected.value] })
}
</script>

<template>
  <section id="nas-source-panel" class="nas-panel" :class="{ 'nas-receiving': dropTarget }" aria-label="NAS 文件" :aria-busy="busy">
    <div class="section-heading">
      <span><HardDrive :size="15"/>NAS 文件</span>
      <div class="sidebar-heading-actions">
        <button v-if="listing" type="button" class="icon-button" title="上级 NAS 目录" aria-label="上级 NAS 目录" :disabled="busy || listing?.parent == null" @click="openParent"><ArrowUp :size="14"/></button>
        <button v-if="listing || error" type="button" class="icon-button" title="刷新 NAS 目录" aria-label="刷新 NAS 目录" :disabled="busy" @click="openDirectory(listing?.path || '')"><RefreshCw :size="14" :class="{ spin: busy }"/></button>
        <button v-if="listing" type="button" class="icon-button" title="更换 NAS 文件夹" aria-label="更换 NAS 文件夹" :disabled="busy" @click="chooseDirectory"><FolderOpen :size="15"/></button>
        <button type="button" class="icon-button" title="收起 NAS 文件" aria-label="收起 NAS 文件" @click="emit('close')"><X :size="15"/></button>
      </div>
    </div>

    <div v-if="displayPath" ref="currentPath" class="nas-path" :title="displayPath" tabindex="-1">{{ displayPath }}</div>
    <div ref="list" class="nas-scroll">
      <p v-if="error" class="nas-error" role="alert">{{ error }}</p>
      <p v-if="busy && !listing && !error" class="side-empty" role="status">正在读取 NAS 目录…</p>
      <div v-else-if="!listing" class="nas-empty">
        <button type="button" :disabled="busy" @click="chooseDirectory"><FolderOpen :size="14"/>选择 NAS 文件夹</button>
        <p>可选择系统已连接的共享文件夹或映射盘。</p>
      </div>
      <template v-if="listing">
        <p v-if="listing.truncated" class="nas-note">目录条目较多，仅显示部分内容。</p>
        <p v-if="!listing.entries.length && !error" class="nas-note">没有可浏览的文件或文件夹。</p>
        <ul v-else class="file-tree" aria-label="NAS 目录内容">
          <li v-for="entry in listing.entries" :key="entry.path">
            <button type="button" class="file-row nas-row" :class="{ selected: selected.has(entry.path) || dropTarget?.path === entry.path, 'nas-draggable': uploadReady && !busy && !error }"
              :data-nas-local-directory="entry.type === 'directory' ? entry.path : undefined"
              :title="entry.name" :aria-pressed="entry.type === 'file' ? selected.has(entry.path) : undefined"
              :disabled="busy || (entry.type === 'file' && !selected.has(entry.path) && selected.size >= maxSelection)"
              @click="select(entry)" @pointerdown="drag($event, entry)">
              <ChevronRight v-if="entry.type === 'directory'" :size="13" aria-hidden="true"/>
              <span v-else class="nas-check" aria-hidden="true"><Check v-if="selected.has(entry.path)" :size="11"/></span>
              <Folder v-if="entry.type === 'directory'" class="folder-icon" :size="15" aria-hidden="true"/>
              <File v-else :size="15" aria-hidden="true"/>
              <span class="nas-name">{{ entry.name }}</span>
              <small v-if="entry.type === 'file'">{{ sizeLabel(entry.size) }}</small>
            </button>
          </li>
        </ul>
      </template>
    </div>
    <div v-if="listing" class="nas-footer">
      <span>{{ uploadReady ? '文件和文件夹可拖到远程文件树' : '连接服务器后可传输' }}</span>
      <button type="button" class="primary" :disabled="busy || !uploadReady || !selected.size" @click="uploadSelected"><Upload :size="12"/>上传所选{{ selected.size ? ` (${selected.size})` : '' }}</button>
    </div>
    <div v-if="dropTarget" class="nas-drop-destination" :title="dropTarget.label"><Download :size="14"/><span>下载到 {{ dropTarget.label }}</span></div>
  </section>
</template>

<style scoped>
.nas-panel { position: relative; display: flex; flex: 0 1 43%; flex-direction: column; min-height: 0; min-width: 0; border-top: 1px solid var(--border); background: var(--bg-canvas); }
.nas-panel.nas-receiving { outline: 1px dashed var(--blue); outline-offset: -1px; }
.section-heading, .nas-path, .nas-footer { flex: 0 0 auto; }
.nas-path { margin: 6px 8px 4px; padding: 4px 7px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--bg-surface); font: 10.5px/1.4 var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.nas-scroll { flex: 1; min-height: 0; overflow: auto; padding: 2px 6px; }
.nas-row { padding-inline: 8px; }
.nas-draggable { cursor: grab; user-select: none; }
.nas-draggable:active { cursor: grabbing; }
.nas-check { width: 13px; height: 13px; display: grid; place-items: center; flex-shrink: 0; border: 1px solid var(--border); border-radius: 3px; }
.nas-row.selected .nas-check { border-color: var(--blue); }
.nas-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.nas-row small { flex-shrink: 0; color: var(--muted); font-family: var(--font-mono); font-size: 10px; }
.nas-error, .nas-note, .nas-empty { margin: 0; padding: 8px 10px; color: var(--muted); font-size: 11px; overflow-wrap: anywhere; }
.nas-error { color: var(--danger); background: var(--error-bg); border-radius: var(--radius-control); }
.nas-empty p { margin: 7px 0 0; }
.nas-footer { display: flex; align-items: center; justify-content: space-between; gap: 6px; padding: 6px 8px; border-top: 1px solid var(--border-soft); background: var(--bg-surface); }
.nas-footer span { color: var(--muted); font-size: 10px; }
.nas-footer button { flex-shrink: 0; min-height: 24px; gap: 4px; padding: 2px 8px; font-size: 10.5px; }
.nas-drop-destination { position: absolute; z-index: 3; bottom: 8px; inset-inline: 8px; display: flex; align-items: center; gap: 6px; padding: 6px 8px; color: var(--blue); background: var(--bg-surface); border: 1px solid var(--blue); border-radius: var(--radius-control); font-size: 11px; pointer-events: none; }
.nas-drop-destination svg { flex-shrink: 0; }
.nas-drop-destination span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
