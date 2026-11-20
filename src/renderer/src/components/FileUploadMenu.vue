<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ChevronDown, FileUp, FolderUp, Upload } from '@lucide/vue'

const props = defineProps<{ disabled: boolean; allowDirectory: boolean }>()
const emit = defineEmits<{ files: []; directory: [] }>()
const root = ref<HTMLElement>(), trigger = ref<HTMLButtonElement>(), menu = ref<HTMLElement>(), open = ref(false)

function close(restore = false): void {
  open.value = false
  if (restore) trigger.value?.focus()
}

function items(): HTMLButtonElement[] {
  return [...(menu.value?.querySelectorAll<HTMLButtonElement>('button') ?? [])]
}

async function show(last = false): Promise<void> {
  if (props.disabled) return
  open.value = true
  await nextTick()
  const options = items()
  const option = last ? options.at(-1) : options[0]
  option?.focus()
}

function triggerKey(event: KeyboardEvent): void {
  if (!['ArrowDown', 'ArrowUp'].includes(event.key)) return
  event.preventDefault()
  void show(event.key === 'ArrowUp')
}

function menuKey(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    close(true)
  } else if (event.key === 'Tab') {
    close(true)
  } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    event.preventDefault()
    const options = items(), index = options.indexOf(event.target as HTMLButtonElement)
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : options.length - 1)) % options.length
    options[next]?.focus()
  }
}

function choose(kind: 'files' | 'directory'): void {
  close(true)
  if (kind === 'files') emit('files')
  else emit('directory')
}

function outside(event: PointerEvent): void { if (!root.value?.contains(event.target as Node)) close() }
function blur(): void { close() }
watch(() => props.disabled, disabled => { if (disabled) close() })
onMounted(() => { document.addEventListener('pointerdown', outside); window.addEventListener('blur', blur) })
onBeforeUnmount(() => { document.removeEventListener('pointerdown', outside); window.removeEventListener('blur', blur) })
</script>

<template>
  <div ref="root" class="file-upload">
    <button id="file-upload-trigger" ref="trigger" type="button" class="file-upload-trigger" :disabled="disabled"
      aria-haspopup="menu" :aria-expanded="open" :aria-controls="open ? 'file-upload-menu' : undefined"
      @click="open ? close(true) : show()" @keydown="triggerKey" @keydown.esc.stop="close()">
      <Upload :size="15" aria-hidden="true" />上传<ChevronDown :size="13" aria-hidden="true" />
    </button>
    <div v-if="open" id="file-upload-menu" ref="menu" class="file-upload-menu" role="menu" aria-labelledby="file-upload-trigger" @keydown="menuKey">
      <button type="button" role="menuitem" tabindex="-1" @click="choose('files')"><FileUp :size="15" aria-hidden="true" />上传文件</button>
      <button v-if="allowDirectory" type="button" role="menuitem" tabindex="-1" @click="choose('directory')"><FolderUp :size="15" aria-hidden="true" />上传文件夹</button>
    </div>
  </div>
</template>

<style scoped>
.file-upload { position: relative; }
.file-upload-trigger { gap: 5px; padding-inline: 7px; }
.file-upload-menu { position: absolute; top: calc(100% + 4px); left: 0; z-index: 8; min-width: 132px; padding: 4px; border: 1px solid var(--border); border-radius: 6px; background: var(--background); box-shadow: 0 6px 20px var(--shadow); }
.file-upload-menu button { width: 100%; justify-content: flex-start; white-space: nowrap; border-color: transparent; }
.file-upload-menu button:focus-visible { background: var(--highlight); outline-offset: -2px; }
</style>
