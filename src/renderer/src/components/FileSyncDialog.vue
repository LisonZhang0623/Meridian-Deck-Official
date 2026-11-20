<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { X } from '@lucide/vue'
import type { DirectoryComparison } from '../../../shared/types'
import { confirmAction } from '../confirmation'
import { loadDirectory, notify, state, unwrap, type Workspace } from '../workbench'

const props = defineProps<{ session: Workspace; directory: string }>()
const emit = defineEmits<{ close: [] }>()
const modal = ref<HTMLDialogElement>()
const comparison = ref<DirectoryComparison>()
const busy = ref(false)
const error = ref('')
const sessionId = props.session.id
const ready = computed(() => props.session.id === sessionId && props.session.phase === 'connected' && state.sessions.some(session => session.id === sessionId))
const conflict = computed(() => comparison.value?.changes.some(change => change.status === 'conflict'))
const uploads = computed(() => comparison.value?.changes.filter(change => change.status === 'different' || change.status === 'local-only').length || 0)
const downloads = computed(() => comparison.value?.changes.filter(change => change.status === 'different' || change.status === 'remote-only').length || 0)
const labels = { 'local-only': '仅本地', 'remote-only': '仅远程', different: '内容不同', conflict: '冲突' }
onMounted(() => modal.value?.showModal())

function close(): void {
  if (!busy.value) emit('close')
}

async function compare(): Promise<void> {
  const { compareDirectory } = window.sshTool
  if (!compareDirectory || !ready.value || busy.value) return
  busy.value = true
  error.value = ''
  comparison.value = undefined
  try {
    const result = unwrap(await compareDirectory(sessionId, props.directory))
    if (ready.value) comparison.value = result || undefined
  } catch (cause) {
    error.value = (cause as Error).message
  } finally {
    busy.value = false
  }
}

async function sync(direction: 'upload' | 'download'): Promise<void> {
  const { syncDirectory } = window.sshTool
  const plan = comparison.value
  if (!syncDirectory || !plan || !ready.value || busy.value || conflict.value) return
  const destination = direction === 'upload' ? '远程目录' : '本地目录'
  busy.value = true
  try {
    if (!await confirmAction({
      title: `同步到${destination}？`,
      message: '同名文件将按所选方向覆盖，目标独有的文件会保留。',
      action: '同步',
      danger: true
    }) || !ready.value || comparison.value !== plan) return
    error.value = ''
    comparison.value = undefined
    const tasks = unwrap(await syncDirectory(sessionId, plan.token, direction))
    if (tasks > 0) state.transferOpen = true
    else notify('目录已同步')
    if (direction === 'upload' && ready.value) await loadDirectory(props.session, props.directory, true)
    emit('close')
  } catch (cause) {
    error.value = (cause as Error).message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <dialog ref="modal" class="sync-dialog" @cancel.prevent="close">
    <header class="dialog-header">
      <div><h2>目录比较与同步</h2><p>远程目录 {{ directory }}</p></div>
      <button class="icon-button" aria-label="关闭" :disabled="busy" @click="close"><X :size="18"/></button>
    </header>
    <div class="dialog-body">
      <p v-if="!ready" class="error-box">连接已变化，请关闭后重新打开。</p>
      <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      <button :disabled="busy || !ready" @click="compare">{{ busy ? '处理中…' : '选择本地目录并比较' }}</button>
      <template v-if="comparison">
        <p class="muted">本地目录 {{ comparison.localPath }}</p>
        <p v-if="!comparison.changes.length">两个目录的内容相同。</p>
        <template v-else>
          <p v-if="conflict" class="error-box">请处理类型、名称或符号链接冲突后重新比较。</p>
          <ul class="sync-results" aria-label="目录差异">
            <li v-for="change in comparison.changes" :key="change.path">
              <span :title="change.path">{{ change.path }}</span>
              <strong :class="{ danger: change.status === 'conflict' }">{{ labels[change.status] }}</strong>
            </li>
          </ul>
        </template>
      </template>
    </div>
    <footer class="dialog-footer">
      <button :disabled="busy" @click="close">关闭</button>
      <button :disabled="busy || !ready || !!conflict || !downloads" @click="sync('download')">远程 → 本地（{{ downloads }}）</button>
      <button class="primary" :disabled="busy || !ready || !!conflict || !uploads" @click="sync('upload')">本地 → 远程（{{ uploads }}）</button>
    </footer>
  </dialog>
</template>

<style scoped>
.sync-dialog { width: min(46rem, 90vw); }
.dialog-header p, .dialog-body p { overflow-wrap: anywhere; }
.sync-results { list-style: none; padding: 0; margin: 0; max-height: min(34vh, 18rem); overflow-y: auto; }
.sync-results li { display: flex; justify-content: space-between; gap: 1rem; padding: .35rem .4rem; border-bottom: 1px solid var(--border); }
.sync-results li span { overflow-wrap: anywhere; }
.sync-results li strong { flex: 0 0 auto; }
</style>
