<script setup lang="ts">
import { onMounted, ref } from 'vue'
import type { Confirmation } from '../confirmation'

defineProps<{ request: Confirmation }>()
const emit = defineEmits<{ resolve: [accepted: boolean] }>()
const modal = ref<HTMLDialogElement>()

onMounted(() => modal.value?.showModal())

function finish(accepted: boolean): void {
  modal.value?.close()
  emit('resolve', accepted)
}
</script>

<template>
  <dialog ref="modal" class="confirmation-dialog" role="alertdialog" aria-labelledby="confirmation-title" aria-describedby="confirmation-message" @cancel.prevent="finish(false)">
    <header class="dialog-header"><h2 id="confirmation-title">{{ request.title }}</h2></header>
    <div class="dialog-body">
      <p id="confirmation-message">{{ request.message }}</p>
    </div>
    <footer class="dialog-footer">
      <button type="button" autofocus @click="finish(false)">取消</button>
      <button type="button" :class="request.danger ? 'danger-button' : 'primary'" @click="finish(true)">{{ request.action }}</button>
    </footer>
  </dialog>
</template>

<style scoped>
.confirmation-dialog { width: min(420px, calc(100vw - 32px)); }
.confirmation-dialog p { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
