<script setup lang="ts">
import { ref, watch } from 'vue'
import { prepareHtmlPreview } from '../static-html'
import hostUrl from '../html-preview-host.html?url&no-inline'
import type { PreviewResources } from '../preview-resources'

const props = defineProps<{ source: string; assets?: PreviewResources; interactive: boolean }>()
const emit = defineEmits<{ scripts: [available: boolean] }>()
const url = ref(''), error = ref(''), interactiveDocument = ref('')
function startInteractive(event: Event) {
  const frame = event.currentTarget as HTMLIFrameElement
  // Sandboxed frames have an opaque origin. Address only our newly loaded host.
  frame.contentWindow?.postMessage({ type: 'meridian-html-preview', html: interactiveDocument.value }, '*')
}
watch(() => [props.source, props.assets, props.interactive] as const, ([source, assets, interactive], _, cleanup) => {
  url.value = ''; interactiveDocument.value = ''; error.value = ''
  let live = true, blob = ''
  const timer = setTimeout(async () => {
    try {
      const result = await prepareHtmlPreview(source, assets, interactive)
      if (!live) return
      emit('scripts', result.hasScripts)
      if (interactive && result.hasScripts) interactiveDocument.value = result.html
      else {
        blob = URL.createObjectURL(new Blob([result.html], { type: 'text/html;charset=utf-8' }))
        url.value = blob
      }
      error.value = ''
    } catch (cause) {
      if (live) { emit('scripts', false); error.value = (cause as Error).message; url.value = '' }
    }
  }, 100)
  cleanup(() => { live = false; clearTimeout(timer); if (blob) URL.revokeObjectURL(blob) })
}, { immediate: true })
</script>
<template>
  <p v-if="error" role="alert" class="error-box">{{ error }}</p>
  <iframe v-else-if="interactiveDocument" class="static-html" :src="hostUrl" sandbox="allow-scripts" referrerpolicy="no-referrer" title="交互 HTML 预览" @load="startInteractive"></iframe>
  <iframe v-else-if="url" class="static-html" :src="url" sandbox="" referrerpolicy="no-referrer" title="静态 HTML 预览"></iframe>
</template>
<style scoped>.static-html { border: 0; flex: 1; width: 100%; min-height: 0; background: white; color-scheme: light; }</style>
