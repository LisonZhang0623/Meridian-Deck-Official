<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight } from '@lucide/vue'
import { sizeLabel, unwrap, type DocumentTab, type Workspace } from '../workbench'

const props = defineProps<{ doc: DocumentTab; session: Workspace }>()
const page = ref(props.doc.preview), offsets = ref([0]), index = ref(0)
const busy = ref(false), error = ref(''), reader = ref<HTMLPreElement>()
let generation = 0

watch(() => [props.doc.preview, props.session.id, props.session.connectionKey], () => {
  generation++
  page.value = props.doc.preview
  offsets.value = [0]
  index.value = 0
  busy.value = false
  error.value = ''
})
onBeforeUnmount(() => { generation++ })

async function navigate(direction: number): Promise<void> {
  const api = window.sshTool.previewPage
  const nextIndex = index.value + direction
  const offset = direction < 0 ? offsets.value[nextIndex] : page.value.page?.nextOffset
  if (!api || offset === undefined || nextIndex < 0 || busy.value || props.session.phase !== 'connected') return
  const request = ++generation
  const id = props.session.id, identity = props.session.connectionKey, version = page.value.version, path = props.doc.path
  const current = () => request === generation && props.session.id === id && props.session.connectionKey === identity
  busy.value = true
  error.value = ''
  try {
    const next = unwrap(await api(id, path, offset, version))
    if (!current() || props.session.phase !== 'connected') return
    if (next.kind !== 'text' || next.page?.offset !== offset || next.version !== version) throw new Error('收到无效的文件分页，请重新加载')
    page.value = next
    offsets.value[nextIndex] = offset
    index.value = nextIndex
    reader.value?.scrollTo(0, 0)
  } catch (problem) {
    if (current()) error.value = (problem as Error).message
  } finally {
    if (current()) busy.value = false
  }
}
</script>

<template>
  <div class="text-page-bar">
    <span class="muted">只读分页 · 第 {{ index + 1 }} 页 · {{ sizeLabel(page.page?.offset || 0) }} 至 {{ sizeLabel(page.page?.nextOffset || page.size) }}</span>
    <button :disabled="!index || busy || session.phase !== 'connected'" @click="navigate(-1)"><ChevronLeft :size="14"/>上一页</button>
    <button :disabled="!page.page?.nextOffset || busy || session.phase !== 'connected'" @click="navigate(1)">下一页<ChevronRight :size="14"/></button>
  </div>
  <div v-if="error" class="inline-error" role="alert">{{ error }}</div>
  <pre ref="reader" class="text-page-reader" aria-label="远程文本当前页" tabindex="0" :aria-busy="busy">{{ page.content }}</pre>
</template>

<style scoped>
.text-page-bar{display:flex;align-items:center;gap:8px;padding:6px 12px;border-bottom:1px solid var(--border-soft);background:var(--bg-surface);flex-wrap:wrap}
.text-page-bar span{margin-right:auto}
.text-page-reader{flex:1;min-height:0;overflow:auto;margin:0;padding:12px 16px;white-space:pre-wrap;overflow-wrap:anywhere;font:var(--editor-font-size, 13px)/1.55 var(--font-mono);tab-size:4}
</style>
