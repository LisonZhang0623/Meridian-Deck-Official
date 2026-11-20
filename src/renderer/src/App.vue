<script setup lang="ts">
import { computed, nextTick, provide, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { Terminal, Server, Plus, X, Settings, Trash2, Search, FolderTree, ArrowUp, HardDrive, LocateFixed, RefreshCw, Upload, FilePlus, FolderPlus, Plug, Unplug, ShieldCheck, AlertTriangle, LoaderCircle, FileText, Image, Download, Pencil, Copy, Columns2, Rows2, SquareTerminal } from '@lucide/vue'
import { version } from '../../../package.json'
import lightAppIcon from './assets/meridian-deck-icon.png'
import darkAppIcon from './assets/meridian-deck-icon-dark.png'
import ConnectionDialog from './components/ConnectionDialog.vue'
import TerminalPane from './components/TerminalPane.vue'
import TerminalSplitDialog from './components/TerminalSplitDialog.vue'
import TerminalSplitDivider from './components/TerminalSplitDivider.vue'
import TerminalSplitDrop from './components/TerminalSplitDrop.vue'
import { useFileTabDrag, fileWindows, tabDrag, showTabDrag, clearTabDrag } from './document-tab-drag'
import { useTabMenu } from './tab-context-menu'
import TabDragFeedback from './components/TabDragFeedback.vue'
import WindowControls from './components/WindowControls.vue'
import WorkbenchMenu from './components/WorkbenchMenu.vue'
import SidebarRail from './components/SidebarRail.vue'
import SidebarResize from './components/SidebarResize.vue'
import FileUploadMenu from './components/FileUploadMenu.vue'
import GpuPanel from './components/GpuPanel.vue'
import SettingsDialog from './components/SettingsDialog.vue'
import StartupArtwork from './components/StartupArtwork.vue'
import ConfirmationDialog from './components/ConfirmationDialog.vue'
import { confirmAction, pendingConfirmation, resolveConfirmation } from './confirmation'
import UpdateDialog from './components/UpdateDialog.vue'
import CredentialsDialog from './components/CredentialsDialog.vue'
import EditorPane from './components/EditorPane.vue'
import { useFileSelection } from './file-selection'
import FileTree from './components/FileTree.vue'
import NasPanel from './components/NasPanel.vue'
import ServerList from './components/ServerList.vue'
import CommandsPanel from './components/CommandsPanel.vue'
import LogsPanel from './components/LogsPanel.vue'
import ForwardsPanel from './components/ForwardsPanel.vue'
import ResourcesPanel from './components/ResourcesPanel.vue'
import FtpDialog from './components/FtpDialog.vue'
import AuthChallengeDialog from './components/AuthChallengeDialog.vue'
import EnvironmentDialog from './components/EnvironmentDialog.vue'
import FileSyncDialog from './components/FileSyncDialog.vue'
import { uploadFolder, downloadFolder, removeRemoteTree } from './file-actions'
import { useTerminalLayout } from './terminal-layout'
import { logTargets } from './logTargets'
import TransferPanel from './components/TransferPanel.vue'
import { state, active, selectedProfile, currentDocument, workspaceRequest, saveProfile, initialize, attempt, unwrap, notify, requestConnection, closeSession, closeSessions, newTerminal, renameSession, sessionTooltip, openDirectoryTerminal, openTmuxTerminal, requestDragDownload, confirmTransfer, loadDirectory, enterDirectory, refreshFileTree, navigate, jumpToTerminalDirectory, parentPath, openFile, closeDocument, closeDocuments, setLayout, phaseLabels, answerHostKey, sizeLabel, type DocumentTab, type Workspace } from './workbench'
import { useFileTreeRefresh } from './file-tree-refresh'
import { connectionIdentity } from '../../shared/connection'
import type { NasDirectory, Profile, RemoteEntry, UpdateRelease } from '../../shared/types'
import './menu.css'
import { useFileDrop } from './useFileDrop'
import { nasPathLabel, useNasDrag, type NasDrag } from './nas-drag'
import { compareReleaseVersions } from './release-version'

const props = defineProps<{ captionWindow?: typeof import('@wailsio/runtime').Window; startupAnimation?: 'echo' | 'orbit' }>()
const motionPreference = matchMedia('(prefers-reduced-motion: reduce)')
const animationFinished = ref(motionPreference.matches)
const showStartup = computed(() => props.startupAnimation && !state.fatalError && (!animationFinished.value || state.loading))
let startupTimer: number | undefined
function finishStartup() { animationFinished.value = true }
function motionChanged(event: MediaQueryListEvent) { if (event.matches) finishStartup() }
function toggleWindowSize() {
  const win = props.captionWindow
  if (win) void attempt(() => win.ToggleMaximise())
}
const api = window.sshTool
const settingsDialogOpen = ref(false)
const updateDialogOpen = ref(false), updateChecking = ref(false), updateOpenError = ref('')
const updateError = ref<{ message: string; code?: string } | null>(null)
const updateRelease = ref<UpdateRelease | null>(null)
const releasesPage = 'https://github.com/LisonZhang0623/Meridian-Deck-Official/releases'
const ftpDialogOpen = ref(false)
const environmentSession = ref<Workspace>()
const syncTarget = ref<{ session: Workspace; directory: string } | null>(null)
const workspaceContent = ref<HTMLElement>()
const { followDirectory, split, shown: shownSplit, openSplit, inSplit, focusPane, splitChoice, splitTargets, canCreateSplit, splitBusy, closeSplitChoice, confirmSplit, splitRatio, setSplitRatio, splitDragSource, splitDragTarget, startSplitDrag, moveSplitDrag, finishSplitDrag, cancelSplitDrag, consumeSplitDragClick } = useTerminalLayout(() => workspaceContent.value)
const sidebarWidth = computed({
  get: () => state.settings.sidebarWidth,
  set: width => { state.settings.sidebarWidth = width }
})
const sessionStrip = ref<HTMLElement>()
const directoryInput = ref(''), pathEditing = ref(false), pathInputEl = ref<HTMLInputElement>()
const systemDark = ref(matchMedia('(prefers-color-scheme: dark)').matches)
const dark = computed(() => state.settings.theme === 'dark' || state.settings.theme === 'system' && systemDark.value)
const appIcon = computed(() => dark.value ? darkAppIcon : lightAppIcon)
const activeTransfers = computed(() => state.transfers.filter(t => t.status === 'running' || t.status === 'queued'))
const transferSpeed = computed(() => activeTransfers.value.reduce((sum, t) => sum + (t.status === 'running' ? t.speed : 0), 0))
const breadcrumbs = computed(() => {
  const dir = active.value?.directory || ''
  if (!dir) return []
  if (dir === '/' || dir === '.') return [{ label: dir, path: dir }]
  const absolute = dir.startsWith('/')
  const parts = dir.split('/').filter(Boolean)
  const items: { label: string; path: string }[] = absolute ? [{ label: '/', path: '/' }] : []
  let acc = absolute ? '' : ''
  for (const part of parts) {
    acc = absolute ? `${acc}/${part}` : acc ? `${acc}/${part}` : part
    items.push({ label: part, path: acc })
  }
  return items
})
async function beginPathEdit(): Promise<void> {
  if (!fileReady.value) return
  pathEditing.value = true
  await nextTick()
  pathInputEl.value?.focus()
  pathInputEl.value?.select()
}
async function submitPathEdit(): Promise<void> {
  pathEditing.value = false
  await attempt(() => navigate(directoryInput.value))
}
const dragFileTab = useFileTabDrag()
function startFileDrag(event: PointerEvent, doc: DocumentTab) {
  if (!api.openEditorWindow) return
  const strip = (event.currentTarget as HTMLElement).closest('.document-tabs')?.getBoundingClientRect()
  if (!strip) return
  const outside = (e: PointerEvent) => e.clientX < strip.left - 10 || e.clientX > strip.right + 10 ||
    e.clientY < strip.top - 10 || e.clientY > strip.bottom + 10
  dragFileTab(event, e => {
    if (e) showTabDrag('workbench', doc.path, { x: e.clientX, y: e.clientY }, 'detach', outside(e))
    else clearTabDrag('workbench')
  }, end => {
    if (outside(end)) {
      void attempt(async () => { await fileWindows.get(doc)?.() })
    }
  })
}
function triggerTerminalSearch(): void {
  if (active.value && !shownSplit.value) active.value.activeDocument = 'terminal'
  void nextTick(() => window.dispatchEvent(new CustomEvent('sshtool:terminal-search')))
}
const fileReady = computed(() => active.value?.phase === 'connected' && !active.value?.sftpError)
const fileDropEnabled = computed(() => fileReady.value && !state.settings.sidebarCollapsed && state.settings.sidebarPanel === 'files')
useFileTreeRefresh(fileDropEnabled)
const selection = useFileSelection(path => {
  const session = active.value
  if (!session || !path) return []
  let visible = path
  while (visible !== session.directory) {
    const parent = parentPath(visible)
    if (parent === visible || !session.expanded.has(visible) || !session.tree[parent]?.some(row => row.path === visible)) return []
    visible = parent
  }
  return session.tree[path] || []
})
provide('file-selection', selection)
watch([active, () => active.value?.id, () => active.value?.directory, fileReady], () => selection.reset(), { flush: 'sync' })
watch(() => active.value?.tree[selection.directory.value], () => selection.prune())
const nasAvailable = !!(api.nasSelect && api.nasList && api.nasUpload)
const nasOpen = ref(false)
const nasPanel = ref<InstanceType<typeof NasPanel>>()
const nasView = ref<NasDirectory | null>(null)
const nasDropMode = computed(() => nasOpen.value && fileDropEnabled.value && !!api.nasDownload)
const nasDropReady = computed(() => nasDropMode.value && !!nasView.value)
const filesDropZone = ref<HTMLElement>()
const fileDropKey = computed(() => JSON.stringify([active.value?.id, active.value?.connectionKey, active.value?.directory]))
const { dragging, dragOver, dragLeave, acceptDrop, nativeEnded, reset: resetDrag } = useFileDrop(() => ({ enabled: fileDropEnabled.value, key: fileDropKey.value }), () => filesDropZone.value)
type PanelDrag = (NasDrag & { direction: 'upload' }) | { direction: 'download'; sessionId: string; rootId: string; paths: string[] }
type PanelDestination = { direction: 'upload' | 'download'; path: string; label: string; sessionId: string; rootId: string }
const { dragging: nasDrag, start: startPanelDrag, cancel: cancelNasDrag } = useNasDrag<PanelDrag, PanelDestination>(panelDestination, (files, destination) => void attempt(() => completePanelDrag(files, destination)))
const uploadTarget = computed(() => nasDrag.value?.destination?.direction === 'upload' ? nasDrag.value.destination : undefined)
const downloadTarget = computed(() => nasDrag.value?.destination?.direction === 'download' ? nasDrag.value.destination : undefined)
watch([fileDropEnabled, fileDropKey, nasOpen, nasView], cancelNasDrag, { flush: 'sync' })
const unsubscribeDrag = api.onEvent(event => { if (event.type === 'drag-ended') nativeEnded(event.sessionId, event.token) })
onBeforeUnmount(() => { unsubscribeDrag(); resolveConfirmation(false) })
const trustDialog = ref<HTMLDialogElement>(), trustBusy = ref(false)
const menu = ref<{ x: number; y: number; entry: RemoteEntry; session: Workspace } | null>(null)
type TabMenuTarget = { kind: 'session'; session: Workspace } | { kind: 'file'; session: Workspace; doc: DocumentTab }
const { tabMenu, tabMenuPanel, openTabMenu, tabMenuKey } = useTabMenu<TabMenuTarget>(() => { menu.value = null })
const tabMenuActions = computed(() => {
  const target = tabMenu.value?.target
  if (!target) return []
  const tabs = target.kind === 'session' ? state.sessions : target.session.documents.filter(doc => !doc.busy)
  const current = target.kind === 'session' ? target.session : target.doc
  return [
    ...(target.kind === 'file' && api.openEditorWindow ? [{ key: 'detach' as const, label: '在独立窗口打开', disabled: false }] : []),
    { key: 'current', label: '关闭当前标签', disabled: !tabs.some(tab => tab === current) },
    { key: 'others', label: '关闭其他标签', disabled: !tabs.some(tab => tab !== current) },
    { key: 'all', label: '关闭所有标签', disabled: !tabs.length }
  ] as const
})
const nameDialog = ref<HTMLDialogElement>(), nameInput = ref<HTMLInputElement>(), actionValue = ref(''), actionTitle = ref(''), actionError = ref(''), actionBusy = ref(false)
let actionSubmit: ((value: string) => Promise<void>) | undefined
const media = matchMedia('(prefers-color-scheme: dark)')
const onTheme = (event: MediaQueryListEvent) => systemDark.value = event.matches
function closeMenu(): void { menu.value = null; tabMenu.value = null }
const beforeUnload = (event: BeforeUnloadEvent) => {
  if (api.nativeFileDrop) return
  const dirty = state.sessions.some(s => s.documents.some(d => d.content !== d.saved))
  const uploading = state.transfers.some(t => ['running', 'queued'].includes(t.status))
  if (dirty || uploading) { event.preventDefault(); event.returnValue = '' }
}
onMounted(() => {
  if (props.startupAnimation) {
    if (!animationFinished.value) startupTimer = window.setTimeout(finishStartup, 2200)
    motionPreference.addEventListener('change', motionChanged)
  }
  void initialize().then(() => {
    if (!state.fatalError && state.settings.checkUpdatesOnStartup) void checkUpdate(false)
  })
  media.addEventListener('change', onTheme)
  window.addEventListener('click', closeMenu)
  window.addEventListener('beforeunload', beforeUnload)
  window.addEventListener('sshtool:files-dropped', nativeDrop)
})
onBeforeUnmount(() => { window.clearTimeout(startupTimer); motionPreference.removeEventListener('change', motionChanged) })
onBeforeUnmount(() => { media.removeEventListener('change', onTheme); window.removeEventListener('click', closeMenu); window.removeEventListener('beforeunload', beforeUnload); window.removeEventListener('sshtool:files-dropped', nativeDrop) })
watch(() => state.sessions.some(s => s.documents.some(d => d.content !== d.saved)), dirty => api.workspaceDirty?.(dirty), { immediate: true, flush: 'sync' })
watch(() => [active.value?.id, active.value?.directory] as const, ([, directory]) => directoryInput.value = directory || '')
watch(() => state.activeId, async id => {
  closeMenu()
  await nextTick()
  if (id !== state.activeId) return
  sessionStrip.value?.querySelector('.session-tab.active')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
})
watch(() => { const pending = state.trust[0]; return pending ? JSON.stringify([pending.sessionId, pending.host, pending.fingerprint]) : '' }, async () => { await nextTick(); if (state.trust[0] && !trustDialog.value?.open) trustDialog.value?.showModal(); if (!state.trust[0]) trustDialog.value?.close() })
function busy(session: Workspace): boolean { return ['connecting','verifying','authenticating'].includes(session.phase) }
function showUpdateDialog(): void {
  updateOpenError.value = ''
  updateDialogOpen.value = true
}
async function checkUpdate(manual = true): Promise<void> {
  if (!api.checkUpdate) return
  if (manual) showUpdateDialog()
  if (updateChecking.value) return
  updateChecking.value = true
  updateError.value = null
  try {
    const result = await api.checkUpdate()
    if (!result.ok) {
      updateRelease.value = null
      updateError.value = { message: result.error, code: result.code }
      return
    }
    const release = result.value
    const newer = compareReleaseVersions(release.version, version) > 0
    const tag = 'v' + release.version.replace(/^v/, '')
    if (newer && release.url !== `${releasesPage}/tag/${tag}`) throw new Error('无法识别发布页地址。')
    updateRelease.value = newer ? release : null
  } catch (error) {
    updateRelease.value = null
    updateError.value = { message: (error as Error).message || '请稍后重试。' }
  } finally { updateChecking.value = false }
}
async function openUpdatePage(): Promise<void> {
  if (!api.openUpdateRelease) return
  updateOpenError.value = ''
  try { unwrap(await api.openUpdateRelease(updateRelease.value?.url || releasesPage)) }
  catch (error) { updateOpenError.value = '无法打开 GitHub 发布页。' + ((error as Error).message || '') }
}
function editProfile(profile?: Profile): void { state.connectionDialog = { profile } }
async function collapseSidebar(): Promise<void> {
  const panel = state.settings.sidebarPanel
  await attempt(() => setLayout({ sidebarCollapsed: true }))
  await nextTick()
  document.getElementById('sidebar-toggle-' + panel)?.focus()
}
function reconnect(session: Workspace): void { const profile = state.profiles.find(p => p.id === session.profileId); if (profile) requestConnection(profile, session.id) }
function connectionChanged(session: Workspace): boolean {
  const profile = state.profiles.find(p => p.id === session.profileId)
  return !!profile && session.connectionKey !== connectionIdentity(profile)
}
async function disconnect(session: Workspace): Promise<void> { unwrap(await api.disconnect(session.id)) }
async function trust(accept: boolean): Promise<void> {
  const pending = state.trust[0]; if (!pending || trustBusy.value) return
  trustBusy.value = true
  try { await answerHostKey(pending, accept) }
  catch (error) { notify((error as Error).message) }
  finally { trustBusy.value = false }
}
function showContext(event: MouseEvent, entry: RemoteEntry): void { closeMenu(); if (active.value) menu.value = { x: Math.min(event.clientX, innerWidth - 225), y: Math.max(8, Math.min(event.clientY, innerHeight - 375)), entry, session: active.value } }
function runTabAction(action: 'detach' | 'current' | 'others' | 'all'): void {
  const context = tabMenu.value
  tabMenu.value = null
  if (!context) return
  context.trigger.focus()
  const { target } = context
  if (action === 'detach') {
    if (target.kind === 'file') void attempt(async () => { await fileWindows.get(target.doc)?.() })
    return
  }
  if (target.kind === 'session') {
    void attempt(() => action === 'current' ? closeSession(target.session) : closeSessions(action === 'others' ? target.session : undefined))
  } else if (action === 'current') void closeDocument(target.session, target.doc)
  else void closeDocuments(target.session, action === 'others' ? target.doc : undefined)
}
async function openName(title: string, initial: string, submit: (value: string) => Promise<void>, selectionEnd = initial.length): Promise<void> { actionTitle.value = title; actionValue.value = initial; actionError.value = ''; actionSubmit = submit; await nextTick(); nameDialog.value?.showModal(); nameInput.value?.focus(); nameInput.value?.setSelectionRange(0, selectionEnd) }
function renameTerminal(session = active.value): void {
  if (session) void openName('重命名终端', session.name, async name => renameSession(session, name))
}
function pickSession(event: Event): void {
  const picker = event.target as HTMLSelectElement
  if (state.sessions.some(session => session.id === picker.value)) state.activeId = picker.value
}
async function sessionKey(event: KeyboardEvent, session: Workspace): Promise<void> {
  const index = state.sessions.indexOf(session), count = state.sessions.length
  if (index < 0 || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const target = event.key === 'Home' ? 0 : event.key === 'End' ? count - 1 : (index + (event.key === 'ArrowRight' ? 1 : count - 1)) % count
  const id = state.sessions[target].id
  state.activeId = id
  await nextTick()
  await nextTick()
  if (state.activeId === id) sessionStrip.value?.querySelector<HTMLButtonElement>('.session-tab.active .session-select')?.focus()
}
async function submitName(): Promise<void> {
  if (actionBusy.value) return
  actionBusy.value = true
  try { await actionSubmit?.(actionValue.value); nameDialog.value?.close() } catch (error) { actionError.value = (error as Error).message }
  finally { actionBusy.value = false }
}
async function createEntry(directory: boolean): Promise<void> {
  const s = active.value; if (!s) return
  await openName(directory ? '新建文件夹' : '新建文件', '', async name => { unwrap(await api.createEntry(s.id, s.directory, name, directory)); await loadDirectory(s, s.directory, true) })
}
async function showTmux(): Promise<void> {
  const session = active.value
  if (!session) return
  await openName('打开或接回 tmux 会话', session.tmuxName || 'sshtool', name => openTmuxTerminal(session, name))
}
async function toggleFollow(): Promise<void> {
  followDirectory.value = !followDirectory.value
  if (followDirectory.value) await setLayout({ sidebarPanel: 'files', sidebarCollapsed: false })
}
async function importConfig(): Promise<void> {
  if (!api.importConfig) return
  const result = unwrap(await api.importConfig())
  if (!result) return
  state.profiles = result.profiles
  state.settings = result.settings
  if (!state.selectedProfileId) state.selectedProfileId = result.profiles[0]?.id || ''
  notify(`配置已导入，新增 ${result.added} 个连接`)
}
async function exportConfig(): Promise<void> {
  if (api.exportConfig && unwrap(await api.exportConfig())) notify('连接与界面偏好已导出')
}
async function renameEntry(session: Workspace, entry: RemoteEntry) {
  const request = workspaceRequest(session)
  const dot = entry.name.lastIndexOf('.')
  await openName('重命名', entry.name, async name => {
    if (!request.current() || session.phase !== 'connected') throw new Error('连接已更改，请重新选择文件')
    unwrap(await api.renameEntry(request.id, entry.path, name))
    if (request.current()) await loadDirectory(session, parentPath(entry.path), true)
  }, entry.type !== 'directory' && dot > 0 ? dot : entry.name.length)
}
async function deleteEntries(session: Workspace, entries: RemoteEntry[]) {
  if (!entries.length) return
  const request = workspaceRequest(session), root = session.directory
  const current = () => request.current() && active.value === session && fileReady.value && session.directory === root
  const names = entries.slice(0, 5).map(row => row.path)
  if (entries.length > names.length) names.push('…另有 ' + (entries.length - names.length) + ' 项')
  if (!await confirmAction({ title: entries.length === 1 ? '删除远程条目？' : '删除选中的 ' + entries.length + ' 项？', message: names.join('\n'), action: '删除', danger: true })) return
  const failed: string[] = []
  for (const row of entries) {
    if (!current()) break
    const result = await api.deleteEntry(request.id, row.path, row.type === 'directory')
    if (!result.ok) failed.push(row.name + '：' + result.error)
  }
  if (current()) await loadDirectory(session, parentPath(entries[0].path), true)
  if (failed.length) notify('删除失败 ' + failed.length + ' 项 · ' + failed.join('，'))
}
function fileShortcut(event: KeyboardEvent) {
  const session = active.value
  if (!session || !fileReady.value || document.querySelector('dialog[open]') || !(event.target instanceof HTMLElement)) return
  const row = event.target.closest<HTMLElement>('[data-entry]')
  if (!row && event.target !== event.currentTarget) return
  const dir = row?.dataset.parent || session.directory
  const selected = selection.selected.value
  const single = selected.length === 1 ? selected[0] : undefined
  let run: (() => void | Promise<void>) | undefined
  if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'a') run = () => selection.all(dir)
  else if (event.altKey && event.key === 'ArrowUp') run = () => enterDirectory(session, parentPath(session.directory))
  else if (!event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey) {
    switch (event.key) {
      case 'Delete': run = () => deleteEntries(session, selected); break
      case 'F2': run = () => single && renameEntry(session, single); break
      case 'Enter': run = () => single && (single.type === 'directory' ? enterDirectory(session, single.path) : openFile(session, single)); break
      case 'F5': run = () => refreshFileTree(session); break
      case 'Escape': run = () => selection.reset(); break
    }
  }
  if (run) {
    event.preventDefault()
    event.stopPropagation()
    if (!event.repeat) void attempt(async () => { await run() })
  }
}
async function fileAction(action: string): Promise<void> {
  const context = menu.value; menu.value = null; if (!context) return
  const { entry, session } = context
  if (action === 'open') { await openFile(session, entry); return }
  if (action === 'enter') { await enterDirectory(session, entry.path); return }
  if (action === 'copy') { await navigator.clipboard.writeText(entry.path); notify('已复制远程路径'); return }
  if (action === 'terminal') { await openDirectoryTerminal(session, entry.path); return }
  if (action === 'startup') {
    const profile = state.profiles.find(item => item.id === session.profileId)
    if (!profile || connectionChanged(session)) throw new Error('连接配置已变更，请重新连接')
    await saveProfile({ ...profile, initialPath: entry.path }, {})
    notify('已设置启动目录，下次连接时生效')
    return
  }
  if (action === 'drag') { await requestDragDownload(session, entry); return }
  if (action === 'log') { logTargets[session.id] = entry.path; await setLayout({ sidebarPanel: 'logs', sidebarCollapsed: false }); return }
  if (action === 'download') {
    if (entry.type === 'directory') { await downloadFolder(session, entry.path); return }
    unwrap(await api.download(session.id, entry.path)); state.transferOpen = true; return
  }
  if (action === 'deleteTree') { await removeRemoteTree(session, entry.path); return }
  if (action === 'rename') await renameEntry(session, entry)
  if (action === 'delete') await deleteEntries(session, [entry])
}
async function upload(paths?: string[]): Promise<void> { const s = active.value; if (!s) return; unwrap(await api.upload(s.id, s.directory, paths)); state.transferOpen = true }
function startNasUpload(event: PointerEvent, files: NasDrag): void { startPanelDrag(event, { direction: 'upload', ...files }) }
function startNasDownload(event: PointerEvent, entry: RemoteEntry): void {
  const session = active.value, view = nasView.value
  if (nasDropReady.value && session && view) startPanelDrag(event, { direction: 'download', sessionId: session.id, rootId: view.rootId, paths: [entry.path] })
}
function panelDestination(x: number, y: number, files: PanelDrag): PanelDestination | undefined {
  if (!fileDropEnabled.value) return
  const target = document.elementFromPoint(x, y), session = active.value, view = nasView.value
  if (!target || !session || !view || files.rootId !== view.rootId) return
  if (files.direction === 'upload') {
    if (!filesDropZone.value?.contains(target)) return
    const path = target.closest<HTMLElement>('[data-nas-directory]')?.dataset.nasDirectory ?? session.directory
    return { direction: 'upload', path, label: `${session.name} · ${path}`, sessionId: session.id, rootId: view.rootId }
  }
  if (files.sessionId !== session.id || !nasDropReady.value || !target.closest('#nas-source-panel')) return
  const path = target.closest<HTMLElement>('[data-nas-local-directory]')?.dataset.nasLocalDirectory ?? view.path
  return { direction: 'download', path, label: nasPathLabel(view.root, path), sessionId: session.id, rootId: view.rootId }
}
async function completePanelDrag(files: PanelDrag, target: PanelDestination): Promise<void> {
  const session = active.value, view = nasView.value
  if (!session || session.id !== target.sessionId || !view || view.rootId !== target.rootId || !fileDropEnabled.value) return
  const key = fileDropKey.value
  if (files.direction === 'upload' && target.direction === 'upload') {
    if (!await confirmTransfer('上传', files.paths.map(path => nasPathLabel(view.root, path)), target.label)) return
    if (fileDropKey.value === key && nasOpen.value && nasView.value?.rootId === target.rootId) await uploadNas(files, target.path)
  } else if (files.direction === 'download' && target.direction === 'download' && api.nasDownload) {
    if (!await confirmTransfer('下载', files.paths.map(path => `${session.name} · ${path}`), target.label)) return
    if (fileDropKey.value !== key || nasView.value?.rootId !== target.rootId || !nasDropReady.value) return
    unwrap(await api.nasDownload(target.sessionId, files.paths, target.rootId, target.path))
    state.transferOpen = true
    nasPanel.value?.refresh()
  }
}
async function uploadNas(files: NasDrag, directory?: string): Promise<void> {
  const session = active.value
  if (!session || !fileDropEnabled.value || !api.nasUpload) return
  const id = session.id, path = directory ?? session.directory
  unwrap(await api.nasUpload(id, path, files.rootId, files.paths))
  state.transferOpen = true
  if (session.id === id && session.phase === 'connected') {
    if (path === session.directory || session.expanded.has(path)) await loadDirectory(session, path, true)
    else delete session.tree[path]
  }
}
async function drop(event: DragEvent): Promise<void> {
  if (api.nativeFileDrop) {
    // Wails resolves local file paths from the bubbling drop event.
    resetDrag()
    return
  }
  event.stopPropagation()
  if (!acceptDrop(event)) return
  const paths = [...(event.dataTransfer?.files || [])].map(file => api.filePath(file)).filter(Boolean)
  const session = active.value
  if (!session || !paths.length) return
  const key = fileDropKey.value
  if (await confirmTransfer('上传', paths, `${session.name} · ${session.directory}`) && fileDropKey.value === key && fileDropEnabled.value) {
    await attempt(() => upload(paths))
  }
}
function nativeDrop(event: Event): void {
  resetDrag()
  const session = active.value, tokens: unknown = (event as CustomEvent).detail
  if (!session || !fileDropEnabled.value || !Array.isArray(tokens) || !tokens.length || !tokens.every(token => typeof token === 'string')) return
  const id = session.id, directory = session.directory
  void attempt(async () => {
    if (api.uploadDropped) {
      if (!unwrap(await api.uploadDropped(id, directory, tokens))) return
    } else {
      unwrap(await api.upload(id, directory, tokens))
    }
    state.transferOpen = true
    if (session.id === id && session.phase === 'connected') await loadDirectory(session, directory, true)
  })
}
</script>

<template>
  <Transition name="startup">
    <div v-if="showStartup && startupAnimation" class="startup-screen" :data-theme="dark ? 'dark' : 'light'" @click="finishStartup">
      <StartupArtwork :variant="startupAnimation" :dark="dark" />
    </div>
  </Transition>
  <div v-show="!showStartup" class="app-shell" :data-theme="dark ? 'dark' : 'light'" :class="{ 'sidebar-right': state.settings.sidebarSide === 'right', 'sidebar-hidden': state.settings.sidebarCollapsed }" @dragover.prevent @drop.prevent>
    <header class="topbar" :class="{ 'custom-titlebar': captionWindow }">
      <div class="brand" @dblclick="toggleWindowSize"><img draggable="false" :src="appIcon" width="19" height="19" alt=""/><strong>Meridian Deck</strong></div>
      <span class="brand-divider" aria-hidden="true"></span>
      <WorkbenchMenu @create-connection="editProfile()" @edit-connection="editProfile(selectedProfile)" @open-settings="settingsDialogOpen = true" @check-update="checkUpdate()" :split="!!split" :follow-directory="followDirectory" @split-terminal="openSplit" @rename-terminal="renameTerminal()" @unsplit="split = null" @open-tmux="attempt(showTmux)" @toggle-follow="attempt(toggleFollow)" @import-config="attempt(importConfig)" @export-config="attempt(exportConfig)" @open-ftp="ftpDialogOpen = true"/>
      <span v-if="captionWindow" class="caption-drag-space" aria-hidden="true" @dblclick="toggleWindowSize"></span>
      <div class="topbar-right">
        <template v-if="active">
          <div class="host-capsule" :title="sessionTooltip(active)">
            <span class="connection-dot" :data-phase="active.phase" aria-hidden="true"></span>
            <span class="host-capsule-endpoint mono">{{ active.endpoint }}</span>
            <span v-if="active.phase !== 'connected'" class="host-capsule-phase" :data-phase="active.phase">{{ phaseLabels[active.phase] }}</span>
          </div>
          <button class="icon-button" type="button" title="连接设置" aria-label="连接设置" :disabled="busy(active)" @click="editProfile(selectedProfile)"><Settings :size="14"/></button>
          <button v-if="active.phase === 'connected'" class="icon-button danger" type="button" title="断开当前连接" aria-label="断开当前连接" @click="attempt(() => disconnect(active!))"><Unplug :size="14"/></button>
          <button v-else-if="busy(active)" class="topbar-compact-btn" type="button" @click="attempt(() => disconnect(active!))">取消连接</button>
          <button v-else class="primary topbar-compact-btn" type="button" @click="reconnect(active)"><Plug :size="13"/>{{ connectionChanged(active) ? '新标签连接' : '重新连接' }}</button>
        </template>
        <button v-if="updateRelease" type="button" class="update-notice" @click="showUpdateDialog">新版本 {{ updateRelease.version }}</button>
      </div>
      <WindowControls v-if="captionWindow" :native-window="captionWindow" />
    </header>
    <nav class="session-tabs" aria-label="服务器会话">
      <div ref="sessionStrip" class="session-strip">
        <div v-for="session in state.sessions" :key="session.id" class="session-tab" :class="{ active: state.activeId === session.id }" @contextmenu="openTabMenu($event, { kind: 'session', session })" @keydown="openTabMenu($event, { kind: 'session', session })">
          <button class="session-select" type="button" :title="sessionTooltip(session)" :aria-current="state.activeId === session.id ? 'true' : undefined" :aria-label="`${session.name}，${phaseLabels[session.phase]}`" :class="{ 'session-draggable': session.phase === 'connected', 'session-dragging': splitDragSource === session }" @click="!consumeSplitDragClick($event) && (state.activeId = session.id)" @dblclick="!consumeSplitDragClick($event) && renameTerminal(session)" @keydown="sessionKey($event, session)" @pointerdown="startSplitDrag($event, session)" @pointermove="moveSplitDrag" @pointerup="finishSplitDrag" @pointercancel="cancelSplitDrag" @lostpointercapture="cancelSplitDrag">
            <span class="connection-dot" :data-phase="session.phase" aria-hidden="true"></span><span>{{ session.name }}</span>
          </button>
          <button class="tab-close" type="button" :aria-label="'关闭 ' + session.name" @click="attempt(() => closeSession(session))"><X :size="12"/></button>
        </div>
        <span v-if="!state.sessions.length" class="session-placeholder">连接服务器后，会话将在这里显示</span>
      </div>
      <div class="session-tab-actions">
        <select v-if="state.sessions.length > 1" class="session-picker" aria-label="切换终端" title="切换终端" :value="state.activeId" @change="pickSession">
          <option disabled value="">切换终端</option>
          <option v-for="session in state.sessions" :key="session.id" :value="session.id">{{ session.name }} · {{ session.endpoint }}</option>
        </select>
        <button class="new-session icon-button" type="button" aria-label="新建终端" title="新建终端（Ctrl+Shift+T）" :disabled="!selectedProfile" @click="newTerminal()"><Plus :size="16"/></button>
      </div>
    </nav>
    <div class="app-body">
      <aside v-show="!state.settings.sidebarCollapsed" id="workbench-sidebar" class="sidebar" :class="{ 'gpu-sidebar': state.settings.sidebarPanel === 'gpu', 'tools-sidebar': ['commands','logs','forwards','resources'].includes(state.settings.sidebarPanel) }" :style="{ '--sidebar-width': sidebarWidth ? `${sidebarWidth}px` : undefined }">
        <section id="sidebar-panel-servers" v-show="state.settings.sidebarPanel === 'servers'" class="servers-section" aria-label="服务器"><div class="section-heading"><span><Server :size="15"/>服务器</span><div class="sidebar-heading-actions"><button class="icon-button" title="新建连接" @click="editProfile()"><Plus :size="16"/></button><button class="icon-button" title="收起服务器面板" aria-label="收起服务器面板" @click="collapseSidebar"><X :size="15"/></button></div></div><ServerList/></section>
        <section ref="filesDropZone" id="sidebar-panel-files" :data-file-drop-target="fileReady ? '' : undefined" v-show="state.settings.sidebarPanel === 'files'" class="files-section" aria-label="远程文件" :class="{ 'nas-drop-active': uploadTarget }" @dragenter="dragOver" @dragover="dragOver" @dragleave="dragLeave" @drop.prevent="drop">
          <div class="section-heading"><span><FolderTree :size="15"/>远程文件</span>
            <div class="sidebar-heading-actions">
              <button v-if="nasAvailable" class="nas-toggle" type="button" title="NAS 文件夹" aria-controls="nas-source-panel" :aria-expanded="nasOpen" @click="nasOpen = !nasOpen"><HardDrive :size="13"/>NAS</button>
              <button class="icon-button" type="button" title="跳转到当前终端目录" aria-label="跳转到当前终端目录" :disabled="!fileReady" @click="attempt(jumpToTerminalDirectory)"><LocateFixed :size="14"/></button>
              <button class="icon-button" title="刷新目录 (F5)" aria-label="刷新目录" :disabled="!fileReady" @click="active && attempt(() => refreshFileTree(active!))"><RefreshCw :size="14" :class="{ spin: active?.loading.size }"/></button>
              <button class="icon-button" title="收起文件面板" aria-label="收起文件面板" @click="collapseSidebar"><X :size="15"/></button>
            </div>
          </div>
          <div class="file-tools">
            <button class="icon-button" title="上级目录 (Alt + ↑)" aria-label="上级目录" :disabled="!fileReady" @click="attempt(() => navigate(parentPath(active!.directory)))"><ArrowUp :size="14"/></button>
            <button class="icon-button" title="在当前目录打开新终端" aria-label="在当前目录打开新终端" :disabled="!fileReady" @click="active && attempt(() => openDirectoryTerminal(active!, active!.directory))"><Terminal :size="14"/></button>
            <FileUploadMenu :disabled="!fileDropEnabled" :allow-directory="!!api.uploadDirectory" @files="attempt(() => upload())" @directory="active && attempt(() => uploadFolder(active!, active!.directory))"/>
            <button class="icon-button" title="Bash 环境变量 (.bashrc)" aria-label="Bash 环境变量" :disabled="!fileReady" @click="active && (environmentSession = active)"><Settings :size="14"/></button>
            <button class="icon-button" title="新建文件" aria-label="新建文件" :disabled="!fileReady" @click="createEntry(false)"><FilePlus :size="14"/></button>
            <button class="icon-button" title="新建文件夹" aria-label="新建文件夹" :disabled="!fileReady" @click="createEntry(true)"><FolderPlus :size="14"/></button>
            <button v-if="api.compareDirectory" class="icon-button" title="对比和同步本地目录" aria-label="对比和同步本地目录" :disabled="!fileReady" @click="active && (syncTarget = { session: active, directory: active.directory })"><FolderTree :size="14"/></button>
          </div>
          <form v-if="pathEditing || !fileReady || !breadcrumbs.length" class="path-input" @submit.prevent="submitPathEdit">
            <input ref="pathInputEl" v-model="directoryInput" aria-label="远程目录" :disabled="!fileReady" placeholder="连接后浏览远程文件" spellcheck="false" @blur="pathEditing = false" @keydown.esc="pathEditing = false">
          </form>
          <div v-else class="path-breadcrumb" role="navigation" aria-label="远程目录路径" :title="active?.directory" @click="beginPathEdit">
            <div class="path-breadcrumb-segs mono">
              <template v-for="(seg, idx) in breadcrumbs" :key="seg.path">
                <span v-if="idx > 0 && breadcrumbs[idx - 1].label !== '/'" class="path-sep">/</span>
                <button type="button" class="path-seg" :class="{ current: idx === breadcrumbs.length - 1 }" @click.stop="attempt(() => navigate(seg.path))">{{ seg.label }}</button>
              </template>
            </div>
            <button type="button" class="icon-button path-edit-btn" aria-label="编辑远程目录路径" title="手动输入路径" @click.stop="beginPathEdit"><Pencil :size="11"/></button>
          </div>
          <p v-if="followDirectory && fileReady" class="follow-status">{{ active?.terminalDirectory ? '正在跟随终端目录' : '等待 Shell 报告当前目录（OSC 7）' }}</p>
          <div class="tree-scroll" tabindex="0" aria-label="远程文件列表" @keydown="fileShortcut"><FileTree v-if="active && active.tree[active.directory]" :session="active" :path="active.directory" :nas-drop-target="uploadTarget?.path" :nas-drop-mode="nasDropMode" @context="showContext" @drag-to-nas="startNasDownload"/><div v-else class="side-empty">{{ active?.sftpError || (fileReady ? '正在读取目录…' : '连接后浏览远程文件。') }}</div></div>
          <div v-if="active && !fileReady && active.tree[active.directory]" class="cached-note">连接已断开 · 显示上次目录记录</div>
          <div v-if="uploadTarget" class="nas-drop-status" :title="uploadTarget.label"><Upload :size="14"/><span>上传到 {{ uploadTarget.path }}</span></div>
          <div v-else-if="dragging" class="drop-overlay"><Upload :size="25"/>拖放文件或文件夹到当前目录</div>
        </section>
        <NasPanel v-if="nasOpen" v-show="state.settings.sidebarPanel === 'files'" ref="nasPanel" :upload-ready="fileDropEnabled" :drop-target="downloadTarget" @close="nasOpen = false" @view="nasView = $event" @drag="startNasUpload" @upload="files => attempt(() => uploadNas(files))"/>
        <GpuPanel @close="collapseSidebar" />
        <CommandsPanel v-if="!state.settings.sidebarCollapsed && state.settings.sidebarPanel === 'commands'" @close="collapseSidebar" />
        <LogsPanel v-if="!state.settings.sidebarCollapsed && state.settings.sidebarPanel === 'logs'" @close="collapseSidebar" />
        <ForwardsPanel v-if="!state.settings.sidebarCollapsed && state.settings.sidebarPanel === 'forwards'" @close="collapseSidebar" />
        <ResourcesPanel v-if="!state.settings.sidebarCollapsed && state.settings.sidebarPanel === 'resources'" @close="collapseSidebar" />
      </aside>
      <SidebarResize v-if="!state.settings.sidebarCollapsed" v-model="sidebarWidth" :side="state.settings.sidebarSide" @commit="width => attempt(() => setLayout({ sidebarWidth: width }))"/>
      <main class="main-workspace">
        <nav v-if="active" class="document-tabs" :data-dock-target="tabDrag?.action === 'dock' ? (tabDrag.ready ? 'ready' : 'available') : undefined" aria-label="终端与文件">
          <div class="document-tabs-strip">
            <button class="document-tab" :class="{ active: active.activeDocument === 'terminal' }" @click="active.activeDocument = 'terminal'"><Terminal :size="13"/>终端</button>
            <div v-for="doc in active.documents" v-show="!doc.detached" :key="doc.path" class="document-tab" :class="{ active: active.activeDocument === doc.path }" @contextmenu="openTabMenu($event, { kind: 'file', session: active!, doc })" @keydown="openTabMenu($event, { kind: 'file', session: active!, doc })">
              <button aria-haspopup="menu" :class="{ 'preview-tab-draggable': !!api.openEditorWindow }" @pointerdown="startFileDrag($event, doc)" @click="!doc.detached && (active!.activeDocument = doc.path)"><Image v-if="doc.preview.kind === 'image'" :size="13"/><FileText v-else :size="13"/><span :title="doc.path">{{ doc.path.split('/').pop() }}</span><span v-if="doc.content !== doc.saved" class="amber" aria-label="未保存"> ●</span></button>
              <button class="tab-close" aria-label="关闭文件" @click="closeDocument(active!, doc)"><X :size="12"/></button>
            </div>
          </div>
          <div class="document-tabs-actions">
            <button class="icon-button" type="button" title="左右分屏" aria-label="左右分屏" @click="openSplit('horizontal')"><Columns2 :size="14"/></button>
            <button class="icon-button" type="button" title="上下分屏" aria-label="上下分屏" @click="openSplit('vertical')"><Rows2 :size="14"/></button>
            <button v-if="api.newTmuxTerminal" class="icon-button" type="button" title="打开或接回 tmux 会话" aria-label="打开或接回 tmux 会话" :disabled="active.phase !== 'connected'" @click="attempt(showTmux)"><SquareTerminal :size="14"/></button>
            <button class="icon-button" type="button" title="查找终端输出（Ctrl+Shift+F）" aria-label="查找终端输出" @click="triggerTerminalSearch"><Search :size="14"/></button>
          </div>
        </nav>
        <div ref="workspaceContent" class="workspace-content" :class="shownSplit && ['terminal-split', 'terminal-split-' + shownSplit.direction]" :style="shownSplit ? { '--split-first': `${splitRatio}fr`, '--split-second': `${100 - splitRatio}fr` } : undefined">
          <div v-if="state.loading" class="welcome"><LoaderCircle class="spin" :size="28"/><h2>正在加载工作台</h2></div>
          <div v-else-if="state.fatalError" class="welcome"><AlertTriangle :size="30"/><h2>无法打开工作台</h2><p class="danger">{{ state.fatalError }}</p></div>
          <div v-else-if="!active" class="welcome welcome-home">
            <img class="welcome-mark" :src="appIcon" width="104" height="104" alt=""/>
            <h1>子午线</h1>
            <p>
              一线通山海<br/>
              <small>SSH 终端、文件传输与服务器监控</small>
            </p>
            <div class="welcome-actions">
              <button v-if="selectedProfile" class="primary" @click="requestConnection(selectedProfile)">
                <Plug :size="18"/>连接服务器
              </button>
              <button :class="{ primary: !selectedProfile }" @click="editProfile()">
                <Plus :size="18"/>新建 SSH 连接
              </button>
            </div>
            <small v-if="selectedProfile">
              {{ selectedProfile.name }} · <span class="mono">{{ selectedProfile.username }}@{{ selectedProfile.host }}:{{ selectedProfile.port }}</span>
            </small>
          </div>
          <section v-for="session in state.sessions" v-show="session.id === state.activeId || inSplit(session)" :key="session.id" class="session-workspace" :data-terminal-session="session.id" :class="{ 'split-pane': inSplit(session), 'split-first': shownSplit?.first === session.id, 'split-second': shownSplit?.second === session.id, 'split-active': inSplit(session) && session.id === state.activeId }" @focusin="focusPane(session)">
            <button v-if="inSplit(session)" class="split-pane-label" type="button" :aria-pressed="session.id === state.activeId" :title="sessionTooltip(session)" @click="focusPane(session)"><strong>{{ session.name }}</strong><span>{{ session.endpoint }}</span></button>
            <div v-if="session.phase === 'disconnected' || session.phase === 'failed'" class="connection-banner" :class="{ error: session.phase === 'failed' }"><AlertTriangle :size="16"/><div><strong>{{ session.message }}</strong><small v-if="connectionChanged(session)">连接配置已修改，将在新标签中连接。</small></div><button @click="reconnect(session)">{{ connectionChanged(session) ? '在新标签连接' : '重试连接' }}</button></div>
            <div v-if="busy(session)" class="connection-progress"><LoaderCircle :size="15" class="spin"/><span>{{ session.message }}</span><button v-if="session.phase === 'verifying'" @click="trustDialog?.showModal()">查看指纹</button></div>
            <div class="pane-container"><TerminalPane v-show="session.activeDocument === 'terminal' || inSplit(session)" :session="session" :active="session.id === state.activeId && session.activeDocument === 'terminal'" :visible="inSplit(session) || session.id === state.activeId && session.activeDocument === 'terminal'" :dark="dark" :font-size="state.settings.terminalFontSize"/><EditorPane v-for="doc in session.documents" v-show="session.activeDocument === doc.path && !inSplit(session)" :key="doc.path" :session="session" :doc="doc" :dark="dark"/></div>
          </section>
          <TerminalSplitDivider v-if="shownSplit" :direction="shownSplit.direction" :ratio="splitRatio" @resize="setSplitRatio"/>
          <div v-if="state.fileBusy" class="file-loading"><LoaderCircle class="spin" :size="17"/>读取远程文件…</div>
          <TerminalSplitDrop v-if="splitDragSource" :source="splitDragSource" :active="active" :shown="shownSplit" :target="splitDragTarget"/>
        </div>
        <TransferPanel />
      </main>
      <SidebarRail :dark="dark" :has-update="!!updateRelease" @open-settings="settingsDialogOpen = true" />
    </div>
    <footer class="statusbar">
      <span class="status-session"><span v-if="active" class="connection-dot" :data-phase="active.phase" aria-hidden="true"></span>{{ active ? active.name + ' · ' + phaseLabels[active.phase] : '就绪' }}</span>
      <span v-if="active?.directory" class="status-dir mono" :title="active.directory">{{ active.directory }}</span>
      <span v-if="currentDocument">{{ currentDocument.preview.kind === 'text' ? currentDocument.preview.encoding || 'UTF-8' : '图片预览' }}<template v-if="currentDocument.content !== currentDocument.saved"> · <span class="amber">未保存</span></template></span>
      <span class="status-right">
        <button type="button" class="status-transfer-pill" :class="activeTransfers.length ? 'active' : ''" :aria-expanded="state.transferOpen" title="展开或收起传输任务" @click="state.transferOpen = !state.transferOpen">
          <Upload :size="12" aria-hidden="true"/>
          <template v-if="activeTransfers.length">传输中 {{ activeTransfers.length }} 项<template v-if="transferSpeed"> · {{ sizeLabel(transferSpeed) }}/s</template></template>
          <template v-else>传输{{ state.transfers.length ? ` (${state.transfers.length})` : '' }}</template>
        </button>
        <span class="status-divider">|</span>
        <span>{{ state.sessions.length }} 个会话</span>
      </span>
    </footer>
    <div v-if="state.toast" class="toast" role="status"><span>{{ state.toast }}</span><button class="icon-button" aria-label="关闭通知" @click="state.toast = ''"><X :size="14"/></button></div>
    <TabDragFeedback />
    <div v-if="nasDrag" class="pointer-drag-label nas-drag-label" :class="{ 'drop-ready': nasDrag.destination }" :style="{ left: nasDrag.x + 'px', top: nasDrag.y + 'px' }" aria-hidden="true"><component :is="nasDrag.files.direction === 'download' ? Download : Upload" :size="14"/>{{ nasDrag.count }} 项</div>
    <div v-if="tabMenu" ref="tabMenuPanel" class="context-menu" role="menu" tabindex="-1" :aria-label="tabMenu.target.kind === 'session' ? '会话标签' : '文件标签'" :style="{ left: tabMenu.x + 'px', top: tabMenu.y + 'px' }" @click.stop @contextmenu.prevent @keydown="tabMenuKey">
      <button v-for="action in tabMenuActions" :key="action.key" type="button" role="menuitem" tabindex="-1" :disabled="action.disabled" @click="runTabAction(action.key)">{{ action.label }}</button>
    </div>
    <ConnectionDialog v-if="state.connectionDialog" />
    <CredentialsDialog v-if="state.credentialsDialog" />
    <AuthChallengeDialog />
    <FtpDialog v-if="ftpDialogOpen" @close="ftpDialogOpen = false" />
    <EnvironmentDialog v-if="environmentSession" :session="environmentSession" @close="environmentSession = undefined" />
    <FileSyncDialog v-if="syncTarget" :session="syncTarget.session" :directory="syncTarget.directory" @close="syncTarget = null" />
    <ConfirmationDialog v-if="pendingConfirmation" :request="pendingConfirmation" @resolve="resolveConfirmation" />
    <SettingsDialog v-if="settingsDialogOpen" @close="settingsDialogOpen = false" />
    <TerminalSplitDialog v-if="splitChoice" :key="splitChoice.source.id + splitChoice.direction" :source="splitChoice.source" :direction="splitChoice.direction" :targets="splitTargets" :can-create="canCreateSplit" :busy="splitBusy" :confirm="confirmSplit" @close="closeSplitChoice"/>
    <UpdateDialog v-if="updateDialogOpen" :version="version" :release="updateRelease" :checking="updateChecking" :error="updateError" :open-error="updateOpenError" :can-open="!!api.openUpdateRelease" @retry="checkUpdate()" @open="openUpdatePage" @close="updateDialogOpen = false"/>
    <dialog ref="trustDialog" class="trust-dialog" @cancel.prevent="trust(false)"><header class="dialog-header"><div><h2>{{ state.trust[0]?.previous ? '服务器指纹发生变化' : '确认服务器身份' }}</h2><p>核对服务器指纹后再继续连接。</p></div><ShieldCheck :size="24"/></header><div v-if="state.trust[0]" class="dialog-body"><p v-if="state.trust[0].previous" class="error-box">此服务器与上次保存的身份不一致。请向管理员核实密钥更换后再信任。</p><dl class="fingerprint-details"><dt>服务器</dt><dd>{{ state.trust[0].host }}</dd><dt>密钥类型</dt><dd>{{ state.trust[0].keyType }}</dd><template v-if="state.trust[0].previous"><dt>原指纹</dt><dd class="fingerprint">{{ state.trust[0].previous }}</dd></template><dt>当前指纹</dt><dd class="fingerprint">{{ state.trust[0].fingerprint }}</dd></dl></div><footer class="dialog-footer"><button :disabled="trustBusy" @click="trust(false)">取消连接</button><button :class="state.trust[0]?.previous ? 'danger-button' : 'primary'" :disabled="trustBusy" @click="trust(true)">{{ state.trust[0]?.previous ? '替换并信任' : '信任并继续' }}</button></footer></dialog>
    <dialog ref="nameDialog" class="name-dialog"><form @submit.prevent="submitName"><header class="dialog-header"><h2>{{ actionTitle }}</h2><button type="button" class="icon-button" aria-label="关闭" @click="nameDialog?.close()"><X :size="17"/></button></header><div class="dialog-body"><label>名称<input ref="nameInput" v-model="actionValue" required maxlength="255" autocomplete="off"></label><p v-if="actionError" class="error-box">{{ actionError }}</p></div><footer class="dialog-footer"><button type="button" @click="nameDialog?.close()">取消</button><button type="submit" class="primary" :disabled="actionBusy">{{ actionBusy ? '处理中…' : '确定' }}</button></footer></form></dialog>
    <div v-if="menu" class="context-menu" :style="{ left: menu.x + 'px', top: menu.y + 'px' }" @click.stop>
      <button v-if="menu.entry.type === 'directory' || menu.entry.targetType === 'directory'" :disabled="!fileReady" @click="attempt(() => fileAction('enter'))"><FolderTree :size="14"/>进入文件夹</button>
      <button v-if="menu.entry.targetType !== 'directory' && menu.entry.targetType !== 'missing'" :disabled="!fileReady" @click="attempt(() => fileAction('open'))"><component :is="menu.entry.type === 'directory' ? FolderTree : FileText" :size="14"/>{{ menu.entry.type === 'directory' ? (menu.session.expanded.has(menu.entry.path) ? '折叠目录' : '展开目录') : '打开预览' }}</button>
      <button v-if="menu.entry.type === 'directory' || menu.entry.targetType === 'directory'" :disabled="!fileReady" @click="attempt(() => fileAction('terminal'))"><Terminal :size="14"/>在此目录打开终端</button>
      <button v-if="menu.entry.type === 'directory' || menu.entry.targetType === 'directory'" :disabled="!fileReady" @click="attempt(() => fileAction('startup'))"><FolderTree :size="14"/>设为启动目录</button>
      <button v-if="api.nativeDragDownload !== false && (menu.entry.type === 'file' || menu.entry.type === 'directory' && api.prepareDragDirectory)" :disabled="!fileReady" @click="attempt(() => fileAction('drag'))"><Download :size="14"/>准备拖出下载</button>
      <button v-if="menu.entry.type === 'file'" :disabled="!fileReady" @click="attempt(() => fileAction('log'))"><FileText :size="14"/>在日志面板打开</button>
      <button v-if="menu.entry.type !== 'link'" :disabled="!fileReady || menu.entry.type === 'directory' && !api.downloadDirectory" @click="attempt(() => fileAction('download'))"><Download :size="14"/>{{ menu.entry.type === 'directory' ? '下载整个文件夹' : '下载文件' }}</button>
      <button @click="attempt(() => fileAction('copy'))"><Copy :size="14"/>复制远程路径</button><hr>
      <button :disabled="!fileReady" @click="attempt(() => fileAction('rename'))"><Pencil :size="14"/>重命名 (F2)</button>
      <button class="danger" :disabled="!fileReady" @click="attempt(() => fileAction('delete'))"><Trash2 :size="14"/>{{ menu.entry.type === 'link' ? '删除链接' : menu.entry.type === 'directory' ? '删除空文件夹' : '删除文件' }}</button>
      <button v-if="menu.entry.type === 'directory' && api.deleteTree" :disabled="!fileReady" class="danger" @click="attempt(() => fileAction('deleteTree'))"><Trash2 :size="14"/>递归删除文件夹…</button>
    </div>
  </div>
</template>

<style scoped>
.startup-leave-active { transition: opacity 140ms ease-out; pointer-events: none; }
.startup-leave-to { opacity: 0; }
@media (prefers-reduced-motion: reduce) { .startup-leave-active { transition: none; } }
.startup-screen { position: fixed; inset: 0; z-index: 10; display: grid; place-items: center; background: #fff; }
.startup-screen[data-theme="dark"] { background: #0a0c10; }
.topbar.custom-titlebar { padding-block: 0; padding-right: 0; }
.custom-titlebar .brand, .caption-drag-space { --wails-draggable: drag; user-select: none; }
.caption-drag-space { flex: 1; min-width: 12px; align-self: stretch; }
.topbar-right { margin-left: auto; display: flex; align-items: center; gap: 4px; min-width: 0; }
.host-capsule { display: inline-flex; align-items: center; gap: 6px; padding: 2px 9px; border-radius: var(--radius-pill); background: var(--bg-canvas); border: 1px solid var(--border); font-size: 10.5px; max-width: 260px; min-width: 0; }
.host-capsule-endpoint { color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.host-capsule-phase { color: var(--amber); font-size: 10px; white-space: nowrap; }
.host-capsule-phase[data-phase=failed] { color: var(--danger); }
.topbar-compact-btn { min-height: 24px; padding: 2px 8px; font-size: 11px; }
.session-tabs { overflow: visible; }
.session-strip { min-width: 0; flex: 1; display: flex; align-items: center; gap: 5px; overflow-x: auto; padding-bottom: 1px; }
.session-select:focus-visible, .session-tab .tab-close:focus-visible { outline-offset: -2px; }
.session-select.session-draggable { cursor: grab; user-select: none; touch-action: none; }
.session-select.session-dragging { cursor: grabbing; opacity: .62; }
.session-tab-actions { flex-shrink: 0; display: flex; align-items: center; gap: 4px; padding-left: 6px; border-left: 1px solid var(--border); }
.session-tab-actions .new-session { margin: 0; }
.session-picker { min-height: 26px; max-width: 152px; padding: 2px 7px; font-size: 11.5px; text-overflow: ellipsis; }
.path-breadcrumb { display: flex; align-items: center; gap: 4px; margin: 0 8px 6px; padding: 2px 4px 2px 7px; min-height: 28px; border: 1px solid var(--border); border-radius: var(--radius-control); background: var(--bg-canvas); cursor: text; flex-shrink: 0; }
.path-breadcrumb:hover { border-color: var(--blue); }
.path-breadcrumb-segs { flex: 1; min-width: 0; display: flex; align-items: center; gap: 1px; overflow-x: auto; white-space: nowrap; font-size: 11px; }
.path-sep { color: var(--muted); padding: 0 1px; }
.path-seg { border: 0; background: transparent; padding: 1px 4px; min-height: 20px; border-radius: 4px; color: var(--muted); font-size: 11px; }
.path-seg:hover:not(:disabled) { background: var(--highlight); color: var(--text); }
.path-seg.current { color: var(--blue); font-weight: 600; }
.path-edit-btn { min-width: 20px; min-height: 20px; padding: 2px; }
.statusbar > .status-session { display: inline-flex; align-items: center; gap: 6px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.status-dir { max-width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.status-transfer-pill { min-height: 20px; padding: 1px 8px; border: 1px solid transparent; border-radius: var(--radius-pill); background: transparent; color: var(--muted); font-size: 10.5px; gap: 5px; }
.status-transfer-pill:hover:not(:disabled) { background: var(--highlight); color: var(--blue); border-color: transparent; }
.status-transfer-pill.active { background: var(--highlight); color: var(--blue); font-weight: 500; }
.workspace-content.terminal-split { display: grid; background: var(--border); }
.terminal-split-horizontal { grid-template: "first divider second" minmax(0, 1fr) / minmax(0, var(--split-first)) 7px minmax(0, var(--split-second)); }
.terminal-split-vertical { grid-template: "first" minmax(0, var(--split-first)) "divider" 7px "second" minmax(0, var(--split-second)) / minmax(0, 1fr); }
.session-workspace.split-pane { position: relative; inset: auto; min-width: 0; background: var(--bg-canvas); overflow: hidden; }
.split-first { grid-area: first; }
.split-second { grid-area: second; }
.split-pane-label { min-height: 28px; justify-content: flex-start; border: 0; border-radius: 0; background: var(--bg-surface); font-size: 11.5px; border-bottom: 1px solid var(--border-soft); }
.split-pane-label > * { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.split-pane-label > strong { flex-shrink: 0; max-width: 68%; font-weight: 600; }
.split-pane-label > span { color: var(--muted); font-family: var(--font-mono); font-size: 10.5px; }
.split-pane-label:focus-visible { outline-offset: -2px; }
.split-active .split-pane-label { color: var(--blue); border-bottom-color: var(--blue); }
.follow-status { margin: 0 10px 5px; color: var(--muted); font-size: 10.5px; }
.nas-toggle { gap: 4px; min-height: 24px; padding: 2px 7px; font-size: 10.5px; font-weight: 600; border-radius: 5px; background: transparent; border-color: transparent; color: var(--muted); }
.nas-toggle:hover:not(:disabled) { background: var(--highlight); color: var(--blue); }
.nas-toggle[aria-expanded=true] { color: var(--blue); background: var(--highlight); }
.nas-drop-active { outline: 1px dashed var(--blue); outline-offset: -1px; }
.nas-drop-status { position: absolute; z-index: 3; bottom: 8px; inset-inline: 8px; display: flex; align-items: center; gap: 6px; padding: 6px 8px; color: var(--blue); background: var(--bg-surface); border: 1px solid var(--blue); border-radius: var(--radius-control); font-size: 11px; pointer-events: none; }
.nas-drop-status svg { flex-shrink: 0; }
.nas-drop-status span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.nas-drag-label { transform: translate(calc(-100% - 8px), calc(-100% - 8px)); }
.update-notice { flex-shrink: 0; min-height: 24px; padding: 2px 8px; border-radius: var(--radius-pill); border-color: transparent; color: var(--blue); background: var(--highlight); font-size: 10.5px; font-weight: 500; }
@media (max-width: 960px) {
  .host-capsule { max-width: 170px; }
  .status-dir { max-width: 150px; }
}
@media (max-width: 750px) {
  .brand strong, .brand-divider, .host-capsule-endpoint, .status-dir { display: none; }
  .session-picker { max-width: 110px; }
  .welcome { padding: 18px; }
}
</style>
