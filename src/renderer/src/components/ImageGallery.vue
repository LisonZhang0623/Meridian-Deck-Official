<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, Columns2, Image, RotateCw } from '@lucide/vue'
import type { RemoteEntry } from '../../../shared/types'
import { loadDirectory, parentPath, sizeLabel, unwrap, type Workspace } from '../workbench'

const props = defineProps<{ session: Workspace; current: string; active: boolean }>()
const emit = defineEmits<{ open: [entry: RemoteEntry] }>()
const pageSize = 24
const directory = computed(() => parentPath(props.current))
const images = computed(() => (props.session.tree[directory.value] || []).filter(entry => entry.type !== 'directory' && /\.(png|jpe?g|gif|webp|bmp)$/i.test(entry.name)))
const currentPage = () => Math.max(0, Math.floor(images.value.findIndex(entry => entry.path === props.current) / pageSize))
const page = ref(currentPage())
const count = computed(() => Math.max(1, Math.ceil(images.value.length / pageSize)))
const visible = computed(() => images.value.slice(page.value * pageSize, (page.value + 1) * pageSize))
const thumbnails = ref<Record<string, string>>({})
const thumbnailErrors = ref<Record<string, string>>({})
const selected = ref<RemoteEntry[]>([])
const comparison = ref<{ entry: RemoteEntry; src: string }[]>([])
const comparing = ref(false), compareError = ref('')
const connected = computed(() => props.session.phase === 'connected')
let compareGeneration = 0

const refresh = async () => {
  await loadDirectory(props.session, directory.value, true)
}
onMounted(() => { if (!props.session.tree[directory.value] && connected.value) void loadDirectory(props.session, directory.value) })
watch(count, value => { if (page.value >= value) page.value = value - 1 })
watch(() => [props.session.id, props.session.connectionKey, directory.value], () => {
  compareGeneration++
  page.value = currentPage()
  selected.value = []
  comparison.value = []
  comparing.value = false
  compareError.value = ''
})
onBeforeUnmount(() => { compareGeneration++ })

watch([visible, connected, () => props.active, () => props.session.id, () => props.session.connectionKey, () => comparison.value.length], async (_, __, onCleanup) => {
  let cancelled = false
  onCleanup(() => { cancelled = true })
  const thumbnail = window.sshTool.thumbnail
  if (!thumbnail || !connected.value || !props.active || comparison.value.length) return
  const id = props.session.id
  thumbnails.value = {}
  thumbnailErrors.value = {}
  for (const entry of visible.value) {
    try {
      const src = unwrap(await thumbnail(id, entry.path))
      if (cancelled) return
      if (!src.startsWith('data:image/jpeg;base64,')) throw new Error('无效的图片缩略图')
      thumbnails.value[entry.path] = src
    } catch (error) {
      if (cancelled) return
      thumbnailErrors.value[entry.path] = (error as Error).message
    }
  }
}, { immediate: true })

function chosen(path: string): boolean { return selected.value.some(entry => entry.path === path) }
function toggle(entry: RemoteEntry): void {
  if (chosen(entry.path)) selected.value = selected.value.filter(item => item.path !== entry.path)
  else if (selected.value.length < 2) selected.value.push(entry)
  compareError.value = ''
}

async function compare(): Promise<void> {
  if (selected.value.length !== 2 || !connected.value || comparing.value) return
  const request = ++compareGeneration, id = props.session.id, identity = props.session.connectionKey
  const current = () => request === compareGeneration && id === props.session.id && identity === props.session.connectionKey
  comparing.value = true
  compareError.value = ''
  try {
    const results = await Promise.all(selected.value.map(async entry => {
      const preview = unwrap(await window.sshTool.preview(id, entry.path))
      if (preview.kind !== 'image' || !/^data:image\/(?:png|jpeg|gif|webp|bmp);base64,/.test(preview.content)) throw new Error(`无法预览图片“${entry.name}”`)
      return { entry, src: preview.content }
    }))
    if (current() && connected.value) comparison.value = results
  } catch (error) {
    if (current()) compareError.value = (error as Error).message
  } finally {
    if (current()) comparing.value = false
  }
}
</script>

<template>
  <div class="gallery-toolbar">
    <span class="muted" :title="directory">同目录 {{ images.length }} 张图片</span>
    <template v-if="!comparison.length">
      <button class="icon-button" aria-label="刷新图片列表" :disabled="!connected || session.loading.has(directory)" @click="refresh"><RotateCw :size="14"/></button>
      <button class="icon-button" aria-label="上一页缩略图" :disabled="page === 0 || comparing" @click="page--"><ChevronLeft :size="14"/></button>
      <span>{{ page + 1 }} / {{ count }}</span>
      <button class="icon-button" aria-label="下一页缩略图" :disabled="page + 1 >= count || comparing" @click="page++"><ChevronRight :size="14"/></button>
      <button :disabled="selected.length !== 2 || !connected || comparing" @click="compare"><Columns2 :size="14"/>{{ comparing ? '正在加载…' : `并排对比（${selected.length}/2）` }}</button>
    </template>
    <button v-else @click="comparison = []">返回缩略图</button>
  </div>
  <p v-if="compareError" class="inline-error" role="alert">{{ compareError }}</p>
  <div v-if="comparison.length" class="gallery-comparison" aria-label="图片并排对比">
    <figure v-for="item in comparison" :key="item.entry.path">
      <img :src="item.src" :alt="item.entry.name">
      <figcaption :title="item.entry.name">{{ item.entry.name }} · {{ sizeLabel(item.entry.size) }}</figcaption>
    </figure>
  </div>
  <div v-else class="gallery-scroll">
    <p v-if="!visible.length" class="muted">{{ session.loading.has(directory) ? '正在读取图片目录…' : '当前目录没有可预览的图片。' }}</p>
    <div v-else class="gallery-grid">
      <article v-for="entry in visible" :key="entry.path" :class="{ current: entry.path === current }">
        <button class="gallery-image" :aria-label="`查看 ${entry.name}`" :title="thumbnailErrors[entry.path] || entry.name" :disabled="!connected && entry.path !== current" @click="emit('open', entry)">
          <img v-if="thumbnails[entry.path]" :src="thumbnails[entry.path]" alt="">
          <Image v-else :size="25" aria-hidden="true"/>
        </button>
        <div class="gallery-entry"><span :title="entry.name">{{ entry.name }}</span><label :title="`选择 ${entry.name} 参与对比`"><input type="checkbox" :aria-label="`选择 ${entry.name} 参与对比`" :checked="chosen(entry.path)" :disabled="comparing || (!chosen(entry.path) && selected.length >= 2)" @change="toggle(entry)"></label></div>
      </article>
    </div>
  </div>
</template>

<style scoped>
.gallery-toolbar{display:flex;flex-wrap:wrap;gap:7px;align-items:center;padding:7px 12px;border-bottom:1px solid var(--border)}
.gallery-toolbar>.muted{margin-right:auto}
.gallery-scroll,.gallery-comparison{flex:1;min-height:0;overflow:auto;padding:12px;background:var(--surface)}
.gallery-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(125px,1fr));gap:9px;align-items:start}
.gallery-grid article{min-width:0;border:1px solid var(--border);border-radius:6px;overflow:hidden;background:var(--background)}
.gallery-grid article.current{border-color:var(--blue)}
.gallery-image{width:100%;height:110px;border:0;border-radius:0;padding:3px;color:var(--muted)}
.gallery-image img{display:block;width:100%;height:100%;object-fit:contain}
.gallery-entry{display:flex;gap:4px;align-items:center;padding:6px 7px}
.gallery-entry span,.gallery-comparison figcaption{white-space:nowrap;overflow:hidden;text-overflow:ellipsis;min-width:0}
.gallery-entry label{margin-left:auto;display:grid;place-items:center}
.gallery-comparison{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}
.gallery-comparison figure{display:grid;grid-template-rows:minmax(0,1fr) auto;min-height:0;min-width:0;margin:0;text-align:center}
.gallery-comparison img{display:block;width:100%;height:100%;object-fit:contain}
.gallery-comparison figcaption{padding:5px;background:var(--background)}
</style>
