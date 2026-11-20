<script setup lang="ts">
import { ref, watch } from 'vue'
import type { PreviewResources } from '../preview-resources'

const props = defineProps<{ reference: string; alt: string; assets?: PreviewResources }>()
const src = ref<string>()
watch(() => [props.assets, props.reference] as const, async ([assets, reference], _, cleanup) => {
  let live = true
  cleanup(() => { live = false })
  src.value = undefined
  const result = await assets?.read(reference, 'image')
  if (live) src.value = result
}, { immediate: true })
</script>
<template>
  <img v-if="src" :src="src" :alt="alt" decoding="async" @error="src = undefined">
  <span v-else class="muted">{{ alt || '图片' }}</span>
</template>
<style scoped>img { max-width: 100%; height: auto; vertical-align: middle; }</style>
