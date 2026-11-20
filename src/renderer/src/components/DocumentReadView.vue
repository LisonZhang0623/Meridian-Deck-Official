<script setup lang="ts">
import { computed, defineAsyncComponent, h, ref, type VNodeChild } from 'vue'
import MarkdownCode from './MarkdownCode.vue'
import MarkdownImage from './MarkdownImage.vue'
import { externalWebUrl, relativeRemotePath, type PreviewResources } from '../preview-resources'
import { parseTable } from '../document-formats'
import { renderMarkdown } from '../markdown-preview'

const props = defineProps<{ source: string; mode: 'markdown' | 'csv' | 'tsv'; path: string; assets?: PreviewResources }>()
const MarkdownMath = defineAsyncComponent(() => import('./MarkdownMath.vue'))
const linkError = ref('')
function link(raw: string, label: VNodeChild[]) {
  const url = externalWebUrl(raw)
  if (!url) return h('span', null, label)
  return h('button', { type: 'button', class: 'markdown-external', title: url, onClick: async () => {
    try {
      const { Browser } = await import('@wailsio/runtime')
      await Browser.OpenURL(url)
      linkError.value = ''
    } catch { linkError.value = '无法打开系统浏览器' }
  } }, label)
}
const preview = computed(() => {
  try {
    return props.mode === 'markdown'
      ? { nodes: renderMarkdown(props.source, {
          math: (tex, display) => h(MarkdownMath, { tex, display }),
          code: (source, language) => h(MarkdownCode, { source, language }),
          image: (reference, alt) => relativeRemotePath(props.path, reference)
            ? h(MarkdownImage, { reference, alt, assets: props.assets }) : h('span', null, alt || '图片'),
          link,
        }) }
      : { table: parseTable(props.source, props.mode === 'csv' ? ',' : '\t') }
  } catch (error) {
    return { error: (error as Error).message }
  }
})
const MarkdownBody = () => h('article', { class: 'markdown-body' }, preview.value.nodes || [])
</script>

<template>
  <div class="read-view" tabindex="0">
    <p v-if="linkError" role="alert" class="error-box">{{ linkError }}</p>
    <p v-if="preview.error" class="error-box" role="alert">{{ preview.error }}</p>
    <MarkdownBody v-else-if="preview.nodes" />
    <template v-else-if="preview.table">
      <p v-if="preview.table.truncated" class="muted">表格仅显示前 200 行、50 列，每个单元格最多 2048 字符。</p>
      <p v-if="!preview.table.rows.length" class="muted">表格为空。</p>
      <table v-else :aria-label="mode.toUpperCase() + ' 表格预览'">
        <thead><tr><th scope="col">行</th><th v-for="column in preview.table.columns" :key="column" scope="col">{{ column }}</th></tr></thead>
        <tbody>
          <tr v-for="(row, rowIndex) in preview.table.rows" :key="rowIndex">
            <th scope="row">{{ rowIndex + 1 }}</th>
            <td v-for="column in preview.table.columns" :key="column">{{ row[column - 1] || '' }}</td>
          </tr>
        </tbody>
      </table>
    </template>
  </div>
</template>

<style scoped>
.read-view{flex:1;min-height:0;overflow:auto;padding:16px 20px;line-height:1.65}
.read-view :deep(.markdown-body){max-width:900px;margin:0 auto;overflow-wrap:anywhere}
.read-view :deep(h1),.read-view :deep(h2){padding-bottom:6px;border-bottom:1px solid var(--border)}
.read-view :deep(blockquote){margin-left:0;border-left:3px solid var(--border);padding-left:12px;color:var(--muted)}
.read-view :deep(pre){overflow:auto;white-space:pre;padding:10px;background:var(--bg-surface);border:1px solid var(--border-soft);border-radius:var(--radius-control)}
.read-view :deep(code){font-family:var(--font-mono);background:var(--bg-surface)}
.read-view :deep(table){border-collapse:collapse;text-align:left;max-width:100%}
.read-view :deep(th),.read-view :deep(td){border:1px solid var(--border);padding:5px 9px;min-width:40px;max-width:400px;white-space:pre-wrap;overflow-wrap:anywhere;vertical-align:top}
.read-view :deep(th){background:var(--surface);color:var(--muted)}
.read-view :deep(.markdown-external){display:inline;border:0;padding:0;min-height:0;background:transparent;color:var(--blue);font:inherit;text-decoration:underline;cursor:pointer}
</style>
