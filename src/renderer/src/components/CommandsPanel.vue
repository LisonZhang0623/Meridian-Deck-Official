<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { Bookmark, Check, Pencil, Play, Plus, Search, Trash2, X } from '@lucide/vue'
import type { CommandSnippet } from '../../../shared/v2'
import { connectionIdentity } from '../../../shared/connection'
import { active, selectedProfile, state, notify, sendTerminalInput, unwrap } from '../workbench'
import { confirmAction } from '../confirmation'
import '../commands.css'

defineEmits<{ close: [] }>()
const api = window.sshTool
const commands = ref<CommandSnippet[]>([]), query = ref(''), scope = ref('applicable'), selectedId = ref('')
const loading = ref(false), busy = ref(false), error = ref(''), editing = ref(false)
const form = reactive<CommandSnippet>({ id: '', name: '', group: '', profileId: '', command: '', description: '' })
const parameters = reactive<Record<string, string>>(Object.create(null)), previewText = ref('')
let revision = 0, request = 0, disposed = false
let previewTarget: { sessionId: string; identity: string; revision: number } | undefined
const selected = computed(() => commands.value.find(command => command.id === selectedId.value))
const parameterNames = computed(() => [...new Set([...(selected.value?.command || '').matchAll(/\{\{([A-Za-z_][A-Za-z0-9_]{0,31})\}\}/g)].map(match => match[1]))])
const filtered = computed(() => {
  const search = query.value.trim().toLowerCase()
  return commands.value.filter(command => (scope.value === 'all' || !command.profileId || command.profileId === selectedProfile.value?.id)
    && `${command.name} ${command.group} ${command.description} ${command.command} ${profileName(command.profileId)}`.toLowerCase().includes(search))
})
const groups = computed(() => [...new Set(filtered.value.map(command => command.group || '未分组'))].map(name => ({ name, commands: filtered.value.filter(command => (command.group || '未分组') === name) })))
function profileName(profileId: string): string { return profileId ? state.profiles.find(profile => profile.id === profileId)?.name || '服务器已删除' : '全局' }
const canExecute = computed(() => {
  const session = active.value, command = selected.value
  const profile = state.profiles.find(item => item.id === session?.profileId)
  return !!previewText.value && !!previewTarget && !!command && !!session && session.phase === 'connected'
    && session.id === previewTarget.sessionId && session.connectionKey === previewTarget.identity && revision === previewTarget.revision
    && !!profile && session.connectionKey === connectionIdentity(profile) && (!command.profileId || command.profileId === session.profileId)
})
watch(selected, command => {
  for (const name of Object.keys(parameters)) delete parameters[name]
  for (const match of (command?.command || '').matchAll(/\{\{([A-Za-z_][A-Za-z0-9_]{0,31})\}\}/g)) parameters[match[1]] = ''
}, { flush: 'sync' })
watch(() => JSON.stringify([active.value?.id, active.value?.phase, active.value?.connectionKey, selected.value, parameters]), () => {
  revision++; previewText.value = ''; previewTarget = undefined
}, { flush: 'sync' })
async function load(): Promise<void> {
  loading.value = true; error.value = ''
  try { const items = unwrap(await api.listCommands()); if (!disposed) commands.value = items }
  catch (cause) { if (!disposed) error.value = (cause as Error).message }
  finally { loading.value = false }
}
function edit(command?: CommandSnippet): void {
  Object.assign(form, command || { id: '', name: '', group: '', profileId: '', command: '', description: '' })
  editing.value = true; error.value = ''
}
async function save(): Promise<void> {
  if (busy.value) return
  if (form.profileId && !state.profiles.some(profile => profile.id === form.profileId)) { error.value = '请选择存在的服务器或全局范围'; return }
  busy.value = true; error.value = ''
  try {
    const saved = unwrap(await api.saveCommand({ ...form }))
    const index = commands.value.findIndex(command => command.id === saved.id)
    if (index < 0) commands.value.push(saved); else commands.value[index] = saved
    selectedId.value = saved.id; editing.value = false
  } catch (cause) { error.value = (cause as Error).message }
  finally { busy.value = false }
}
async function remove(command: CommandSnippet): Promise<void> {
  if (busy.value || !await confirmAction({ title: '删除命令收藏？', message: command.name, action: '删除', danger: true })) return
  if (disposed || busy.value || !commands.value.some(item => item.id === command.id)) return
  busy.value = true; error.value = ''
  try { unwrap(await api.deleteCommand(command.id)); commands.value = commands.value.filter(item => item.id !== command.id) }
  catch (cause) { error.value = (cause as Error).message }
  finally { busy.value = false }
}
async function preview(): Promise<void> {
  const command = selected.value; if (!command || busy.value) return
  const token = ++request, expected = revision, session = active.value
  const target = session?.phase === 'connected' ? { sessionId: session.id, identity: session.connectionKey, revision: expected } : undefined
  busy.value = true; error.value = ''; previewText.value = ''; previewTarget = undefined
  try {
    const output = unwrap(await api.renderCommand(command.id, { ...parameters }))
    if (disposed || token !== request || expected !== revision) return
    previewText.value = output; previewTarget = target
  } catch (cause) { if (!disposed && token === request) error.value = (cause as Error).message }
  finally { if (token === request) busy.value = false }
}
function execute(): void {
  if (!canExecute.value || busy.value) return
  const session = active.value!
  sendTerminalInput(session, previewText.value.replace(/\r?\n/g, '\r') + '\r')
  session.activeDocument = 'terminal'
  previewText.value = ''; previewTarget = undefined
  notify(`命令已发送到“${session.name}”的当前终端`)
}
onMounted(() => { void load() })
onBeforeUnmount(() => { disposed = true; request++; previewTarget = undefined })
</script>

<template>
  <section id="sidebar-panel-commands" class="commands-panel" aria-label="常用命令">
    <div class="section-heading"><span><Bookmark :size="15"/>常用命令</span><div class="sidebar-heading-actions"><button class="icon-button" title="新增命令收藏" aria-label="新增命令收藏" :disabled="busy" @click="edit()"><Plus :size="16"/></button><button class="icon-button" title="收起常用命令" aria-label="收起常用命令" @click="$emit('close')"><X :size="15"/></button></div></div>
    <div v-if="editing" class="command-form-scroll">
      <form class="command-form" @submit.prevent="save">
        <h3>{{ form.id ? '编辑命令' : '新增命令' }}</h3>
        <label>名称<input v-model="form.name" required maxlength="80" placeholder="例如：查看训练日志"></label>
        <div class="command-form-pair"><label>分组<input v-model="form.group" maxlength="80" placeholder="例如：训练"></label><label>适用范围<select v-model="form.profileId"><option value="">全局</option><option v-for="profile in state.profiles" :key="profile.id" :value="profile.id">{{ profile.name }}</option></select></label></div>
        <label>命令内容<textarea v-model="form.command" required rows="7" maxlength="16384" spellcheck="false" placeholder="tail -n {{lines}} -- {{path}}"></textarea></label>
        <p class="command-help">参数写成 <code v-pre>{{name}}</code>，必须是独立、未加引号的参数。</p>
        <label>说明<textarea v-model="form.description" rows="2" maxlength="500" placeholder="用途或参数说明（可选）"></textarea></label>
        <p class="command-help">收藏保存在本机，请勿写入密码或访问令牌。</p>
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
        <div class="command-form-actions"><button type="button" :disabled="busy" @click="editing = false; error = ''">取消</button><button type="submit" class="primary" :disabled="busy"><Check :size="14"/>{{ busy ? '保存中…' : '保存命令' }}</button></div>
      </form>
    </div>
    <template v-else>
      <div class="command-scope segmented"><button :aria-pressed="scope === 'applicable'" :class="{ active: scope === 'applicable' }" @click="scope = 'applicable'">{{ selectedProfile ? '当前与全局' : '全局命令' }}</button><button :aria-pressed="scope === 'all'" :class="{ active: scope === 'all' }" @click="scope = 'all'">全部收藏</button></div>
      <div class="server-search"><Search :size="14"/><input v-model="query" aria-label="搜索命令" placeholder="搜索名称、分组或命令"></div>
      <div class="command-list">
        <p v-if="loading" class="side-empty">正在读取命令…</p>
        <div v-for="group in groups" :key="group.name"><div class="group-title">{{ group.name }}</div><div v-for="command in group.commands" :key="command.id" class="command-item hover-row" :class="{ selected: selectedId === command.id }"><button class="command-select" @click="selectedId = selectedId === command.id ? '' : command.id"><strong>{{ command.name }}</strong><small>{{ profileName(command.profileId) }}</small></button><div class="command-item-actions hover-actions"><button class="icon-button" :title="'编辑 ' + command.name" :aria-label="'编辑 ' + command.name" :disabled="busy" @click="edit(command)"><Pencil :size="13"/></button><button class="icon-button" :title="'删除 ' + command.name" :aria-label="'删除 ' + command.name" :disabled="busy" @click="remove(command)"><Trash2 :size="13"/></button></div></div></div>
        <div v-if="!loading && !filtered.length" class="side-empty">{{ query ? '未找到匹配的命令。' : '还没有适用的命令，点击 + 添加，或查看全部收藏。' }}</div>
      </div>
      <div v-if="selected" class="command-runner">
        <div class="command-run-heading"><strong>{{ selected.name }}</strong><small>{{ profileName(selected.profileId) }}</small></div>
        <p v-if="selected.description" class="command-description">{{ selected.description }}</p>
        <pre class="command-source">{{ selected.command }}</pre>
        <label v-for="name in parameterNames" :key="name" class="command-parameter">{{ name }}<input v-model="parameters[name]" :aria-label="'命令参数 ' + name" :disabled="busy" maxlength="4096" spellcheck="false" placeholder="参数值"></label>
        <button :disabled="busy" @click="preview">{{ busy ? '生成中…' : '生成执行预览' }}</button>
        <template v-if="previewText"><div class="command-preview-label">执行预览 · {{ active?.name || '未连接服务器' }}</div><pre class="command-preview">{{ previewText }}</pre><button class="primary" :disabled="!canExecute || busy" @click="execute"><Play :size="14"/>发送到当前终端执行</button><p v-if="!canExecute" class="command-help">连接适用的服务器后，重新生成预览即可执行。</p></template>
      </div>
      <div v-if="error" class="command-error"><p class="error-box" role="alert">{{ error }}</p><button v-if="!commands.length" @click="load">重新读取</button></div>
      <p class="command-panel-note">收藏可离线管理；命令仅在点击执行后发送到当前终端。</p>
    </template>
  </section>
</template>
