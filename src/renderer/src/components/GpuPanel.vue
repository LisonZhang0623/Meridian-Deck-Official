<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Cpu, X, RefreshCw, Pause, Play, AlertTriangle } from '@lucide/vue'
import { active, state, setLayout, attempt } from '../workbench'
import { useGpuMonitor } from '../useGpuMonitor'
import { MIN_GPU_REFRESH_SECONDS, MAX_GPU_REFRESH_SECONDS } from '../../../shared/settings'
import GpuCard from './GpuCard.vue'

defineEmits<{ close: [] }>()
const visible = computed(() => !state.settings.sidebarCollapsed && state.settings.sidebarPanel === 'gpu')
const connected = computed(() => active.value?.phase === 'connected')
const interval = ref(String(state.settings.gpuRefreshSeconds)), intervalError = ref('')
watch(() => state.settings.gpuRefreshSeconds, value => { interval.value = String(value) })
const { devices, collectedAt, warning, error, busy, paused, refresh } = useGpuMonitor(() => ({
  id: active.value?.id || '', identity: active.value?.connectionKey || '', connected: connected.value,
  visible: visible.value, interval: state.settings.gpuRefreshSeconds
}), window.sshTool)
const updated = computed(() => collectedAt.value ? new Date(collectedAt.value).toLocaleTimeString('zh-CN', { hour12: false }) : '')
const status = computed(() => !connected.value ? '等待连接' : error.value ? '刷新失败 · 已暂停' : busy.value ? '正在刷新…' : paused.value ? '已暂停' : `每 ${state.settings.gpuRefreshSeconds} 秒刷新`)
function applyInterval(): void {
  const seconds = Number(interval.value)
  if (!Number.isInteger(seconds) || seconds < MIN_GPU_REFRESH_SECONDS || seconds > MAX_GPU_REFRESH_SECONDS) {
    intervalError.value = '请输入 1–3600 的整数秒'; return
  }
  intervalError.value = ''
  if (seconds !== state.settings.gpuRefreshSeconds) void attempt(() => setLayout({ gpuRefreshSeconds: seconds }))
}
</script>

<template>
  <section id="sidebar-panel-gpu" v-show="state.settings.sidebarPanel === 'gpu'" class="gpu-panel" aria-label="显卡状态">
    <div class="section-heading">
      <span><Cpu :size="15" />显卡状态</span>
      <div class="sidebar-heading-actions">
        <button class="icon-button" title="立即刷新显卡状态" aria-label="立即刷新显卡状态" :disabled="!connected || busy" @click="refresh"><RefreshCw :size="14" :class="{ spin: busy }" /></button>
        <button class="icon-button" title="收起显卡面板" aria-label="收起显卡面板" @click="$emit('close')"><X :size="15" /></button>
      </div>
    </div>
    <div class="gpu-server" :title="active?.name">{{ active?.name || '尚未选择已连接的会话' }}</div>
    <form class="gpu-controls" @submit.prevent="applyInterval">
      <label for="gpu-refresh-seconds">刷新间隔</label>
      <input id="gpu-refresh-seconds" v-model="interval" type="number" :min="MIN_GPU_REFRESH_SECONDS" :max="MAX_GPU_REFRESH_SECONDS" step="1" :aria-invalid="!!intervalError" aria-describedby="gpu-interval-error" @change="applyInterval">
      <span>秒</span>
      <button type="button" :disabled="!connected" @click="paused = !paused"><Play v-if="paused" :size="13" /><Pause v-else :size="13" />{{ paused ? '开始刷新' : '暂停' }}</button>
    </form>
    <p v-if="intervalError" id="gpu-interval-error" class="gpu-validation" role="alert">{{ intervalError }}</p>
    <div class="gpu-status"><span>{{ status }}</span><span v-if="updated">上次更新 {{ updated }}</span></div>
    <div v-if="error" class="gpu-error" role="alert"><AlertTriangle :size="16" /><div><strong>无法更新显卡状态</strong><p>{{ error }}</p><button :disabled="!connected || busy" @click="paused = false; refresh()">重试</button></div></div>
    <div v-if="!connected" class="gpu-note">{{ active ? '当前会话未连接。连接成功后可查看服务器显卡。' : '请先连接一台服务器，再打开显卡状态。' }}<span v-if="collectedAt">下方为上次采集结果。</span></div>
    <div v-else-if="paused && collectedAt" class="gpu-note">自动刷新已暂停，下方为上次采集结果。</div>
    <div v-if="warning" class="gpu-note gpu-warning">{{ warning }}</div>
    <div class="gpu-cards" tabindex="0" aria-label="GPU 状态卡片" :aria-busy="busy">
      <GpuCard v-for="device in devices" :key="device.uuid" :device="device" />
      <div v-if="!devices.length" class="gpu-placeholder"><Cpu :size="27" /><p>{{ collectedAt ? '未检测到 NVIDIA GPU' : busy ? '正在读取服务器显卡状态…' : error ? '暂时无法读取显卡数据' : '等待获取显卡状态' }}</p></div>
    </div>
    <div class="gpu-footnote"><span>{{ collectedAt ? devices.length + ' 张 GPU · ' : '' }}— 表示指标不可用</span><span>收起后停止刷新</span></div>
  </section>
</template>
