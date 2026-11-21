<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { X } from '@lucide/vue'
import StartupArtwork from './StartupArtwork.vue'
import { state, unwrap } from '../workbench'
import { pickStartupAnimation, DEFAULT_TERMINAL_FONT_SIZE, MIN_TERMINAL_FONT_SIZE, MAX_TERMINAL_FONT_SIZE, DEFAULT_EDITOR_FONT_SIZE, MIN_EDITOR_FONT_SIZE, MAX_EDITOR_FONT_SIZE, DEFAULT_UI_ZOOM, MIN_UI_ZOOM, MAX_UI_ZOOM } from '../../../shared/settings'
import type { Settings, StartupAnimationVariant } from '../../../shared/types'

const systemTheme = matchMedia('(prefers-color-scheme: dark)')
const systemDark = ref(systemTheme.matches)
function updateTheme(event: MediaQueryListEvent) { systemDark.value = event.matches }
onMounted(() => systemTheme.addEventListener('change', updateTheme))
onBeforeUnmount(() => systemTheme.removeEventListener('change', updateTheme))
const previewKey = ref(0)
const previewVariant = ref<StartupAnimationVariant>()
const emit = defineEmits<{ close: [] }>()
const modal = ref<HTMLDialogElement>()
const saving = ref(false), error = ref('')
const checkUpdateAvailable = !!window.sshTool.checkUpdate
const preferences = reactive<Pick<Settings, 'terminalFontSize' | 'editorFontSize' | 'uiZoom' | 'theme' | 'sidebarSide' | 'windowCloseAction' | 'startupAnimation' | 'checkUpdatesOnStartup'>>({
  terminalFontSize: state.settings.terminalFontSize,
  editorFontSize: state.settings.editorFontSize,
  uiZoom: state.settings.uiZoom,
  theme: state.settings.theme,
  startupAnimation: state.settings.startupAnimation,
  sidebarSide: state.settings.sidebarSide,
  windowCloseAction: !state.traySupported && state.settings.windowCloseAction === 'tray' ? 'ask' : state.settings.windowCloseAction,
  checkUpdatesOnStartup: state.settings.checkUpdatesOnStartup === true
})
onMounted(() => modal.value?.showModal())
function previewAnimation(): void {
  previewVariant.value = pickStartupAnimation(preferences.startupAnimation)
  previewKey.value++
}
function resetTextSize(): void {
  preferences.terminalFontSize = DEFAULT_TERMINAL_FONT_SIZE
  preferences.editorFontSize = DEFAULT_EDITOR_FONT_SIZE
  preferences.uiZoom = DEFAULT_UI_ZOOM
}
function close(): void { if (!saving.value) emit('close') }
async function save(): Promise<void> {
  if (saving.value) return
  saving.value = true
  error.value = ''
  try {
    unwrap(await window.sshTool.saveSettings({ ...state.settings, ...preferences }))
    Object.assign(state.settings, preferences)
    emit('close')
  } catch (cause) {
    error.value = (cause as Error).message
  } finally { saving.value = false }
}
</script>

<template>
  <dialog ref="modal" class="settings-dialog" aria-labelledby="settings-title" @cancel.prevent="close">
    <form @submit.prevent="save">
      <header class="dialog-header"><h2 id="settings-title">设置</h2><button type="button" class="icon-button" aria-label="关闭设置" :disabled="saving" @click="close"><X :size="18"/></button></header>
      <div class="dialog-body">
        <fieldset class="settings-columns" :disabled="saving">
          <legend>外观与窗口</legend>
          <label>主题<select v-model="preferences.theme"><option value="system">跟随系统</option><option value="light">浅色</option><option value="dark">深色</option></select></label>
          <label>侧栏位置<select v-model="preferences.sidebarSide"><option value="left">左侧</option><option value="right">右侧</option></select></label>
          <label>关闭窗口时<select v-model="preferences.windowCloseAction"><option value="ask">每次询问</option><option v-if="state.traySupported" value="tray">收起到系统托盘</option><option value="quit">退出程序</option></select></label>
          <label>启动动画<select v-model="preferences.startupAnimation" @change="previewVariant = undefined"><option value="echo">经线回声</option><option value="orbit">瓷蓝轨道</option><option value="random">随机播放</option><option value="off">不展示</option></select></label>
          <label>工作台缩放（%）<input v-model.number="preferences.uiZoom" type="number" :min="MIN_UI_ZOOM" :max="MAX_UI_ZOOM" required></label>
          <button v-if="preferences.startupAnimation !== 'off'" class="preview-button" type="button" @click="previewAnimation">预览动画</button>
          <StartupArtwork v-if="previewVariant" :key="previewKey" class="startup-preview" :variant="previewVariant" :dark="preferences.theme === 'system' ? systemDark : preferences.theme === 'dark'" />
        </fieldset>
        <fieldset class="settings-columns" :disabled="saving">
          <legend>字号</legend>
          <label>终端字号（px）<input v-model.number="preferences.terminalFontSize" type="number" :min="MIN_TERMINAL_FONT_SIZE" :max="MAX_TERMINAL_FONT_SIZE" required></label>
          <label>编辑器字号（px）<input v-model.number="preferences.editorFontSize" type="number" :min="MIN_EDITOR_FONT_SIZE" :max="MAX_EDITOR_FONT_SIZE" required></label>
          <div class="font-sample" :style="{ fontSize: preferences.terminalFontSize + 'px' }">终端字号预览</div>
          <div class="font-sample" :style="{ fontSize: preferences.editorFontSize + 'px' }">编辑器字号预览</div>
        </fieldset>
        <slot />
        <fieldset v-if="checkUpdateAvailable" :disabled="saving">
          <legend>软件更新</legend>
          <label class="check"><input v-model="preferences.checkUpdatesOnStartup" type="checkbox">启动时检查更新</label>
        </fieldset>
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      </div>
      <footer class="dialog-footer"><button type="button" class="cancel" :disabled="saving" @click="resetTextSize">恢复默认字号与缩放</button><button type="button" :disabled="saving" @click="close">取消</button><button class="primary" type="submit" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button></footer>
    </form>
  </dialog>
</template>

<style scoped>
.settings-dialog { width: 460px; overflow: hidden; }
.settings-dialog form { display: flex; flex-direction: column; max-height: inherit; }
.settings-dialog .dialog-body { min-height: 0; overflow-y: auto; padding-block: 14px; }
.settings-dialog :is(.dialog-header, .dialog-footer) { flex-shrink: 0; }
.settings-dialog .dialog-footer { flex-wrap: wrap; }
.settings-dialog fieldset { display: grid; gap: 11px; padding: 10px 12px 12px; }
.settings-columns { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.settings-dialog .preview-button { align-self: end; }
.settings-dialog .startup-preview { grid-column: 1 / -1; max-width: 300px; justify-self: center; border: 1px solid var(--border); border-radius: var(--radius-card); }
.settings-dialog .font-sample { padding: 8px 10px; }
.settings-dialog .error-box { margin: 0; }
</style>
