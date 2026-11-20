<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Plus, X } from '@lucide/vue'
import type { Preview } from '../../../shared/types'
import { environmentDiff, parseEnvironment, updateEnvironment, type Variable } from '../bash-environment'
import { loadDirectory, notify, unwrap, workspaceRequest, type Workspace } from '../workbench'

const props = defineProps<{ session: Workspace }>()
const emit = defineEmits<{ close: [] }>()
const modal = ref<HTMLDialogElement>(), rows = ref<Variable[]>([]), original = ref<Variable[]>([])
const home = ref(''), file = ref<Preview>(), ready = ref(false), busy = ref(false), error = ref(''), review = ref(false)
const request = workspaceRequest(props.session)
const current = () => request.current() && props.session.phase === 'connected'
const path = computed(() => home.value.replace(/\/$/, '') + '/.bashrc')
const changes = computed(() => environmentDiff(original.value, rows.value))
const api = window.sshTool

async function load() {
  busy.value = true; ready.value = false; error.value = ''
  try {
    const listing = unwrap(await api.listDirectory(request.id, '.'))
    home.value = listing.path
    const exists = listing.entries.some(entry => entry.name === '.bashrc')
    file.value = exists ? unwrap(await api.preview(request.id, path.value)) : undefined
    if (file.value && (file.value.kind !== 'text' || file.value.page)) throw new Error('.bashrc 需要是可编辑的文本文件')
    original.value = parseEnvironment(file.value?.content || '').rows
    rows.value = original.value.map(row => ({ ...row }))
    ready.value = true
  } catch (cause) { error.value = (cause as Error).message }
  finally { busy.value = false }
}
async function submit() {
  if (busy.value || !ready.value || !current()) return
  error.value = ''
  try {
    const content = updateEnvironment(file.value?.content || '', rows.value)
    if (!review.value) { review.value = true; return }
    busy.value = true
    const doc = props.session.documents.find(item => item.path === path.value)
    if (doc && (doc.busy || doc.content !== doc.saved)) throw new Error('请先处理文本编辑器中的 .bashrc 修改')
    if (!file.value) {
      // Exclusive creation preserves a file added by another client after the preview.
      ready.value = false
      unwrap(await api.createEntry(request.id, home.value, '.bashrc', false))
      file.value = unwrap(await api.preview(request.id, path.value))
      if (file.value.content !== '') throw new Error('.bashrc 已发生变化，请重新读取')
    }
    ready.value = true
    if (!current()) throw new Error('连接已更改，请重新打开环境变量')
    if (doc) doc.busy = true
    let saved: Preview
    try { saved = unwrap(await api.saveText(request.id, path.value, content, file.value.version)) }
    finally { if (doc && current()) doc.busy = false }
    if (!current()) return
    if (doc && !doc.busy && doc.content === doc.saved) {
      doc.preview = saved; doc.content = saved.content; doc.saved = saved.content; doc.error = ''; doc.conflict = false
    }
    await loadDirectory(props.session, home.value, true)
    notify('环境变量已保存，新打开的 Bash 会话生效')
    emit('close')
  } catch (cause) {
    error.value = (cause as Error).message
    if ((cause as { code?: string }).code === 'CONFLICT') ready.value = false
  } finally { busy.value = false }
}
onMounted(() => { modal.value?.showModal(); void load() })
</script>
<template>
  <dialog ref="modal" aria-labelledby="environment-title" @cancel.prevent="!busy && emit('close')">
    <form @submit.prevent="submit">
      <header class="dialog-header"><h2 id="environment-title">Bash 环境变量</h2><button type="button" class="icon-button" :disabled="busy" aria-label="关闭" @click="emit('close')"><X :size="16"/></button></header>
      <div class="dialog-body">
        <small v-if="home" class="muted">{{ path }}{{ ready && !file ? '（保存时创建）' : '' }}</small>
        <template v-if="review">
          <pre class="environment-diff">{{ changes.join('\n') }}</pre>
        </template>
        <fieldset v-else-if="ready" :disabled="busy" class="environment-fields">
          <div v-for="(row, index) in rows" :key="index" class="environment-row">
            <input v-model="row.name" aria-label="变量名" placeholder="变量名" spellcheck="false" autocomplete="off">
            <input v-model="row.value" aria-label="变量值" placeholder="值" spellcheck="false" autocomplete="off">
            <button type="button" class="icon-button" aria-label="移除变量" @click="rows.splice(index, 1)"><X :size="15"/></button>
          </div>
          <button type="button" @click="rows.push({ name: '', value: '' })"><Plus :size="14"/>添加变量</button>
        </fieldset>
        <p v-if="error" role="alert" class="error-box">{{ error }}</p>
      </div>
      <footer class="dialog-footer">
        <button v-if="error" type="button" :disabled="busy" @click="review = false; load()">重新读取</button>
        <button type="button" :disabled="busy" @click="review ? review = false : emit('close')">{{ review ? '返回编辑' : '取消' }}</button>
        <button type="submit" class="primary" :disabled="!ready || busy || !changes.length || !current()">{{ review ? '保存到 .bashrc' : '查看更改' }}</button>
      </footer>
    </form>
  </dialog>
</template>
<style scoped>
dialog { width: min(620px, 92vw); }
.environment-fields { border: 0; margin: 0; padding: 0; display: grid; gap: 8px; }
.environment-fields > button { justify-self: start; }
.environment-row { display: grid; grid-template-columns: minmax(80px, 1fr) minmax(100px, 2fr) auto; gap: 6px; }
.environment-diff { white-space: pre-wrap; overflow-wrap: anywhere; user-select: text; }
</style>
