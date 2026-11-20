<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { MAX_SIDEBAR_WIDTH, MIN_SIDEBAR_WIDTH } from '../../../shared/settings'

const props = defineProps<{ modelValue?: number; side: 'left' | 'right' }>()
const emit = defineEmits<{ 'update:modelValue': [width: number]; commit: [width: number] }>()
const handle = ref<HTMLElement>()
const actualWidth = ref(280), maxWidth = ref(MAX_SIDEBAR_WIDTH)
let observer: ResizeObserver | undefined
let drag: { pointerId: number; x: number; scale: number; width: number; initial: number; current: number } | undefined
const dragging = ref(false)

function sidebar(): HTMLElement | null { return document.getElementById('workbench-sidebar') }

function measure(): void {
  const panel = sidebar(), body = handle.value?.parentElement
  if (!panel || !body) return
  const rail = body.querySelector<HTMLElement>('.sidebar-rail')
  const available = Math.max(body.clientWidth * .45, body.clientWidth - (rail?.offsetWidth ?? 44) - handle.value!.offsetWidth - 290)
  maxWidth.value = Math.min(MAX_SIDEBAR_WIDTH, Math.floor(available))
  actualWidth.value = panel.offsetWidth
}

function clamp(width: number): number {
  return Math.max(MIN_SIDEBAR_WIDTH, Math.min(maxWidth.value, Math.round(width)))
}

function start(event: PointerEvent): void {
  const panel = sidebar()
  if (!panel || !handle.value || event.button !== 0 || drag || !panel.offsetWidth) return
  event.preventDefault()
  measure()
  const initial = props.modelValue ?? 0
  drag = {
    pointerId: event.pointerId, x: event.clientX, scale: panel.getBoundingClientRect().width / panel.offsetWidth,
    width: actualWidth.value, initial, current: initial
  }
  dragging.value = true
  handle.value.focus()
  handle.value.setPointerCapture(event.pointerId)
}

function move(event: PointerEvent): void {
  if (!drag || event.pointerId !== drag.pointerId) return
  const direction = props.side === 'left' ? 1 : -1
  const delta = Math.round(direction * (event.clientX - drag.x) / drag.scale)
  const width = delta ? clamp(drag.width + delta) : drag.initial
  if (width === drag.current) return
  drag.current = width
  emit('update:modelValue', width)
}

function stop(event: PointerEvent, cancelled = false): void {
  if (!drag || event.pointerId !== drag.pointerId) return
  const { initial, current } = drag
  drag = undefined
  dragging.value = false
  if (cancelled) emit('update:modelValue', initial)
  else if (current !== initial) emit('commit', current)
}

function commit(width: number): void {
  if (width === (props.modelValue ?? 0)) return
  emit('update:modelValue', width)
  emit('commit', width)
}

function resizeKey(event: KeyboardEvent): void {
  if (!['Home', 'End', 'ArrowLeft', 'ArrowRight'].includes(event.key)) return
  event.preventDefault()
  measure()
  if (event.key === 'Home') commit(0)
  else if (event.key === 'End') commit(clamp(maxWidth.value))
  else {
    const direction = event.key === 'ArrowRight' ? 1 : -1
    const step = (event.shiftKey ? 40 : 10) * (props.side === 'left' ? direction : -direction)
    commit(clamp(actualWidth.value + step))
  }
}

onMounted(() => {
  const panel = sidebar(), body = handle.value?.parentElement
  if (!panel || !body) return
  measure()
  observer = new ResizeObserver(measure)
  observer.observe(panel)
  observer.observe(body)
})
onBeforeUnmount(() => {
  observer?.disconnect()
  if (drag) emit('update:modelValue', drag.initial)
})
</script>

<template>
  <div ref="handle" class="sidebar-resize" :class="{ dragging }" role="separator" tabindex="0" aria-orientation="vertical"
    aria-label="调整侧栏宽度" aria-controls="workbench-sidebar" :aria-valuenow="actualWidth"
    :aria-valuemin="Math.min(MIN_SIDEBAR_WIDTH, actualWidth)" :aria-valuemax="Math.max(maxWidth, actualWidth)" :aria-valuetext="`${actualWidth} 像素`"
    title="拖动调整侧栏宽度，双击恢复默认" @dblclick="commit(0)" @keydown="resizeKey"
    @pointerdown="start" @pointermove="move" @pointerup="stop($event)" @pointercancel="stop($event, true)" @lostpointercapture="stop($event, true)" />
</template>
