<script setup lang="ts">
import { computed } from 'vue'
import { MIN_SPLIT_RATIO, MAX_SPLIT_RATIO, type SplitDirection } from '../terminal-layout'

const props = defineProps<{ direction: SplitDirection; ratio: number }>()
const emit = defineEmits<{ resize: [percent: number] }>()
const sideBySide = computed(() => props.direction === 'horizontal')
let pointerOffset = 0

function start(event: PointerEvent): void {
  if (event.button !== 0) return
  const divider = event.currentTarget as HTMLElement
  const rect = divider.getBoundingClientRect()
  pointerOffset = sideBySide.value ? event.clientX - rect.left : event.clientY - rect.top
  divider.setPointerCapture(event.pointerId)
  event.preventDefault()
}

function drag(event: PointerEvent): void {
  const divider = event.currentTarget as HTMLElement
  if (!divider.hasPointerCapture(event.pointerId) || !divider.parentElement) return
  const bounds = divider.parentElement.getBoundingClientRect(), rect = divider.getBoundingClientRect()
  const length = sideBySide.value ? bounds.width - rect.width : bounds.height - rect.height
  if (length <= 0) return
  const position = sideBySide.value ? event.clientX - bounds.left : event.clientY - bounds.top
  emit('resize', (position - pointerOffset) / length * 100)
}

function adjust(event: KeyboardEvent): void {
  const decrease = sideBySide.value ? 'ArrowLeft' : 'ArrowUp'
  const increase = sideBySide.value ? 'ArrowRight' : 'ArrowDown'
  if (event.key === 'Home') emit('resize', MIN_SPLIT_RATIO)
  else if (event.key === 'End') emit('resize', MAX_SPLIT_RATIO)
  else if (event.key === decrease) emit('resize', props.ratio - 5)
  else if (event.key === increase) emit('resize', props.ratio + 5)
  else return
  event.preventDefault()
}
</script>

<template>
  <div class="terminal-split-divider" role="separator" tabindex="0" aria-label="调整分屏比例"
    :aria-orientation="sideBySide ? 'vertical' : 'horizontal'" :aria-valuemin="MIN_SPLIT_RATIO" :aria-valuemax="MAX_SPLIT_RATIO"
    :aria-valuenow="Math.round(ratio)" :aria-valuetext="`${sideBySide ? '左侧' : '上方'}终端 ${Math.round(ratio)}%`"
    @pointerdown="start" @pointermove="drag" @keydown="adjust" @dblclick="emit('resize', 50)" />
</template>

<style scoped>
.terminal-split-divider { grid-area: divider; position: relative; touch-action: none; user-select: none; cursor: row-resize; }
.terminal-split-divider[aria-orientation='vertical'] { cursor: col-resize; }
.terminal-split-divider::before { content: ''; position: absolute; inset: 0; background: var(--border); }
.terminal-split-divider:hover::before, .terminal-split-divider:focus-visible::before { background: var(--blue); }
.terminal-split-divider:focus-visible { outline: 2px solid var(--blue); outline-offset: 1px; z-index: 1; }
</style>
