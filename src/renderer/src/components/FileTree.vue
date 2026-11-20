<script setup lang="ts">
import { ChevronRight, ChevronDown, Download, Folder, FolderOpen, FileCode, FileImage, FileText, Link, LoaderCircle } from '@lucide/vue'
import { attempt, enterDirectory, openFile, dragDownloads, dragKey, dragRemoteFile, requestDragDownload, type Workspace } from '../workbench'
import type { RemoteEntry } from '../../../shared/types'
import { inject } from 'vue'
import type { useFileSelection } from '../file-selection'
const selection = inject<ReturnType<typeof useFileSelection>>('file-selection')!
const props = defineProps<{ session: Workspace; path: string; depth?: number; nasDropTarget?: string; nasDropMode?: boolean }>()
const emit = defineEmits<{ context: [event: MouseEvent, entry: RemoteEntry]; dragToNas: [event: PointerEvent, entry: RemoteEntry] }>()
const api = window.sshTool
function localDragAvailable(entry: RemoteEntry): boolean {
  return props.session.phase === 'connected' && !props.session.sftpError && (entry.type === 'file' || entry.type === 'directory' && !!(api.prepareDragDirectory || api.downloadDirectory))
}
function dragHint(entry: RemoteEntry): string {
  if (props.nasDropMode && entry.type !== 'link') return '\n拖到 NAS 面板下载，右侧图标用于下载到本机。'
  if (!localDragAvailable(entry)) return ''
  return api.nativeDragDownload !== false ? '\n首次拖动会询问是否准备下载，随后可拖到资源管理器。' : '\n拖动后选择本机保存位置。'
}
function icon(entry: RemoteEntry) { if (entry.type === 'link' || entry.hardLink) return Link; if (/\.(png|jpe?g|gif|webp|bmp)$/i.test(entry.name)) return FileImage; if (/\.(py|js|ts|json|ya?ml|html|css|cpp|rs|go|sh|vue)$/i.test(entry.name)) return FileCode; return FileText }
function entryTitle(entry: RemoteEntry): string {
  const link = entry.type === 'link' ? `\n符号链接${entry.linkTarget ? ` → ${entry.linkTarget}` : ''}${entry.targetType === 'missing' ? '（目标不存在）' : ''}` : entry.hardLink ? '\n硬链接' : ''
  return entry.path + link + (entry.type === 'directory' ? '\n双击进入文件夹' : '') + dragHint(entry)
}
function clickEntry(event: MouseEvent, entry: RemoteEntry): void {
  if (event.detail > 1) return
  const toggle = event.ctrlKey || event.metaKey
  selection.select(props.path, entry.path, toggle, event.shiftKey)
  if (!toggle && !event.shiftKey) void attempt(() => openFile(props.session, entry))
}
function doubleClickEntry(entry: RemoteEntry): void {
  if (entry.type === 'directory' || entry.type === 'link' && entry.targetType === 'directory') void attempt(() => enterDirectory(props.session, entry.path))
}
</script>
<template>
  <ul class="file-tree" :aria-label="path">
    <li v-for="entry in session.tree[path] || []" :key="entry.path">
      <div class="remote-entry-row" :data-nas-directory="entry.type === 'directory' ? entry.path : undefined">
        <button type="button" class="file-row" :data-entry="entry.path" :data-parent="path" :aria-pressed="selection.paths.value.has(entry.path)" @keyup.tab="selection.select(path, entry.path)" :class="{ selected: selection.paths.value.has(entry.path) || nasDropTarget === entry.path, 'remote-nas-source': nasDropMode && entry.type !== 'link' }"
          :style="{ paddingLeft: ((depth || 0) * 16 + 8) + 'px' }" :title="entryTitle(entry)" :draggable="localDragAvailable(entry) && !nasDropMode"
          :aria-expanded="entry.type === 'directory' ? session.expanded.has(entry.path) : undefined"
          @pointerdown="nasDropMode && !$event.ctrlKey && !$event.metaKey && !$event.shiftKey && entry.type !== 'link' && emit('dragToNas', $event, entry)" @dragstart="dragRemoteFile($event, session, entry)"
          @click="clickEntry($event, entry)" @dblclick="!$event.ctrlKey && !$event.metaKey && !$event.shiftKey && doubleClickEntry(entry)" @contextmenu.prevent="emit('context', $event, entry)">
          <LoaderCircle v-if="session.loading.has(entry.path)" :size="13" class="spin"/><component :is="session.expanded.has(entry.path) ? ChevronDown : ChevronRight" v-else-if="entry.type === 'directory'" :size="13"/><span v-else class="tree-spacer"></span>
          <component :is="entry.type === 'directory' ? session.expanded.has(entry.path) ? FolderOpen : Folder : icon(entry)" :size="15" :class="{ 'folder-icon': entry.type === 'directory', danger: entry.targetType === 'missing' }"/>
          <span class="file-entry-name">{{ entry.name }}<span v-if="entry.linkTarget" class="muted"> → {{ entry.linkTarget }}</span></span>
          <small v-if="entry.targetType === 'missing'" class="link-label danger">目标不存在</small><small v-else-if="entry.hardLink" class="link-label muted">硬链接</small>
          <small v-if="dragDownloads[dragKey(session.id, entry)]" class="drag-file-status" :class="dragDownloads[dragKey(session.id, entry)].status">{{ dragDownloads[dragKey(session.id, entry)].status === 'preparing' ? '准备中' : dragDownloads[dragKey(session.id, entry)].status === 'ready' ? '可拖出' : '准备失败' }}</small>
        </button>
        <button v-if="nasDropMode && localDragAvailable(entry)" type="button" class="icon-button local-drag-handle" draggable="true" :aria-label="`下载到本机 ${entry.name}`" title="拖到本机或点击准备下载"
          @dragstart="dragRemoteFile($event, session, entry)" @click="attempt(() => requestDragDownload(session, entry))"><Download :size="13"/></button>
      </div>
      <FileTree v-if="entry.type === 'directory' && session.expanded.has(entry.path)" :session="session" :path="entry.path" :depth="(depth || 0) + 1" :nas-drop-target="nasDropTarget" :nas-drop-mode="nasDropMode" @context="(event, child) => emit('context', event, child)" @drag-to-nas="(event, child) => emit('dragToNas', event, child)"/>
    </li>
    <li v-if="session.tree[path]?.length === 0" class="empty-folder" :style="{ paddingLeft: ((depth || 0) * 16 + 28) + 'px' }">空文件夹</li>
  </ul>
</template>

<style scoped>
.remote-entry-row { display: flex; align-items: stretch; }
.remote-entry-row .file-row { flex: 1; min-width: 0; }
.link-label { flex-shrink: 0; font-size: 10px; }
.remote-nas-source, .local-drag-handle { cursor: grab; user-select: none; }
.remote-nas-source:active, .local-drag-handle:active { cursor: grabbing; }
.local-drag-handle { flex: 0 0 28px; padding: 4px; }
</style>
