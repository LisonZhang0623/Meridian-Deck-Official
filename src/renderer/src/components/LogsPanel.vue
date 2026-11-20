<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ScrollText, X, Play, Pause, Search, Eraser, ArrowDown } from '@lucide/vue'
import { active } from '../workbench'
import { logTargets } from '../logTargets'
import { useLogMonitor } from '../useLogMonitor'
import '../logs.css'

defineEmits<{ close: [] }>()
const path = ref(''), initialLines = ref(200), query = ref(''), errorsOnly = ref(false), autoScroll = ref(true)
const formExpanded = ref(false), pathInput = ref<HTMLInputElement>()
const trackedLines = ref(initialLines.value)
const output = ref<HTMLElement>()
const connected = computed(() => active.value?.phase === 'connected')
const monitor = useLogMonitor(() => ({ id: active.value?.id || '', identity: active.value?.connectionKey || '', connected: connected.value }), window.sshTool)
const { rows, partial, running, starting, error, status, dropped } = monitor
const isError = (line: string) => /\b(error|fatal|exception|traceback|failed|failure|panic)\b|错误|异常|失败/i.test(line)
const allLines = computed(() => [...rows.value, ...(partial.value ? [partial.value] : [])])
const errorCount = computed(() => allLines.value.filter(isError).length)
const filtered = computed(() => allLines.value.filter(line => (!errorsOnly.value || isError(line)) && (!query.value || line.toLocaleLowerCase().includes(query.value.toLocaleLowerCase()))))
const compactActive = computed(() => (running.value || starting.value) && !formExpanded.value)
const shortPath = computed(() => {
  const parts = path.value.split('/').filter(Boolean)
  return parts.length > 2 ? '…/' + parts.slice(-2).join('/') : path.value
})
watch(() => [active.value?.id, active.value?.id ? logTargets[active.value.id] : ''], () => {
  const nextPath = active.value?.id ? logTargets[active.value.id] || '' : ''
  if (path.value !== nextPath && (running.value || starting.value)) monitor.stop()
  path.value = nextPath
  formExpanded.value = false
}, { immediate: true })
watch(() => [rows.value, partial.value, query.value, errorsOnly.value], async () => {
  if (autoScroll.value) { await nextTick(); if (output.value) output.value.scrollTop = output.value.scrollHeight }
})
async function changeFile(): Promise<void> {
  path.value = active.value ? logTargets[active.value.id] || '' : ''
  initialLines.value = trackedLines.value
  formExpanded.value = !formExpanded.value
  if (formExpanded.value) {
    await nextTick()
    pathInput.value?.focus()
    pathInput.value?.select()
  }
}
function begin(): void {
  const id = active.value?.id
  if (!id || !connected.value) return
  trackedLines.value = Number(initialLines.value)
  logTargets[id] = path.value
  formExpanded.value = false
  void monitor.start(path.value, Number(initialLines.value))
}
function bottom(): void { if (output.value) output.value.scrollTop = output.value.scrollHeight }
</script>

<template>
  <section id="sidebar-panel-logs" class="logs-panel" aria-label="日志追踪">
    <div class="section-heading">
      <span><ScrollText :size="15"/>日志追踪</span>
      <div class="sidebar-heading-actions">
        <button v-if="running" type="button" class="icon-button logs-toggle-form" :aria-expanded="formExpanded" @click="changeFile">{{ formExpanded ? '取消更换' : '更换文件' }}</button>
        <button class="icon-button" aria-label="收起日志面板" title="收起日志面板" @click="$emit('close')"><X :size="15"/></button>
      </div>
    </div>
    <div class="side-context">
      <span :title="active?.name">{{ active?.name || '尚未选择服务器' }}</span>
      <span role="status">{{ connected ? status : '等待连接' }}</span>
    </div>
    <div v-if="compactActive" class="logs-active-capsule">
      <span class="connection-dot" data-phase="connected" aria-hidden="true"></span>
      <span class="logs-active-path mono" :title="path">{{ shortPath }}</span>
      <button type="button" class="icon-button amber" title="暂停追踪" aria-label="暂停追踪" @click="monitor.stop"><Pause :size="13"/></button>
    </div>
    <form v-else class="logs-form" @submit.prevent="begin">
      <label>远程日志文件<input ref="pathInput" v-model="path" aria-label="远程日志文件" placeholder="/var/log/app.log" spellcheck="false" :disabled="starting" required></label>
      <div class="logs-start-row"><label>末尾行数<input v-model.number="initialLines" aria-label="初始日志行数" type="number" min="0" max="2000" step="1" :disabled="starting" required></label><button type="submit" class="primary" :disabled="starting || !connected || !path.trim() || running && path === logTargets[active!.id] && initialLines === trackedLines"><Play :size="13"/>{{ running ? '切换追踪' : rows.length || partial ? '重新追踪' : '开始追踪' }}</button></div>
    </form>
    <p v-if="error" class="logs-error" role="alert">{{ error }}</p>
    <div class="logs-filter-bar">
      <div class="logs-search"><Search :size="13"/><input v-model="query" aria-label="过滤日志" placeholder="过滤已接收日志"></div>
      <button type="button" class="logs-err-pill" :class="{ active: errorsOnly }" :aria-pressed="errorsOnly" @click="errorsOnly = !errorsOnly">仅错误{{ errorCount ? ` (${errorCount})` : '' }}</button>
    </div>
    <div class="logs-options">
      <label><input v-model="autoScroll" type="checkbox">自动滚动</label>
      <div class="logs-options-actions">
        <button class="icon-button" title="清空已接收日志" aria-label="清空已接收日志" @click="monitor.clear"><Eraser :size="13"/></button>
        <button class="icon-button" title="滚动到底部" aria-label="滚动到底部" @click="bottom"><ArrowDown :size="13"/></button>
      </div>
    </div>
    <div ref="output" class="logs-output" tabindex="0" aria-label="远程日志内容" :aria-busy="starting">
      <div v-for="(line, index) in filtered" :key="index" class="logs-line" :class="{ 'logs-line-error': isError(line) }"><span class="logs-line-number" aria-hidden="true">{{ index + 1 }}</span><span>{{ line || ' ' }}</span></div>
      <p v-if="!filtered.length" class="logs-placeholder">{{ query || errorsOnly ? '没有匹配的日志行' : running ? '等待文件写入新日志…' : '输入日志文件路径并开始追踪' }}</p>
    </div>
    <div class="logs-footnote"><span>{{ filtered.length }} 行<span v-if="dropped"> · 较早内容已截断</span></span><span>tail -F · 最多 2000 行</span></div>
  </section>
</template>
