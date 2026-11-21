<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import { ChevronUp, ChevronDown, X } from '@lucide/vue'
import { state, sendTerminalInput, type Workspace } from '../workbench'
import { terminalDirectory } from '../terminal-directory'
import { terminalTheme } from '../terminal-theme'
import '@xterm/xterm/css/xterm.css'

const props = defineProps<{ session: Workspace; active: boolean; visible: boolean; dark: boolean; fontSize: number }>()
const sessionId = props.session.id
const container = ref<HTMLDivElement>(), searchOpen = ref(false), query = ref('')
const searchInput = ref<HTMLInputElement>(), noMatches = ref(false)
let terminal: Terminal, fit: FitAddon, search: SearchAddon, observer: ResizeObserver, unsubscribe: () => void
const bytes = (data: string) => Uint8Array.from(atob(data), c => c.charCodeAt(0))
function resize(): void { if (!terminal || !props.visible || !container.value?.clientWidth) return; try { fit.fit() } catch { /* detached while changing tabs */ } }

function findMatch(previous = false, incremental = false): void {
  if (!query.value) {
    noMatches.value = false
    search.clearDecorations()
    terminal.clearSelection()
    return
  }
  noMatches.value = !(previous ? search.findPrevious(query.value) : search.findNext(query.value, { incremental }))
}

async function openSearch(): Promise<void> {
  if (!searchOpen.value) {
    searchOpen.value = true
    if (query.value) findMatch(false, true)
  }
  await nextTick()
  searchInput.value?.focus()
  searchInput.value?.select()
}

function closeSearch(): void {
  searchOpen.value = false
  search.clearDecorations()
  if (query.value) terminal.clearSelection()
  terminal.focus()
}

function onSearchToggle(): void {
  if (!props.active || !props.visible) return
  if (searchOpen.value) closeSearch()
  else void openSearch()
}

function isSearchShortcut(event: KeyboardEvent): boolean {
  return event.ctrlKey && event.shiftKey && !event.altKey && !event.metaKey && event.code === 'KeyF'
}

function handleSearchKey(event: KeyboardEvent): void {
  if (event.isComposing) return
  if (isSearchShortcut(event)) void openSearch()
  else if (event.key === 'Escape') closeSearch()
  else if (event.key === 'Enter' && event.target === searchInput.value) findMatch(event.shiftKey)
  else return
  event.preventDefault()
  event.stopPropagation()
}

function focusActiveControl(): void {
  if (!props.active || !props.visible || document.querySelector('dialog[open]')) return
  if (searchOpen.value) searchInput.value?.focus()
  else terminal?.focus()
}

onMounted(() => {
  terminal = new Terminal({
    cursorBlink: true, fontSize: props.fontSize, fontFamily: "'JetBrains Mono', 'Cascadia Code', 'SF Mono', Consolas, monospace", scrollback: 10000,
    theme: terminalTheme(props.dark), minimumContrastRatio: 4.5, allowProposedApi: false,
  })
  fit = new FitAddon(); search = new SearchAddon(); terminal.loadAddon(fit); terminal.loadAddon(search)
  terminal.open(container.value!)
  const history = [...props.session.terminalBuffer]
  let replaying = history.length > 0
  terminal.parser.registerOscHandler(7, sequence => {
    const directory = terminalDirectory(sequence)
    if (!replaying && directory && props.session.id === sessionId) props.session.terminalDirectory = directory
    return true
  })
  for (const [index, data] of history.entries()) terminal.write(bytes(data), index === history.length - 1 ? () => { replaying = false } : undefined)
  let promptEnd: { row: number; col: number } | undefined
  terminal.parser.registerOscHandler(133, data => {
    const b = terminal.buffer.active
    promptEnd = data.split(';')[0] === 'B' ? { row: b.baseY + b.cursorY, col: b.cursorX } : undefined
    return false
  })
  terminal.onWriteParsed(() => {
    if (props.session.id !== sessionId || replaying) return
    const b = terminal.buffer.active, row = b.baseY + b.cursorY, line = b.getLine(row)
    const prefix = line?.translateToString(true, 0, b.cursorX) ?? ''
    const marked = promptEnd?.row === row && promptEnd.col === b.cursorX
    props.session.terminalAtPrompt = b.type === 'normal' && !!line && !line.translateToString(true, b.cursorX) &&
      (marked || /[$#%❯➜]$/.test(prefix.trimEnd()))
  })
  terminal.buffer.onBufferChange(buffer => { if (props.session.id === sessionId) props.session.terminalAlternate = buffer.type === 'alternate' })
  terminal.onData(data => { if (props.session.id === sessionId && props.session.phase === 'connected') sendTerminalInput(props.session, data) })
  terminal.onResize(({ cols, rows }) => window.sshTool.terminalResize(sessionId, cols, rows))
  terminal.attachCustomKeyEventHandler(event => {
    if (isSearchShortcut(event) && !event.isComposing) {
      event.preventDefault()
      if (event.type === 'keydown') void openSearch()
      return false
    }
    if (event.type === 'keydown' && event.key === 'Escape' && searchOpen.value && !event.isComposing) {
      event.preventDefault()
      closeSearch()
      return false
    }
    return true
  })
  unsubscribe = window.sshTool.onEvent(event => {
    if (event.type === 'terminal' && event.sessionId === sessionId) terminal.write(bytes(event.data), () => { if (event.sequence) window.sshTool.ackTerminal?.(sessionId, event.sequence) })
    if (event.type === 'state' && event.sessionId === sessionId && event.phase === 'connected') {
      resize(); window.sshTool.terminalResize(sessionId, terminal.cols, terminal.rows)
      focusActiveControl()
    }
  })
  window.addEventListener('sshtool:terminal-search', onSearchToggle)
  window.sshTool.terminalConsumer?.(sessionId, true)
  observer = new ResizeObserver(resize); observer.observe(container.value!); resize()
})
watch(query, () => { if (searchOpen.value) findMatch(false, true) })
watch(() => props.active, async value => { if (value) { await nextTick(); resize(); focusActiveControl() } })
watch(() => props.visible, async value => { if (value) { await nextTick(); resize() } })
watch(() => props.dark, value => { if (terminal) terminal.options.theme = terminalTheme(value) })
watch(() => props.fontSize, async value => { if (terminal) { terminal.options.fontSize = value; await nextTick(); resize() } })
watch(() => state.settings.uiZoom, resize, { flush: 'post' })
onBeforeUnmount(() => { window.removeEventListener('sshtool:terminal-search', onSearchToggle); window.sshTool.terminalConsumer?.(sessionId, false); observer?.disconnect(); unsubscribe?.(); terminal?.dispose() })
</script>
<template>
  <div class="terminal-pane">
    <div v-if="searchOpen" class="terminal-search" @keydown="handleSearchKey">
      <input ref="searchInput" v-model="query" aria-label="查找终端输出" placeholder="查找终端输出（Enter 下一个，Esc 关闭）" autocomplete="off" spellcheck="false">
      <button class="icon-button" type="button" aria-label="上一个匹配" title="上一个匹配（Shift+Enter）" :disabled="!query" @click="findMatch(true)"><ChevronUp :size="15"/></button>
      <button class="icon-button" type="button" aria-label="下一个匹配" title="下一个匹配（Enter）" :disabled="!query" @click="findMatch()"><ChevronDown :size="15"/></button>
      <button class="icon-button" type="button" aria-label="关闭查找" title="关闭查找（Esc）" @click="closeSearch"><X :size="15"/></button>
      <span role="status" class="muted">{{ noMatches ? '无匹配结果' : '' }}</span>
    </div>
    <div class="terminal-container">
      <div ref="container" class="terminal-host"></div>
    </div>
  </div>
</template>
