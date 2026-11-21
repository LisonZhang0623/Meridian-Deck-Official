import { reactive, computed, nextTick } from 'vue'
import type { AppEvent, AuthChallenge, Credentials, DragDownload, Phase, Preview, Profile, RemoteEntry, Result, Settings, Transfer } from '../../shared/types'
import { DEFAULT_SETTINGS } from '../../shared/settings'
import { connectionIdentity } from '../../shared/connection'
import { fileWindows } from './document-tab-drag'
import { confirmAction } from './confirmation'

export interface DocumentTab { detached?: boolean; path: string; preview: Preview; content: string; saved: string; busy: boolean; error: string; conflict: boolean }
export interface Workspace {
  id: string; profileId: string; connectionKey: string; endpoint: string; name: string; phase: Phase; message: string; home: string; directory: string
  terminalNumber: number; customName?: string
  sftpError?: string; activeDocument: string; documents: DocumentTab[]; tree: Record<string, RemoteEntry[]>
  expanded: Set<string>; loading: Set<string>; terminalBuffer: string[]; terminalBytes: number
  terminalDirectory?: string; tmuxName?: string
  terminalInputRevision?: number; terminalInputPending?: boolean; terminalAlternate?: boolean; terminalAtPrompt?: boolean
}
export const state = reactive({
  profiles: [] as Profile[], settings: { ...DEFAULT_SETTINGS },
  sessions: [] as Workspace[], activeId: '', selectedProfileId: '', transfers: [] as Transfer[], transferOpen: false,
  toast: '', fatalError: '', loading: true, trust: [] as Extract<AppEvent, { type: 'hostkey' }>[],
  authChallenges: [] as AuthChallenge[],
  connectionDialog: null as { profile?: Profile } | null,
  credentialsDialog: null as { profile: Profile; reconnectId?: string; targetCredentials?: Credentials } | null,
  fileBusy: false, traySupported: false
})
const dismissedTransferIds = new Set<string>()

export function dismissCompletedTransfers(id?: string): void {
  state.transfers = state.transfers.filter(transfer => {
    if (transfer.status !== 'completed' || (id !== undefined && transfer.id !== id)) return true
    dismissedTransferIds.add(transfer.id)
    return false
  })
}

export function sendTerminalInput(session: Workspace, data: string): void {
  session.terminalInputRevision = (session.terminalInputRevision ?? 0) + 1
  session.terminalInputPending = !/[\r\n\x03\x15]$/.test(data)
  session.terminalAtPrompt = false
  window.sshTool.terminalInput(session.id, data)
}

export function workspaceRequest(session: Workspace) {
  const id = session.id, identity = session.connectionKey
  return { id, current: () => session.id === id && session.connectionKey === identity && state.sessions.some(item => item.id === id && item.connectionKey === identity) }
}
const fileRequests = new Set<{ current: () => boolean }>()
function refreshFileBusy(): void { state.fileBusy = [...fileRequests].some(request => request.current()) }
export const dragDownloads = reactive<Record<string, { status: 'preparing' | 'ready' | 'failed'; file?: DragDownload; error?: string; expiresAt?: number; request?: number }>>({})
let dragRequest = 0
function clearDragDownloads(sessionId: string): void { for (const key of Object.keys(dragDownloads)) if (JSON.parse(key)[0] === sessionId) delete dragDownloads[key] }
export function dragKey(sessionId: string, entry: RemoteEntry): string { return JSON.stringify([sessionId, entry.path, entry.size, entry.modified]) }
export function confirmTransfer(action: string, sources: string[], destination: string): Promise<boolean> {
  const displayed = sources.slice(0, 6).join('\n')
  const remaining = sources.length > 6 ? `\n另有 ${sources.length - 6} 项` : ''
  return confirmAction({
    title: `${action} ${sources.length} 项？`,
    message: `来源\n${displayed}${remaining}\n\n目标\n${destination}`,
    action
  })
}
export async function prepareDragDownload(session: Workspace, entry: RemoteEntry): Promise<void> {
  if (entry.type === 'link' || session.phase !== 'connected' || session.sftpError) throw new Error('请连接服务器并选择文件或文件夹')
  const api = window.sshTool
  const prepare = entry.type === 'directory' ? api.prepareDragDirectory : api.prepareDragDownload
  if (!prepare) throw new Error('当前系统无法准备拖出此项目')
  const id = session.id, key = dragKey(id, entry)
  if (dragDownloads[key]?.status === 'preparing') return
  const request = ++dragRequest, identity = session.connectionKey
  dragDownloads[key] = { status: 'preparing', request }; state.transferOpen = true
  const current = () => dragDownloads[key]?.request === request && state.sessions.some(s => s.id === id && s.phase === 'connected' && s.connectionKey === identity)
  try {
    const file = unwrap(await prepare(id, entry.path))
    if (!current()) return
    dragDownloads[key] = { status: 'ready', file, expiresAt: file.expiresAt, request }
    notify(`“${file.name}”已准备好，可再次拖到资源管理器`)
  } catch (error) {
    if (!current()) return
    dragDownloads[key] = { status: 'failed', error: (error as Error).message, request }
    throw error
  }
}
export async function requestDragDownload(session: Workspace, entry: RemoteEntry): Promise<void> {
  if (entry.type === 'link' || session.phase !== 'connected' || session.sftpError) return
  const api = window.sshTool
  const request = workspaceRequest(session)
  const native = api.nativeDragDownload !== false && (entry.type === 'file' || !!api.prepareDragDirectory)
  const cached = dragDownloads[dragKey(request.id, entry)]
  if (native && cached?.status === 'preparing') return
  if (native && cached?.status === 'ready' && (cached.expiresAt || 0) > Date.now()) {
    notify('已准备好，可拖到资源管理器')
    return
  }
  const destination = native ? '本机缓存，准备好后再拖到资源管理器' : '本机，下一步选择保存位置'
  if (!await confirmTransfer('下载', [`${session.name} · ${entry.path}`], destination)) return
  if (!request.current() || session.phase !== 'connected' || session.sftpError) return
  if (native) {
    await prepareDragDownload(session, entry)
  } else if (entry.type === 'directory' && api.downloadDirectory) {
    const tasks = unwrap(await api.downloadDirectory(request.id, entry.path))
    if (tasks === 0) notify('本地文件夹已创建')
    if (tasks && tasks > 0) state.transferOpen = true
  } else if (entry.type === 'file') {
    unwrap(await api.download(request.id, entry.path))
    state.transferOpen = true
  }
}
export function dragRemoteFile(event: DragEvent, session: Workspace, entry: RemoteEntry): void {
  event.preventDefault()
  if (session.phase !== 'connected' || session.sftpError || entry.type === 'link') return
  const key = dragKey(session.id, entry), cached = dragDownloads[key]
  if (cached?.status === 'ready' && cached.file && (cached.expiresAt || 0) > Date.now()) {
    const detail = { sessionId: session.id, token: cached.file.token }
    if (entry.type === 'directory') delete dragDownloads[key]
    window.dispatchEvent?.(new CustomEvent('sshtool:drag-out-start', { detail }))
    try { window.sshTool.startDragDownload(session.id, cached.file.token) }
    catch (error) { window.dispatchEvent?.(new CustomEvent('sshtool:drag-out-failed', { detail })); notify('无法开始拖出：' + (error as Error).message) }
  }
  else {
    void attempt(() => requestDragDownload(session, entry))
  }
}
export async function retryTransfer(transfer: Transfer): Promise<void> {
  if (transfer.purpose !== 'drag') { unwrap(await window.sshTool.retryTransfer(transfer.id)); return }
  const session = state.sessions.find(s => s.id === transfer.sessionId)
  if (!session || session.phase !== 'connected') throw new Error('请先连接原服务器，再重新准备拖出下载')
  const request = workspaceRequest(session)
  const source = transfer.dragRoot || transfer.remotePath
  const parent = parentPath(source)
  await loadDirectory(session, parent, true)
  if (!request.current() || session.phase !== 'connected') return
  const entry = session.tree[parent]?.find(e => e.path === source)
  if (!entry || entry.type !== (transfer.dragRoot ? 'directory' : 'file')) throw new Error('远程项目已不存在，请刷新文件树')
  await requestDragDownload(session, entry)
}
export const active = computed(() => state.sessions.find(s => s.id === state.activeId))
export const selectedProfile = computed(() => state.profiles.find(p => p.id === (active.value?.profileId || state.selectedProfileId)))
export const currentDocument = computed(() => active.value?.documents.find(d => d.path === active.value?.activeDocument))
let toastTimer: ReturnType<typeof setTimeout>
export function notify(message: string): void {
  if (!message || message === state.toast) return
  state.toast = message
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => state.toast = '', 6000)
}
export function unwrap<T>(result: Result<T>): T { if (!result.ok) throw Object.assign(new Error(result.error), { code: result.code }); return result.value }
export async function answerHostKey(pending: Extract<AppEvent, { type: 'hostkey' }>, accept: boolean): Promise<void> {
  const api = window.sshTool
  unwrap(await (api.trustHostKey ? api.trustHostKey(pending.sessionId, pending.host, pending.fingerprint, accept) : api.trust(pending.sessionId, accept)))
  state.trust = state.trust.filter(t => t.sessionId !== pending.sessionId || t.host !== pending.host || t.fingerprint !== pending.fingerprint)
}
export async function attempt<T>(task: () => Promise<T>): Promise<T | undefined> { try { return await task() } catch (error) { notify((error as Error).message) } }
export function newProfile(): Profile { return { id: '', name: '', host: '', port: 22, username: '', group: '开发环境', authType: 'password', keyPath: '', initialPath: '', timeout: 20, keepalive: true, rememberCredentials: false } }
export async function initialize(): Promise<void> {
  window.sshTool.onEvent(onEvent)
  try { const data = unwrap(await window.sshTool.bootstrap()); state.profiles = data.profiles; state.settings = { ...DEFAULT_SETTINGS, ...data.settings }; state.traySupported = data.traySupported === true; state.selectedProfileId = data.profiles[0]?.id || '' }
  catch (error) { state.fatalError = (error as Error).message }
  finally { state.loading = false }
}
function onEvent(event: AppEvent): void {
  if (event.type === 'drag-ended') return
  if (event.type === 'log' || event.type === 'forwards') return
  if (event.type === 'profiles') { state.profiles = event.profiles; return }
  if (event.type === 'notice') {
    if (event.dragToken) for (const key of Object.keys(dragDownloads)) if (dragDownloads[key].file?.token === event.dragToken) delete dragDownloads[key]
    notify(event.message); return
  }
  if (event.type === 'transfer') {
    if (dismissedTransferIds.has(event.transfer.id)) return
    const index = state.transfers.findIndex(t => t.id === event.transfer.id)
    if (index < 0) state.transfers.unshift(event.transfer); else state.transfers[index] = event.transfer
    if (event.transfer.status === 'completed' && event.transfer.direction === 'upload') {
      const session = state.sessions.find(s => s.id === event.transfer.sessionId)
      if (session?.phase === 'connected') {
        const destination = parentPath(event.transfer.remotePath)
        if (destination === session.directory || session.expanded.has(destination)) void loadDirectory(session, destination, true)
        else delete session.tree[destination]
        // Directory uploads can create ancestors of the uploaded file.
        const root = session.directory, prefix = root.replace(/\/+$/, '') + '/'
        if (destination !== root && destination.startsWith(prefix)) {
          let ancestor = parentPath(destination)
          while (ancestor !== '/' && ancestor.startsWith(prefix) && !session.expanded.has(ancestor)) ancestor = parentPath(ancestor)
          void loadDirectory(session, ancestor.startsWith(prefix) ? ancestor : root, true)
        }
      }
    }
    return
  }
  const session = state.sessions.find(s => s.id === event.sessionId)
  if (!session) return
  if (event.type === 'terminal') {
    session.terminalBuffer.push(event.data); session.terminalBytes += event.data.length
    while (session.terminalBytes > 2 * 1024 * 1024 && session.terminalBuffer.length > 1) session.terminalBytes -= session.terminalBuffer.shift()!.length
  } else if (event.type === 'hostkey') {
    state.trust = state.trust.filter(t => t.sessionId !== event.sessionId); state.trust.push(event)
  } else if (event.type === 'auth-challenge') {
    state.authChallenges = state.authChallenges.filter(challenge => challenge.sessionId !== event.sessionId)
    state.authChallenges.push(event)
  } else {
    if (event.phase !== 'connected') clearDragDownloads(session.id)
    session.phase = event.phase; session.message = event.message
    refreshFileBusy()
    if (event.phase !== 'verifying') state.trust = state.trust.filter(t => t.sessionId !== event.sessionId)
    if (event.phase !== 'authenticating') state.authChallenges = state.authChallenges.filter(challenge => challenge.sessionId !== event.sessionId)
    if (event.phase === 'connected') {
      session.sftpError = event.sftpError
      if (event.startupNotice) notify(event.startupNotice)
      session.home = event.home || '.'; session.directory = session.home
      session.tree = {}; session.expanded.clear()
      if (!event.sftpError) void loadDirectory(session, session.home)
      else notify(event.sftpError)
    }
  }
}
export async function saveProfile(profile: Profile, credentials: Credentials): Promise<Profile> {
  const saved = unwrap(await window.sshTool.saveProfile({ ...profile }, { ...credentials }))
  const index = state.profiles.findIndex(p => p.id === saved.id)
  if (index < 0) state.profiles.push(saved); else state.profiles[index] = saved
  state.selectedProfileId = saved.id
  return saved
}
export function requestConnection(profile: Profile, reconnectId?: string): void {
  const current = active.value
  const key = connectionIdentity(profile)
  const matches = (session: Workspace) => session.phase === 'connected' && session.connectionKey === key
  const source = current && matches(current) ? current : state.sessions.find(matches)
  if (source) {
    void attempt(() => connect(profile, {}, reconnectId, source.id))
  } else {
    void attempt(async () => {
      if (needsLoginCredentials(profile) || profileJumpHops(profile).some(hop => needsLoginCredentials(hop))) state.credentialsDialog = { profile, reconnectId }
      else await connect(profile, {}, reconnectId)
    })
  }
}

export function profileJumpHops(profile: Profile): Profile[] {
  const hops: Profile[] = [], seen = new Set([profile.id])
  for (let id = profile.jumpProfileId; id;) {
    if (seen.has(id)) throw new Error('跳板机配置存在循环引用')
    if (hops.length === 5) throw new Error('最多支持 5 跳 SSH 跳板机')
    const hop = state.profiles.find(candidate => candidate.id === id)
    if (!hop) throw new Error('跳板机配置不存在')
    hops.push(hop); seen.add(id); id = hop.jumpProfileId
  }
  return hops
}

export function needsLoginCredentials(profile: Profile, provided: Credentials = {}): boolean {
  return !profile.hasCredentials && (profile.authType === 'password' && provided.password === undefined || profile.authType === 'key' && provided.passphrase === undefined)
}

function openSSHConnection(id: string, profile: Profile, expected: string, credentials: Credentials, legacyJump?: Credentials, hopCredentials?: Record<string, Credentials>, tmuxName?: string): Promise<Result<void>> {
  const api = window.sshTool, hops = profileJumpHops(profile)
  if (tmuxName) {
    if (!api.connectTmux) throw new Error('当前版本不支持接回 tmux 会话')
    const supplied = hopCredentials || (hops.length === 1 && legacyJump ? { [hops[0].id]: legacyJump } : {})
    return api.connectTmux(id, profile.id, credentials, supplied, tmuxName, expected)
  }
  if (hops.length > 1) {
    if (!api.connectChain) throw new Error('当前版本不支持多跳 SSH 连接')
    return api.connectChain(id, profile.id, credentials, hopCredentials || {}, expected)
  }
  const jump = hops.length ? hopCredentials?.[hops[0].id] || legacyJump || {} : undefined
  return api.connect(id, profile.id, credentials, jump, expected)
}
export function newTerminal(): void {
  if (selectedProfile.value) requestConnection(selectedProfile.value)
  else notify('请先在“连接”菜单中添加服务器，或选择已有服务器')
}
export function renameSession(session: Workspace, value: string): void {
  const name = value.trim()
  if (!name || name.length > 180 || /[\x00-\x1f\x7f]/.test(name)) throw new Error('请输入 1–180 个字符的终端名称')
  if (state.sessions.includes(session) && name !== session.name) session.name = session.customName = name
}
export function sessionTooltip(session: Workspace): string {
  const lines = [session.name, session.endpoint]
  if (session.terminalDirectory) lines.push(`终端目录 ${session.terminalDirectory}`)
  else if (session.directory !== '.') lines.push(`文件目录 ${session.directory}`)
  return lines.join('\n')
}
export async function openDirectoryTerminal(session: Workspace, path: string): Promise<void> {
  if (session.phase !== 'connected') throw new Error('请先连接服务器')
  const profile = state.profiles.find(p => p.id === session.profileId)
  if (!profile || session.connectionKey !== connectionIdentity(profile)) throw new Error('连接配置已变化，请重新连接服务器')
  await connect(profile, {}, undefined, session.id, path)
}
export async function openTmuxTerminal(session: Workspace, name: string): Promise<void> {
  const profile = state.profiles.find(p => p.id === session.profileId)
  if (!profile || session.phase !== 'connected' || session.connectionKey !== connectionIdentity(profile)) throw new Error('请先连接当前服务器')
  if (!window.sshTool.newTmuxTerminal) throw new Error('当前桌面后端不支持 tmux 集成')
  if (!/^[a-zA-Z0-9_][a-zA-Z0-9_-]{0,63}$/.test(name)) throw new Error('请输入 1–64 个字母、数字、下划线或短横线，开头不能是短横线')
  await connect(profile, {}, undefined, session.id, undefined, undefined, undefined, name)
}
export async function connect(profile: Profile, credentials: Credentials, reconnectId?: string, sourceId?: string, initialPath?: string, jumpCredentials?: Credentials, hopCredentials?: Record<string, Credentials>, tmuxName?: string): Promise<Workspace | undefined> {
  let session = state.sessions.find(s => s.id === reconnectId)
  if (session && ['connecting', 'verifying', 'authenticating', 'connected'].includes(session.phase)) return
  const connectionKey = connectionIdentity(profile)
  if (session?.connectionKey !== connectionKey) session = undefined
  if (session && !tmuxName) tmuxName = session.tmuxName
  if (!session) {
    const host = profile.host.includes(':') && !profile.host.startsWith('[') ? `[${profile.host}]` : profile.host
    const terminalNumber = state.sessions.reduce((max, candidate) => candidate.profileId === profile.id ? Math.max(max, candidate.terminalNumber) : max, 0) + 1
    session = reactive<Workspace>({ id: crypto.randomUUID(), profileId: profile.id, connectionKey, endpoint: `${profile.username}@${host}:${profile.port}`, name: profile.name, terminalNumber, phase: 'connecting', message: '正在连接', home: '.', directory: '.', activeDocument: 'terminal', documents: [], tree: {}, expanded: new Set<string>(), loading: new Set<string>(), terminalBuffer: [], terminalBytes: 0 })
    state.sessions.push(session)
  } else {
    const previousId = session.id
    session.id = crypto.randomUUID()
    clearDragDownloads(previousId)
    state.trust = state.trust.filter(item => item.sessionId !== previousId)
    state.authChallenges = state.authChallenges.filter(item => item.sessionId !== previousId)
    session.loading.clear()
    session.sftpError = undefined
    session.terminalDirectory = undefined
    session.terminalInputRevision = 0
    session.terminalInputPending = false
    session.terminalAlternate = false
    session.terminalAtPrompt = false
    for (const doc of session.documents) doc.busy = false
  }
  session.tmuxName = tmuxName
  session.name = session.customName || [profile.name, tmuxName, `终端 ${session.terminalNumber}`].filter(Boolean).join(' · ')
  session.phase = 'connecting'; session.message = sourceId ? '正在新建终端' : '正在连接服务器'; session.activeDocument = 'terminal'
  state.activeId = session.id; state.selectedProfileId = profile.id
  refreshFileBusy()
  const request = workspaceRequest(session)
  await nextTick()
  if (!request.current()) return
  try {
    let result: Result<void>
    if (sourceId && tmuxName) result = await window.sshTool.newTmuxTerminal!(request.id, sourceId, profile.id, tmuxName)
    else if (sourceId) result = initialPath === undefined ? await window.sshTool.newTerminal(request.id, sourceId, profile.id) : await window.sshTool.newTerminal(request.id, sourceId, profile.id, initialPath)
    else result = await openSSHConnection(request.id, profile, connectionKey, credentials, jumpCredentials, hopCredentials, tmuxName)
    if (!request.current()) return
    unwrap(result)
    return session
  } catch (error) {
    if (!request.current()) return
    session.phase = 'failed'; session.message = (error as Error).message || String(error)
    throw error
  }
}
const closingSessions = new Set<string>()
function isSessionRunning(session: Workspace): boolean {
  return ['connected', 'connecting', 'verifying', 'authenticating'].includes(session.phase)
}
export async function closeSession(session: Workspace): Promise<void> {
  if (!state.sessions.includes(session) || closingSessions.has(session.id)) return
  const request = workspaceRequest(session)
  const consequences = [
    isSessionRunning(session) && '将断开 SSH 连接。',
    session.documents.some(doc => doc.content !== doc.saved) && '未保存的文件修改将丢失。'
  ].filter(Boolean)
  if (consequences.length && !await confirmAction({
    title: '关闭会话标签？', message: consequences.join('\n'), action: '关闭', danger: true
  })) return
  if (request.current() && !closingSessions.has(request.id)) await disconnectWorkspace(session)
}
export async function closeSessions(keep?: Workspace): Promise<void> {
  const sessions = state.sessions.filter(session => session !== keep && !closingSessions.has(session.id))
  if (!sessions.length) return
  const connections = sessions.filter(isSessionRunning).length
  const dirtyFiles = sessions.reduce((count, session) => count + session.documents.filter(doc => doc.content !== doc.saved).length, 0)
  const consequences = [connections && `将断开 ${connections} 个 SSH 连接。`, dirtyFiles && `将丢弃 ${dirtyFiles} 个文件的未保存修改。`].filter(Boolean)
  const targets = sessions.map(session => ({ session, request: workspaceRequest(session) }))
  if (consequences.length && !await confirmAction({
    title: `关闭 ${sessions.length} 个会话标签？`, message: consequences.join('\n'), action: '关闭', danger: true
  })) return
  const current = targets.filter(({ request }) => request.current() && !closingSessions.has(request.id))
  const results = await Promise.allSettled(current.map(({ session }) => disconnectWorkspace(session)))
  const failed = results.find(result => result.status === 'rejected')
  if (failed) throw failed.reason
}
async function disconnectWorkspace(session: Workspace): Promise<void> {
  const request = workspaceRequest(session)
  closingSessions.add(request.id)
  try {
    const result = await window.sshTool.disconnect(request.id)
    if (!request.current()) return
    unwrap(result)
  } catch (error) { if (request.current()) throw error; return }
  finally { closingSessions.delete(request.id) }
  const closingIndex = state.sessions.indexOf(session)
  state.sessions = state.sessions.filter(s => s.id !== request.id)
  clearDragDownloads(request.id)
  state.trust = state.trust.filter(t => t.sessionId !== request.id)
  state.authChallenges = state.authChallenges.filter(challenge => challenge.sessionId !== request.id)
  if (state.activeId === request.id) state.activeId = (state.sessions[closingIndex] || state.sessions[closingIndex - 1])?.id || ''
  refreshFileBusy()
}
export async function removeProfile(profile: Profile): Promise<void> {
  if (state.sessions.some(s => s.profileId === profile.id)) { notify('请先关闭该服务器的会话标签'); return }
  if (!await confirmAction({ title: '删除已保存的连接？', message: profile.name, action: '删除', danger: true })) return
  if (!state.profiles.some(item => item.id === profile.id) || state.sessions.some(session => session.profileId === profile.id)) return
  unwrap(await window.sshTool.deleteProfile(profile.id)); state.profiles = state.profiles.filter(p => p.id !== profile.id)
  state.selectedProfileId = state.profiles[0]?.id || ''
}
export async function setLayout(patch: Partial<Settings>): Promise<void> { Object.assign(state.settings, patch); unwrap(await window.sshTool.saveSettings({ ...state.settings })) }
interface DirectoryListing { path: string; entries: RemoteEntry[] }
interface DirectoryRead { id: string; promise?: Promise<DirectoryListing | undefined>; next?: Promise<DirectoryListing | undefined> }
interface HardLinkScan { id: string; startedAt: number; names?: Set<string>; pending?: boolean }
const directoryReads = new WeakMap<Workspace, Map<string, DirectoryRead>>()
const directoryNavigations = new WeakMap<Workspace, symbol>()
const hardLinkScans = new WeakMap<Workspace, Map<string, HardLinkScan>>()
let runningHardLinkScans = 0

function cacheDirectory(session: Workspace, path: string, listing: DirectoryListing, background: boolean): void {
  const request = workspaceRequest(session)
  const { listHardLinks } = window.sshTool
  let scans = hardLinkScans.get(session)
  if (!scans) { scans = new Map(); hardLinkScans.set(session, scans) }
  let scan = scans.get(listing.path)
  if (scan?.id !== request.id) scan = undefined
  const entries = listing.entries.map(entry => entry.type === 'file' && scan?.names?.has(entry.name) ? { ...entry, hardLink: true } : entry)
  session.tree[path] = session.tree[listing.path] = entries
  if (!listHardLinks || runningHardLinkScans >= 2 || scan?.pending || background && scan && Date.now() - scan.startedAt < 30_000 || !entries.some(entry => entry.type === 'file')) return
  const current: HardLinkScan = { id: request.id, startedAt: Date.now(), names: scan?.names, pending: true }
  scans.set(listing.path, current)
  runningHardLinkScans++
  void Promise.resolve().then(() => listHardLinks(request.id, listing.path)).then(result => {
    if (!result.ok || !request.current() || session.phase !== 'connected') return
    const names = new Set(result.value)
    current.names = names
    const currentEntries = session.tree[listing.path]
    if (!currentEntries) return
    const updated = currentEntries.map(entry => {
      if (entry.type !== 'file' || !!entry.hardLink === names.has(entry.name)) return entry
      return { ...entry, hardLink: names.has(entry.name) }
    })
    if (session.tree[path] === currentEntries) session.tree[path] = updated
    session.tree[listing.path] = updated
  }).catch(() => {}).finally(() => { current.pending = false; runningHardLinkScans-- })
}

function readDirectory(session: Workspace, path: string, background: boolean, refreshAfterRead = false): Promise<DirectoryListing | undefined> {
  if (session.phase !== 'connected' || session.sftpError) return Promise.resolve(undefined)
  const request = workspaceRequest(session)
  let reads = directoryReads.get(session)
  if (!reads) { reads = new Map(); directoryReads.set(session, reads) }
  const existing = reads.get(path)
  if (existing?.id === request.id && existing.promise) {
    if (!refreshAfterRead) return existing.promise
    existing.next ??= existing.promise.catch(() => undefined).then(() => request.current() ? readDirectory(session, path, background) : undefined)
    return existing.next
  }
  const reading: DirectoryRead = { id: request.id }
  reads.set(path, reading)
  session.loading.add(path)
  reading.promise = (async () => {
    const response = await window.sshTool.listDirectory(request.id, path)
    if (!request.current() || session.phase !== 'connected') return
    const listing = unwrap(response)
    cacheDirectory(session, path, listing, background)
    return listing
  })().finally(() => {
    if (reads.get(path) === reading) {
      reads.delete(path)
      if (request.current()) session.loading.delete(path)
    }
  })
  return reading.promise
}

export async function loadDirectory(session: Workspace, path: string, force = false, background = false): Promise<void> {
  if (session.phase !== 'connected' || session.sftpError || session.tree[path] && !force) return
  if (background && session.loading.has(path)) return
  const request = workspaceRequest(session)
  const navigation = directoryNavigations.get(session)
  try {
    const listing = await readDirectory(session, path, background, force && !background)
    if (listing && path === session.directory && directoryNavigations.get(session) === navigation) session.directory = listing.path
  } catch (error) { if (!background && request.current() && session.phase === 'connected') notify((error as Error).message) }
}
export async function enterDirectory(session: Workspace, path: string, shouldApply = () => true): Promise<void> {
  if (session.phase !== 'connected' || session.sftpError) return
  const request = workspaceRequest(session)
  const navigation = Symbol()
  directoryNavigations.set(session, navigation)
  const current = () => directoryNavigations.get(session) === navigation && request.current() && session.phase === 'connected' && shouldApply()
  try {
    const listing = await readDirectory(session, path, false)
    if (!listing || !current()) return
    session.directory = listing.path
  } catch (error) { if (current()) throw error }
}
export async function navigate(path: string, shouldApply = () => true): Promise<void> {
  if (active.value) await enterDirectory(active.value, path, shouldApply)
}
export async function refreshFileTree(session: Workspace, background = false): Promise<void> {
  const root = session.directory
  const paths = new Set([root])
  for (const path of paths) {
    for (const entry of session.tree[path] || []) {
      if (entry.type === 'directory' && session.expanded.has(entry.path)) paths.add(entry.path)
    }
  }
  const request = workspaceRequest(session)
  const directories = [...paths]
  for (let index = 0; index < directories.length && request.current() && session.directory === root; index += 4) {
    if (background && active.value !== session) return
    await Promise.all(directories.slice(index, index + 4).map(path => loadDirectory(session, path, true, background)))
  }
}
export async function jumpToTerminalDirectory(): Promise<void> {
  const session = active.value
  if (!session || session.phase !== 'connected' || session.sftpError) return
  if (!session.terminalDirectory) throw new Error('当前终端尚未报告目录。请在远端 Shell 配置 OSC 7 后重试。')
  await navigate(session.terminalDirectory)
}
export function parentPath(path: string): string { return path.replace(/\/+$/, '').replace(/\/[^/]*$/, '') || '/' }
const fileOpens = new WeakMap<Workspace, { id: string; identity: string; selection: symbol; reads: Map<string, Promise<boolean>> }>()
export async function openFile(session: Workspace, entry: RemoteEntry): Promise<void> {
  if (entry.type === 'link' && entry.targetType === 'directory') { await enterDirectory(session, entry.path); return }
  if (entry.type === 'link' && entry.targetType === 'missing') throw new Error('符号链接目标不存在')
  if (entry.type === 'directory') {
    if (session.expanded.delete(entry.path)) return
    if (session.phase !== 'connected' && !session.tree[entry.path]) return
    session.expanded.add(entry.path)
    if (!session.loading.has(entry.path)) await loadDirectory(session, entry.path, true)
    return
  }
  const request = workspaceRequest(session)
  const identity = session.connectionKey, selection = Symbol()
  let group = fileOpens.get(session)
  if (!group || group.id !== request.id || group.identity !== identity) {
    group = { id: request.id, identity, selection, reads: new Map() }
    fileOpens.set(session, group)
  }
  group.selection = selection
  const { reads } = group, path = entry.path, previous = session.activeDocument
  const existing = session.documents.find(doc => doc.path === path)
  if (existing) {
    if (existing.detached) await fileWindows.get(existing)?.()
    else session.activeDocument = path
    return
  }
  let read = reads.get(path)
  if (!read) {
    const loading = { current: () => request.current() && session.phase === 'connected' }
    fileRequests.add(loading)
    refreshFileBusy()
    read = (async () => {
      const response = await window.sshTool.preview(request.id, path)
      if (!loading.current()) return false
      const preview = unwrap(response)
      session.documents.push({ path, preview, content: preview.content, saved: preview.content, busy: false, error: '', conflict: false })
      return true
    })().finally(() => { reads.delete(path); fileRequests.delete(loading); refreshFileBusy() })
    reads.set(path, read)
  }
  try {
    if (await read && request.current() && group.selection === selection && session.activeDocument === previous) session.activeDocument = path
  } catch (error) { if (request.current() && group.selection === selection) throw error }
}
export async function saveDocument(session: Workspace, doc: DocumentTab): Promise<void> {
  if (session.phase !== 'connected' || !session.documents.includes(doc) || doc.busy || doc.preview.kind !== 'text' || doc.preview.page || doc.content === doc.saved) return
  const request = workspaceRequest(session)
  doc.busy = true; doc.error = ''; const content = doc.content
  try {
    const response = await window.sshTool.saveText(request.id, doc.path, content, doc.preview.version)
    if (!request.current() || session.phase !== 'connected' || !session.documents.includes(doc)) return
    const preview = unwrap(response)
    doc.preview = preview; doc.saved = content; doc.conflict = false; notify('已保存到服务器')
  } catch (error) { if (request.current() && session.phase === 'connected') { doc.error = (error as Error).message; doc.conflict = (error as { code?: string }).code === 'CONFLICT' } }
  finally { if (request.current()) doc.busy = false }
}
export async function reloadDocument(session: Workspace, doc: DocumentTab, confirmReload = confirmAction): Promise<void> {
  const request = workspaceRequest(session), content = doc.content
  if (content !== doc.saved && !await confirmReload({
    title: '重新加载文件？', message: '当前未保存的修改将丢失。', action: '重新加载', danger: true
  })) return
  if (!request.current() || session.phase !== 'connected' || !session.documents.includes(doc) || doc.content !== content) return
  try {
    const response = await window.sshTool.preview(request.id, doc.path)
    if (!request.current() || session.phase !== 'connected' || !session.documents.includes(doc) || doc.content !== content) return
    const preview = unwrap(response)
    doc.preview = preview; doc.content = preview.content; doc.saved = preview.content; doc.error = ''; doc.conflict = false
  } catch (error) { if (request.current()) throw error }
}
export async function closeDocument(session: Workspace, doc: DocumentTab): Promise<void> {
  if (doc.busy || !session.documents.includes(doc)) return
  const request = workspaceRequest(session), content = doc.content
  if (content !== doc.saved && !await confirmAction({
    title: '关闭文件？', message: '当前未保存的修改将丢失。', action: '关闭', danger: true
  })) return
  if (!request.current() || doc.busy || doc.content !== content) return
  session.documents = session.documents.filter(d => d !== doc)
  if (session.activeDocument === doc.path) session.activeDocument = 'terminal'
}
export async function closeDocuments(session: Workspace, keep?: DocumentTab): Promise<void> {
  const request = workspaceRequest(session)
  const files = new Map(session.documents.filter(doc => doc !== keep && !doc.busy).map(doc => [doc, doc.content]))
  const unsaved = [...files.keys()].filter(doc => doc.content !== doc.saved).length
  if (unsaved && !await confirmAction({
    title: `关闭 ${files.size} 个文件标签？`, message: `将丢弃 ${unsaved} 个文件的未保存修改。`, action: '关闭', danger: true
  })) return
  if (!request.current()) return
  const closing = session.documents.filter(doc => files.has(doc) && !doc.busy && files.get(doc) === doc.content)
  session.documents = session.documents.filter(doc => !closing.includes(doc))
  if (closing.some(doc => doc.path === session.activeDocument)) {
    session.activeDocument = keep && session.documents.includes(keep) ? keep.path : 'terminal'
  }
}
export const phaseLabels: Record<Phase, string> = { connecting: '连接中', verifying: '等待核验', authenticating: '正在登录', connected: '已连接', failed: '连接失败', disconnected: '已断开' }
export function sizeLabel(bytes: number): string { if (bytes < 1024) return bytes + ' B'; if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB'; if (bytes < 1073741824) return (bytes / 1048576).toFixed(1) + ' MB'; return (bytes / 1073741824).toFixed(2) + ' GB' }
