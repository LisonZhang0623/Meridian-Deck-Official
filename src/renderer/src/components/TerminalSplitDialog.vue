<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { X } from '@lucide/vue'
import type { SplitDirection } from '../terminal-layout'
import type { Workspace } from '../workbench'

const props = defineProps<{
  source: Workspace
  direction: SplitDirection
  targets: Workspace[]
  canCreate: boolean
  busy: boolean
  confirm: (targetId: string) => Promise<void>
}>()
const emit = defineEmits<{ close: [] }>()
const modal = ref<HTMLDialogElement>(), error = ref('')
const selected = ref<string>()
const canSubmit = computed(() => selected.value === '' ? props.canCreate : props.targets.some(session => session.id === selected.value))

onMounted(() => modal.value?.showModal())

function close(): void { if (!props.busy) emit('close') }

async function submit(): Promise<void> {
  if (!canSubmit.value || props.busy || selected.value === undefined) return
  error.value = ''
  try { await props.confirm(selected.value) }
  catch (failure) { error.value = (failure as Error).message }
}
</script>

<template>
  <dialog ref="modal" class="terminal-split-dialog" aria-labelledby="terminal-split-title" @cancel.prevent="close">
    <form @submit.prevent="submit">
      <header class="dialog-header">
        <div><h2 id="terminal-split-title">{{ direction === 'horizontal' ? '左右分屏' : '上下分屏' }}</h2><p>{{ source.name }} · {{ source.endpoint }}</p></div>
        <button type="button" class="icon-button" aria-label="关闭分屏选择" :disabled="busy" @click="close"><X :size="18"/></button>
      </header>
      <div class="dialog-body">
        <label>另一个终端
          <select v-model="selected" :disabled="busy" autofocus>
            <option :value="undefined" disabled>请选择终端</option>
            <option v-if="canCreate" value="">在当前服务器新建终端</option>
            <option v-for="session in targets" :key="session.id" :value="session.id">{{ session.name }} · {{ session.endpoint }}{{ session.terminalDirectory ? ' · ' + session.terminalDirectory : '' }}</option>
          </select>
        </label>
        <p v-if="!canCreate && !targets.length" class="muted">连接服务器后即可选择分屏终端。</p>
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      </div>
      <footer class="dialog-footer">
        <button type="button" :disabled="busy" @click="close">取消</button>
        <button class="primary" :disabled="busy || !canSubmit">{{ busy ? '正在打开…' : '打开分屏' }}</button>
      </footer>
    </form>
  </dialog>
</template>

<style scoped>
.terminal-split-dialog form { display: flex; flex-direction: column; max-height: inherit; }
.terminal-split-dialog .dialog-header, .terminal-split-dialog .dialog-footer { flex-shrink: 0; }
.terminal-split-dialog .dialog-header div { min-width: 0; }
.terminal-split-dialog .dialog-header p { overflow-wrap: anywhere; }
.terminal-split-dialog .dialog-body { min-height: 0; overflow-y: auto; }
.terminal-split-dialog .dialog-body p { margin: 0; }
</style>
