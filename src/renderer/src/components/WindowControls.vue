<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { Copy, Minus, Square, X } from '@lucide/vue'
import { attempt } from '../workbench'

const props = defineProps<{ nativeWindow: typeof import('@wailsio/runtime').Window }>()
const maximised = ref(false)
let timer: number | undefined
async function syncState() { maximised.value = await props.nativeWindow.IsMaximised() }
function resized() {
  window.clearTimeout(timer)
  timer = window.setTimeout(() => { void attempt(syncState) }, 80)
}
onMounted(() => {
  void attempt(syncState)
  window.addEventListener('resize', resized)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', resized)
  window.clearTimeout(timer)
})
</script>

<template>
  <div class="window-controls" role="group" aria-label="窗口控制">
    <button type="button" title="最小化" aria-label="最小化" @click="attempt(() => nativeWindow.Minimise())"><Minus :size="14"/></button>
    <button type="button" :title="maximised ? '还原' : '最大化'" :aria-label="maximised ? '还原' : '最大化'" @click="attempt(() => nativeWindow.ToggleMaximise())"><component :is="maximised ? Copy : Square" :size="12"/></button>
    <button type="button" class="close-window" title="关闭窗口" aria-label="关闭窗口" @click="attempt(() => nativeWindow.Close())"><X :size="15"/></button>
  </div>
</template>

<style scoped>
.window-controls { display: flex; align-self: stretch; flex-shrink: 0; }
.window-controls button { width: 42px; padding: 0; border: 0; border-radius: 0; background: transparent; color: var(--text); }
.window-controls button:hover { background: var(--bg-surface-2); }
.window-controls button:focus-visible { outline-offset: -3px; }
.window-controls .close-window:hover { background: var(--error-bg); color: var(--danger); }
</style>
