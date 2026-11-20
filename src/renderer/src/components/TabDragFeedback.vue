<script setup lang="ts">
import { computed } from 'vue'
import { AppWindow, PanelTop } from '@lucide/vue'
import { tabDrag } from '../document-tab-drag'

const placement = computed(() => {
  const p = tabDrag.value
  if (!p) return {}
  return {
    ...(p.x + 240 > window.innerWidth ? { right: window.innerWidth - p.x + 10 + 'px' } : { left: p.x + 10 + 'px' }),
    ...(p.y + 54 > window.innerHeight ? { bottom: window.innerHeight - p.y + 10 + 'px' } : { top: p.y + 12 + 'px' }),
  }
})
</script>

<template>
  <div v-if="tabDrag" class="pointer-drag-label tab-drag-feedback" :class="{ 'drop-ready': tabDrag.ready }" :style="placement" aria-hidden="true">
    <component :is="tabDrag.action === 'detach' ? AppWindow : PanelTop" :size="15" />
    <div><strong>{{ tabDrag.name }}</strong><small>{{ tabDrag.action === 'detach' ? (tabDrag.ready ? '松开以打开独立窗口' : '拖离标签栏') : (tabDrag.ready ? '松开以收回窗口' : '拖回工作台标签栏') }}</small></div>
  </div>
</template>

<style scoped>
.tab-drag-feedback { max-width: min(220px, 75vw); box-shadow: 0 4px 14px var(--shadow); animation: tab-lift 120ms ease-out; transition: color 100ms, border-color 100ms; }
.tab-drag-feedback > div { min-width: 0; }
.tab-drag-feedback strong, .tab-drag-feedback small { display: block; }
.tab-drag-feedback strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tab-drag-feedback small { font-size: 10px; }
@keyframes tab-lift { from { opacity: 0; transform: translateY(3px) scale(.96); } to { opacity: 1; transform: none; } }
@media (prefers-reduced-motion: reduce) { .tab-drag-feedback { animation: none; transition: none; } }
</style>
