<script setup lang="ts">
import { computed } from 'vue'
import { Cpu, Thermometer, Zap, Fan, Gauge } from '@lucide/vue'
import type { GpuDevice } from '../../../shared/types'

const props = defineProps<{ device: GpuDevice }>()
const memoryPercent = computed(() => {
  const { memoryUsedMiB: used, memoryTotalMiB: total } = props.device
  return used !== null && total !== null && total > 0 ? Math.min(100, used / total * 100) : null
})
const memoryLevel = computed(() => memoryPercent.value === null ? '' : memoryPercent.value >= 95 ? 'err' : memoryPercent.value >= 88 ? 'warn' : '')
const tempLevel = computed(() => props.device.temperatureC === null ? '' : props.device.temperatureC >= 85 ? 'err' : props.device.temperatureC >= 75 ? 'warn' : '')
const percent = (value: number | null) => value === null ? '—' : `${Math.round(value)}%`
const metric = (value: number | null, unit: string) => value === null ? '—' : `${value.toLocaleString('en-US', { maximumFractionDigits: 1 })} ${unit}`
const memory = (value: number | null) => value === null ? '—' : (value / 1024).toLocaleString('en-US', { maximumFractionDigits: 1 })
</script>

<template>
  <article class="gpu-card" :aria-label="`GPU ${device.index} ${device.name}`">
    <header class="gpu-card-header">
      <div class="gpu-card-icon"><Cpu :size="17" /></div>
      <div class="gpu-card-identity"><span class="gpu-card-number mono">GPU {{ device.index }}</span><h3>{{ device.name }}</h3></div>
      <strong class="gpu-card-util mono">{{ percent(device.utilization) }}</strong>
    </header>
    <div class="gpu-card-load">
      <progress v-if="device.utilization !== null" :value="device.utilization" max="100" :aria-label="`GPU ${device.index} 利用率`" />
      <div v-else class="gpu-meter-unavailable" aria-hidden="true"></div>
    </div>
    <div class="gpu-card-memory" :class="memoryLevel">
      <div class="gpu-metric-heading"><span>显存占用</span><div class="gpu-memory-amount mono"><strong>{{ memory(device.memoryUsedMiB) }}</strong><span>/ {{ memory(device.memoryTotalMiB) }} GiB</span></div></div>
      <progress v-if="memoryPercent !== null" :value="memoryPercent" max="100" :aria-label="`GPU ${device.index} 显存占用`" />
      <div v-else class="gpu-meter-unavailable" aria-hidden="true"></div>
    </div>
    <dl class="gpu-card-stats">
      <div><dt><Thermometer :size="12" />温度</dt><dd class="mono" :class="tempLevel">{{ metric(device.temperatureC, '°C') }}</dd></div>
      <div><dt><Zap :size="12" />功耗</dt><dd class="mono">{{ metric(device.powerDrawW, 'W') }}<small>/ {{ metric(device.powerLimitW, 'W') }}</small></dd></div>
      <div><dt><Fan :size="12" />风扇</dt><dd class="mono">{{ percent(device.fanPercent) }}</dd></div>
      <div><dt><Gauge :size="12" />性能状态</dt><dd class="mono">{{ device.performanceState || '—' }}</dd></div>
    </dl>
  </article>
</template>
