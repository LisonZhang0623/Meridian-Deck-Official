<script setup lang="ts">
import { computed, ref } from 'vue'
import { Activity, MemoryStick, HardDrive, Network, X, RefreshCw, Pause, Play } from '@lucide/vue'
import { active, selectedProfile } from '../workbench'
import { useResources } from '../useResources'
import '../resources.css'

defineEmits<{ close: [] }>()
const interval = ref(2)
const { snapshot, rates, error, busy, paused, refresh } = useResources(() => ({ id: active.value?.id || '', identity: active.value?.connectionKey || '', connected: active.value?.phase === 'connected', interval: interval.value }), window.sshTool)
const connected = computed(() => active.value?.phase === 'connected')
const memoryUsed = computed(() => snapshot.value ? snapshot.value.memory.total - snapshot.value.memory.available : 0)
const memoryPercent = computed(() => snapshot.value && snapshot.value.memory.total > 0 ? memoryUsed.value / snapshot.value.memory.total * 100 : 0)
const disks = computed(() => snapshot.value ? snapshot.value.disks ?? [snapshot.value.disk] : [])
function level(percent: number | null): '' | 'warn' | 'err' {
  if (percent === null) return ''
  if (percent >= 90) return 'err'
  if (percent >= 80) return 'warn'
  return ''
}
function bytes(value: number): string { const gib = value / 1024 ** 3; return gib >= 1 ? `${gib.toFixed(1)} GiB` : `${(value / 1024 ** 2).toFixed(0)} MiB` }
function speed(value: number | null): string { if (value === null) return '—'; if (value >= 1024 ** 2) return (value / 1024 ** 2).toFixed(1) + ' MiB/s'; if (value >= 1024) return (value / 1024).toFixed(1) + ' KiB/s'; return Math.round(value) + ' B/s' }
function duration(seconds: number): string { return `${Math.floor(seconds / 86400)} 天 ${Math.floor(seconds % 86400 / 3600)} 小时` }
function changeInterval(event: Event): void { const value = Number((event.target as HTMLInputElement).value); if (Number.isInteger(value) && value >= 1 && value <= 60) interval.value = value; else (event.target as HTMLInputElement).value = String(interval.value) }
</script>
<template>
  <section id="sidebar-panel-resources" class="resources-panel" aria-label="服务器资源">
    <div class="section-heading">
      <span><Activity :size="15"/>服务器资源</span>
      <div class="sidebar-heading-actions">
        <button class="icon-button" :title="paused ? '恢复刷新' : '暂停刷新'" :aria-label="paused ? '恢复刷新' : '暂停刷新'" :disabled="!connected" @click="paused = !paused"><Play v-if="paused" :size="13"/><Pause v-else :size="13"/></button>
        <button class="icon-button" title="立即刷新资源" aria-label="立即刷新资源" :disabled="!connected || busy" @click="refresh"><RefreshCw :size="14" :class="{ spin: busy }"/></button>
        <button class="icon-button" title="收起资源面板" aria-label="收起资源面板" @click="$emit('close')"><X :size="15"/></button>
      </div>
    </div>
    <div class="side-context">
      <span :title="selectedProfile?.name">{{ selectedProfile?.name || '未选择服务器' }}</span>
      <label class="resource-interval">每 <input type="number" :value="interval" min="1" max="60" aria-label="资源刷新秒数" @change="changeInterval"> 秒{{ paused ? ' · 已暂停' : '' }}</label>
    </div>
    <div v-if="error" class="resource-error" role="alert">{{ error }}<button :disabled="busy || !connected" @click="paused = false; refresh()">重试</button></div>
    <div v-if="!snapshot" class="side-empty">{{ connected ? busy ? '正在读取服务器资源…' : '打开后采集 CPU、内存、磁盘与网络。' : '连接服务器后查看资源使用情况。' }}</div>
    <div v-else class="resource-cards">
      <article class="resource-card" :class="level(rates.cpu)">
        <header><Activity :size="15"/>CPU 利用率<strong class="mono">{{ rates.cpu === null ? '—' : rates.cpu.toFixed(1) + '%' }}</strong></header>
        <progress v-if="rates.cpu !== null" max="100" :value="rates.cpu" aria-label="CPU 利用率"></progress>
        <div v-else class="resource-empty-bar"></div>
        <small v-if="rates.cpu === null">等待下一次采样计算利用率</small>
        <div class="resource-meta mono"><span>负载 1/5/15m</span><span>{{ snapshot.load.map(n => n.toFixed(2)).join(' / ') }}</span></div>
      </article>
      <article class="resource-card" :class="level(memoryPercent)">
        <header><MemoryStick :size="15"/>内存<strong class="mono">{{ memoryPercent.toFixed(1) }}%</strong></header>
        <progress max="100" :value="memoryPercent" aria-label="内存利用率"></progress>
        <div class="resource-meta mono"><span>{{ bytes(memoryUsed) }} / {{ bytes(snapshot.memory.total) }}</span><span>可用 {{ bytes(snapshot.memory.available) }}</span></div>
      </article>
      <article class="resource-card">
        <header><HardDrive :size="15"/>存储挂载点</header>
        <div v-for="disk in disks" :key="disk.mount" class="resource-disk-row" :class="level(disk.total > 0 ? disk.used / disk.total * 100 : 0)">
          <div class="resource-meta mono">
            <span class="resource-mount" :title="disk.mount">{{ disk.mount === '/' ? '根分区 /' : disk.mount }}</span>
            <span><strong>{{ (disk.total > 0 ? disk.used / disk.total * 100 : 0).toFixed(0) }}%</strong> · {{ bytes(disk.used) }} / {{ bytes(disk.total) }}</span>
          </div>
          <progress max="100" :value="disk.total > 0 ? disk.used / disk.total * 100 : 0" :aria-label="`${disk.mount} 分区利用率`"></progress>
        </div>
      </article>
      <article class="resource-card">
        <header><Network :size="15"/>网络流量</header>
        <div v-for="net in rates.network" :key="net.name" class="resource-network mono">
          <strong>{{ net.name }}</strong>
          <span class="success">↓ {{ speed(net.received) }}</span>
          <span class="net-up">↑ {{ speed(net.sent) }}</span>
        </div>
        <small v-if="!rates.network.length">没有非回环网络接口</small>
      </article>
    </div>
    <div v-if="snapshot" class="resource-footnote"><span>运行 {{ duration(snapshot.uptime) }}</span><span class="mono">{{ new Date(snapshot.collectedAt).toLocaleTimeString('zh-CN', { hour12: false }) }}</span></div>
  </section>
</template>
