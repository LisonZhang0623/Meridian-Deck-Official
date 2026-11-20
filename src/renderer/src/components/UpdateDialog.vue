<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ExternalLink, LoaderCircle, X } from '@lucide/vue'
import type { UpdateRelease } from '../../../shared/types'

const props = defineProps<{ version: string; release: UpdateRelease | null; checking: boolean; error: { message: string; code?: string } | null; openError: string; canOpen: boolean }>()
const emit = defineEmits<{ close: []; retry: []; open: [] }>()
const modal = ref<HTMLDialogElement>()
const useBrowser = computed(() => props.error?.code === 'UPDATE_RATE_LIMIT')
const releaseNotes = computed(() => props.release?.notes.replace(/<!--[\s\S]*?-->/g, '').trim())
onMounted(() => modal.value?.showModal())
</script>

<template>
  <dialog ref="modal" class="update-dialog" aria-labelledby="update-title" @cancel.prevent="emit('close')">
    <header class="dialog-header">
      <div><h2 id="update-title">检查更新</h2><p>当前版本 {{ props.version }}</p></div>
      <button type="button" class="icon-button" aria-label="关闭检查更新" @click="emit('close')"><X :size="18"/></button>
    </header>
    <div class="dialog-body" aria-live="polite">
      <p v-if="checking" class="update-status"><LoaderCircle class="spin" :size="16"/>正在检查 GitHub 发布版本…</p>
      <p v-else-if="error" class="error-box">{{ error.message }}</p>
      <template v-else-if="release">
        <p class="update-status">发现新版本 <strong>{{ release.version }}</strong></p>
        <pre v-if="releaseNotes" class="release-notes" tabindex="0" aria-label="版本说明">{{ releaseNotes }}</pre>
      </template>
      <p v-else class="update-status">没有发现更新的版本。</p>
      <p v-if="openError" class="error-box">{{ openError }}</p>
    </div>
    <footer class="dialog-footer">
      <button v-if="error && !checking" type="button" class="cancel" @click="emit('retry')">重试</button>
      <button v-if="canOpen && !checking" type="button" :class="{ primary: !!release || useBrowser }" @click="emit('open')"><ExternalLink :size="14"/>打开发布页</button>
      <button type="button" @click="emit('close')">关闭</button>
    </footer>
  </dialog>
</template>

<style scoped>
.update-status { display: flex; align-items: center; gap: 8px; margin: 0; }
.release-notes { margin: 0; max-height: 42vh; overflow: auto; overflow-wrap: anywhere; white-space: pre-wrap; font: inherit; line-height: 1.65; padding: 10px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--surface); }
</style>
