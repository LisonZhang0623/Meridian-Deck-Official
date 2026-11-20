<script setup lang="ts">
import { computed, h, shallowRef, watch, type VNodeChild } from 'vue'
import { LanguageDescription, type LanguageSupport } from '@codemirror/language'
import { languages } from '@codemirror/language-data'
import { highlightCode, classHighlighter } from '@lezer/highlight'

const props = defineProps<{ source: string; language: string }>()
const support = shallowRef<LanguageSupport>()
watch(() => props.language, async (name, _, cleanup) => {
  let live = true
  cleanup(() => { live = false })
  support.value = undefined
  const language = LanguageDescription.matchLanguageName(languages, name) ?? LanguageDescription.matchFilename(languages, 'file.' + name)
  const result = await language?.load().catch(() => undefined)
  if (live) support.value = result
}, { immediate: true })
const tokens = computed(() => {
  if (!support.value || props.source.length > 32768) return props.source
  const parts: VNodeChild[] = []
  highlightCode(props.source, support.value.language.parser.parse(props.source), classHighlighter,
    (text, classes) => parts.push(classes ? h('span', { class: classes }, text) : text), () => parts.push('\n'))
  return parts
})
const Code = () => h('code', null, tokens.value)
</script>
<template><pre><Code /></pre></template>
<style scoped>
pre :deep(.tok-keyword) { color: var(--syntax-keyword); font-weight: 600; }
pre :deep(.tok-string), pre :deep(.tok-regexp) { color: var(--syntax-string); }
pre :deep(.tok-number), pre :deep(.tok-atom), pre :deep(.tok-bool) { color: var(--syntax-number); }
pre :deep(.tok-typeName), pre :deep(.tok-className) { color: var(--syntax-type); }
pre :deep(.tok-propertyName) { color: var(--syntax-property); }
pre :deep(.tok-comment) { color: var(--syntax-comment); }
pre :deep(.tok-operator), pre :deep(.tok-punctuation) { color: var(--syntax-punctuation); }
</style>
