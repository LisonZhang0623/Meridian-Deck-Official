<script setup lang="ts">
import { computed } from 'vue'
import katex from 'katex'
import 'katex/dist/katex.min.css'

const props = defineProps<{ tex: string; display: boolean }>()
const html = computed(() => {
  if (props.tex.length > 8192) return ''
  try {
    return katex.renderToString(props.tex, {
      displayMode: props.display, trust: false, strict: 'ignore', maxExpand: 500, maxSize: 20,
    })
  } catch { return '' }
})
</script>

<template>
  <span v-if="html" :class="{ 'display-equation': display }" v-html="html"></span>
  <code v-else :title="'无法排版此公式'">{{ tex }}</code>
</template>

<style scoped>
.display-equation { display: block; max-width: 100%; overflow-x: auto; overflow-y: hidden; }
</style>
