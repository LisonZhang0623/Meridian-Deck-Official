<script setup lang="ts">
import { computed, nextTick, onMounted, onBeforeUnmount, ref } from 'vue'
import { Check } from '@lucide/vue'
import { active, selectedProfile, state, attempt, setLayout, newTerminal, closeSession } from '../workbench'
import { MIN_TERMINAL_FONT_SIZE, MAX_TERMINAL_FONT_SIZE, DEFAULT_TERMINAL_FONT_SIZE, MIN_UI_ZOOM, MAX_UI_ZOOM, DEFAULT_UI_ZOOM, UI_ZOOM_STEP } from '../../../shared/settings'

const props = defineProps<{ split: boolean; followDirectory: boolean }>()
const emit = defineEmits<{ createConnection: []; editConnection: []; openSettings: []; checkUpdate: []; splitTerminal: [direction: 'horizontal' | 'vertical']; renameTerminal: []; unsplit: []; openTmux: []; toggleFollow: []; importConfig: []; exportConfig: []; openFtp: [] }>()
interface Item { label: string; action?: () => void; disabled?: boolean; checked?: boolean; shortcut?: string; separator?: boolean }
const root = ref<HTMLElement>(), opened = ref(-1)
const divider: Item = { label: '', separator: true }
const setting = (patch: Parameters<typeof setLayout>[0]) => { void attempt(() => setLayout(patch)) }

const menus = computed<{ label: string; items: Item[] }[]>(() => [
  { label: '连接', items: [
    { label: '新建 SSH 连接…', action: () => emit('createConnection') },
    { label: '编辑当前连接…', disabled: !selectedProfile.value, action: () => emit('editConnection') },
    divider,
    { label: 'FTP 文件传输…', disabled: !window.sshTool.ftpConnect, action: () => emit('openFtp') },
    { label: '导入连接与界面偏好…', disabled: !window.sshTool.importConfig, action: () => emit('importConfig') },
    { label: '导出配置…', disabled: !window.sshTool.exportConfig, action: () => emit('exportConfig') }
  ] },
  { label: '终端', items: [
    { label: '新建终端', shortcut: 'Ctrl+Shift+T', disabled: !selectedProfile.value, action: newTerminal },
    { label: '重命名当前终端…', disabled: !active.value, action: () => emit('renameTerminal') },
    { label: '关闭当前终端', disabled: !active.value, action: () => { const session = active.value; if (session) void attempt(() => closeSession(session)) } },
    divider,
    { label: '左右分屏…', disabled: !active.value, action: () => emit('splitTerminal', 'horizontal') },
    { label: '上下分屏…', disabled: !active.value, action: () => emit('splitTerminal', 'vertical') },
    { label: '关闭分屏', disabled: !props.split, action: () => emit('unsplit') },
    divider,
    { label: '打开或接回 tmux 会话…', disabled: active.value?.phase !== 'connected' || !window.sshTool.newTmuxTerminal, action: () => emit('openTmux') },
    { label: '文件树跟随终端目录', checked: props.followDirectory, action: () => emit('toggleFollow') }
  ] },
  { label: '视图', items: [
    { label: '显示侧栏', checked: !state.settings.sidebarCollapsed, action: () => setting({ sidebarCollapsed: !state.settings.sidebarCollapsed }) },
    { label: state.settings.sidebarSide === 'right' ? '侧栏移至左侧' : '侧栏移至右侧', action: () => setting({ sidebarSide: state.settings.sidebarSide === 'right' ? 'left' : 'right' }) },
    { label: '显示传输任务', checked: state.transferOpen, action: () => { state.transferOpen = !state.transferOpen } },
    divider,
    { label: `放大界面（${state.settings.uiZoom}%）`, disabled: state.settings.uiZoom >= MAX_UI_ZOOM, action: () => setting({ uiZoom: Math.min(MAX_UI_ZOOM, state.settings.uiZoom + UI_ZOOM_STEP) }) },
    { label: '缩小界面', disabled: state.settings.uiZoom <= MIN_UI_ZOOM, action: () => setting({ uiZoom: Math.max(MIN_UI_ZOOM, state.settings.uiZoom - UI_ZOOM_STEP) }) },
    { label: `增大终端字体（${state.settings.terminalFontSize} px）`, disabled: state.settings.terminalFontSize >= MAX_TERMINAL_FONT_SIZE, action: () => setting({ terminalFontSize: state.settings.terminalFontSize + 1 }) },
    { label: '减小终端字体', disabled: state.settings.terminalFontSize <= MIN_TERMINAL_FONT_SIZE, action: () => setting({ terminalFontSize: state.settings.terminalFontSize - 1 }) },
    {
      label: '恢复默认缩放与字号',
      disabled: state.settings.uiZoom === DEFAULT_UI_ZOOM && state.settings.terminalFontSize === DEFAULT_TERMINAL_FONT_SIZE,
      action: () => setting({ uiZoom: DEFAULT_UI_ZOOM, terminalFontSize: DEFAULT_TERMINAL_FONT_SIZE })
    }
  ] },
  { label: '帮助', items: [
    { label: '首选项设置…', action: () => emit('openSettings') },
    ...(window.sshTool.checkUpdate ? [divider, { label: '检查更新…', action: () => emit('checkUpdate') }] : [])
  ] }
])

function trigger(index: number): HTMLButtonElement | null { return root.value?.querySelector<HTMLButtonElement>(`[data-menu-trigger="${index}"]`) || null }
function close(restore = false): void { const index = opened.value; opened.value = -1; if (restore) trigger(index)?.focus() }
function items(): HTMLButtonElement[] { return [...(root.value?.querySelectorAll<HTMLButtonElement>('[role="menu"] button:not(:disabled)') || [])] }
async function open(index: number, focus?: 'first' | 'last'): Promise<void> {
  opened.value = index
  await nextTick()
  if (focus) { const buttons = items(); (focus === 'first' ? buttons[0] : buttons.at(-1))?.focus() }
}
function triggerKey(event: KeyboardEvent, index: number): void {
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); void open(index, event.key === 'ArrowDown' ? 'first' : 'last') }
  if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
    event.preventDefault(); const next = (index + (event.key === 'ArrowRight' ? 1 : menus.value.length - 1)) % menus.value.length
    trigger(next)?.focus(); if (opened.value >= 0) void open(next)
  }
  if (event.key === 'Escape') { event.preventDefault(); close(true) }
}
function panelKey(event: KeyboardEvent): void {
  const buttons = items(), index = buttons.indexOf(document.activeElement as HTMLButtonElement)
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    event.preventDefault()
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : buttons.length - 1)) % buttons.length
    buttons[next]?.focus()
  } else if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
    event.preventDefault(); void open((opened.value + (event.key === 'ArrowRight' ? 1 : menus.value.length - 1)) % menus.value.length, 'first')
  } else if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(true) }
  else if (event.key === 'Tab') close(true)
}
function run(item: Item): void { close(true); item.action?.() }
function outside(event: PointerEvent): void { if (!root.value?.contains(event.target as Node)) close() }
function shortcut(event: KeyboardEvent): void {
  if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.code === 'KeyT' && !document.querySelector('dialog[open]')) {
    event.preventDefault(); event.stopPropagation()
    if (!event.repeat) { close(); newTerminal() }
  }
}
function blur(): void { close() }
onMounted(() => { document.addEventListener('pointerdown', outside); window.addEventListener('keydown', shortcut, true); window.addEventListener('blur', blur) })
onBeforeUnmount(() => { document.removeEventListener('pointerdown', outside); window.removeEventListener('keydown', shortcut, true); window.removeEventListener('blur', blur) })
</script>

<template>
  <nav ref="root" class="workbench-menubar" role="menubar" aria-label="应用菜单">
    <div v-for="(menu, index) in menus" :key="menu.label" class="workbench-menu">
      <button :id="'menu-trigger-' + index" :data-menu-trigger="index" class="menu-trigger" role="menuitem" aria-haspopup="menu" :aria-expanded="opened === index" :aria-controls="'menu-panel-' + index" :class="{ active: opened === index }" @click="opened === index ? close() : open(index)" @pointerenter="opened >= 0 && open(index)" @keydown="triggerKey($event, index)">{{ menu.label }}</button>
      <div v-if="opened === index" :id="'menu-panel-' + index" class="workbench-menu-panel" role="menu" :aria-labelledby="'menu-trigger-' + index" @keydown="panelKey">
        <template v-for="(item, itemIndex) in menu.items" :key="itemIndex">
          <hr v-if="item.separator" role="separator">
          <button v-else :role="item.checked !== undefined ? 'menuitemcheckbox' : 'menuitem'" :aria-checked="item.checked" :disabled="item.disabled" @click="run(item)"><span class="menu-check"><Check v-if="item.checked" :size="14"/></span><span>{{ item.label }}</span><kbd v-if="item.shortcut">{{ item.shortcut }}</kbd></button>
        </template>
      </div>
    </div>
  </nav>
</template>
