import type { CommandSnippet, LogOptions, LogEvent, ForwardConfig, PortForward, ResourceSnapshot } from './v2'
export type AuthType = 'password' | 'key' | 'agent' | 'interactive'
export interface Profile {
  id: string; name: string; group: string; host: string; port: number; username: string
  authType: AuthType; keyPath: string; initialPath: string; timeout: number; keepalive: boolean
  rememberCredentials: boolean; hasCredentials?: boolean; tags?: string[]; lastConnectedAt?: number
  jumpProfileId?: string
  jumpIdentity?: string
  proxyAddress?: string
  keyboardInteractive?: boolean
}
export interface Credentials { password?: string; passphrase?: string }
export interface AuthChallenge {
  type: 'auth-challenge'; sessionId: string; challengeId: string; host: string; label: string
  instruction: string; questions: string[]; echo: boolean[]
}
export type SidebarPanel = 'servers' | 'files' | 'gpu' | 'commands' | 'logs' | 'forwards' | 'resources'
export type StartupAnimationVariant = 'echo' | 'orbit'
export type StartupAnimation = StartupAnimationVariant | 'random' | 'off'
export type WindowCloseAction = 'ask' | 'tray' | 'quit'
export interface Settings {
  sidebarSide: 'left' | 'right'; sidebarCollapsed: boolean; sidebarPanel: SidebarPanel; sidebarWidth: number
  theme: 'light' | 'dark' | 'system'; terminalFontSize: number; editorFontSize: number; uiZoom: number
  startupAnimation: StartupAnimation; gpuRefreshSeconds: number; windowCloseAction: WindowCloseAction; checkUpdatesOnStartup?: boolean
}
export interface UpdateRelease { version: string; url: string; notes: string }
export interface GpuDevice {
  index: number; uuid: string; name: string; utilization: number | null
  memoryUsedMiB: number | null; memoryTotalMiB: number | null; temperatureC: number | null
  powerDrawW: number | null; powerLimitW: number | null; fanPercent: number | null; performanceState: string | null
}
export interface GpuSnapshot { devices: GpuDevice[]; collectedAt: number; warning?: string }
export interface DragDownload { token: string; name: string; expiresAt: number }
export type Phase = 'connecting' | 'verifying' | 'authenticating' | 'connected' | 'failed' | 'disconnected'
export interface RemoteEntry {
  name: string; path: string; type: 'directory' | 'file' | 'link'; size: number; modified: number; mode: number
  linkTarget?: string; targetType?: 'directory' | 'file' | 'missing'; hardLink?: boolean
}
export interface FTPConfig { host: string; port: number; username: string; password: string; tls: boolean }
export interface Preview {
  path: string; kind: 'text' | 'image'; content: string; mime?: string; encoding?: string
  page?: { offset: number; nextOffset?: number }; version: string; size: number; modified: number
}
export interface Transfer {
  id: string; sessionId: string; name: string; direction: 'upload' | 'download'; remotePath: string
  purpose?: 'drag' | 'directory' | 'sync' | 'nas'
  dragRoot?: string
  size: number; transferred: number; speed: number; status: 'queued' | 'running' | 'completed' | 'failed' | 'cancelled' | 'paused'; error?: string
  resumable?: boolean
}
export interface DirectoryChange {
  path: string
  status: 'local-only' | 'remote-only' | 'different' | 'conflict'
  localType?: string; remoteType?: string; localSize?: number; remoteSize?: number
}
export interface DirectoryComparison { token: string; localPath: string; remotePath: string; changes: DirectoryChange[] }
export interface NasEntry { path: string; name: string; type: 'file' | 'directory'; size: number; modified: number }
export interface NasDirectory { rootId: string; root: string; path: string; parent: string | null; entries: NasEntry[]; truncated: boolean }
export type AppEvent =
  | { type: 'drag-ended'; sessionId: string; token: string }
  | AuthChallenge
  | LogEvent
  | { type: 'forwards'; forwards: PortForward[] }
  | { type: 'profiles'; profiles: Profile[] }
  | { type: 'notice'; message: string; dragToken?: string }
  | { type: 'state'; sessionId: string; phase: Phase; message: string; home?: string; sftpError?: string; startupNotice?: string }
  | { type: 'terminal'; sessionId: string; data: string; sequence?: string }
  | { type: 'hostkey'; sessionId: string; host: string; fingerprint: string; previous?: string; keyType: string }
  | { type: 'transfer'; transfer: Transfer }
export type Result<T> = { ok: true; value: T } | { ok: false; error: string; code?: string }
export interface OSC7Status { shell: string; path?: string; configured: boolean }

export interface Api {
  checkOsc7?(sessionId: string): Promise<Result<OSC7Status>>
  prepareOsc7?(sessionId: string, path: string): Promise<Result<OSC7Status>>
  configureOsc7?(sessionId: string, path: string): Promise<Result<OSC7Status>>
  nativeFileDrop?: boolean
  nativeDragDownload?: boolean
  terminalConsumer?(sessionId: string, attached: boolean): void
  ackTerminal?(sessionId: string, sequence: string): void
  openEditorWindow?(token: string): Promise<Result<void>>
  workspaceDirty?(dirty: boolean): void
  checkUpdate?(): Promise<Result<UpdateRelease>>
  openUpdateRelease?(url: string): Promise<Result<void>>
  bootstrap(): Promise<Result<{ profiles: Profile[]; settings: Settings; traySupported?: boolean }>>
  saveProfile(profile: Profile, credentials: Credentials): Promise<Result<Profile>>
  deleteProfile(id: string): Promise<Result<void>>
  saveSettings(settings: Settings): Promise<Result<void>>
  selectKey(): Promise<Result<string | null>>
  importConfig?(): Promise<Result<{ added: number; profiles: Profile[]; settings: Settings } | null>>
  exportConfig?(): Promise<Result<boolean>>
  ftpConnect?(config: FTPConfig): Promise<Result<{ path: string; entries: RemoteEntry[] }>>
  ftpDisconnect?(): Promise<Result<void>>
  ftpList?(path: string): Promise<Result<{ path: string; entries: RemoteEntry[] }>>
  ftpUpload?(path: string): Promise<Result<number>>
  ftpDownload?(path: string): Promise<Result<boolean>>
  connect(sessionId: string, profileId: string, credentials: Credentials, jumpCredentials?: Credentials, expectedIdentity?: string): Promise<Result<void>>
  connectChain?(sessionId: string, profileId: string, credentials: Credentials, hopCredentials: Record<string, Credentials>, expectedIdentity?: string): Promise<Result<void>>
  connectTmux?(sessionId: string, profileId: string, credentials: Credentials, hopCredentials: Record<string, Credentials>, name: string, expectedIdentity?: string): Promise<Result<void>>
  answerChallenge?(sessionId: string, challengeId: string, answers: string[] | null): Promise<Result<void>>
  newTerminal(sessionId: string, sourceSessionId: string, profileId: string, initialPath?: string): Promise<Result<void>>
  newTmuxTerminal?(sessionId: string, sourceSessionId: string, profileId: string, name: string): Promise<Result<void>>
  disconnect(sessionId: string): Promise<Result<void>>
  trust(sessionId: string, accept: boolean): Promise<Result<void>>
  trustHostKey?(sessionId: string, host: string, fingerprint: string, accept: boolean): Promise<Result<void>>
  terminalInput(sessionId: string, data: string): void
  terminalResize(sessionId: string, cols: number, rows: number): void
  gpuStatus(sessionId: string): Promise<Result<GpuSnapshot>>
  cancelGpuStatus(sessionId: string): Promise<Result<void>>
  listCommands(): Promise<Result<CommandSnippet[]>>
  saveCommand(command: CommandSnippet): Promise<Result<CommandSnippet>>
  deleteCommand(id: string): Promise<Result<void>>
  renderCommand(id: string, parameters: Record<string, string>): Promise<Result<string>>
  startLog(sessionId: string, options: LogOptions): Promise<Result<string>>
  stopLog(sessionId: string): Promise<Result<void>>
  listForwards(): Promise<Result<PortForward[]>>
  startForward(sessionId: string, config: ForwardConfig): Promise<Result<PortForward>>
  stopForward(id: string): Promise<Result<void>>
  dismissForward?(id: string): Promise<Result<void>>
  openForward(id: string): Promise<Result<void>>
  resourceStatus(sessionId: string): Promise<Result<ResourceSnapshot>>
  cancelResourceStatus(sessionId: string): Promise<Result<void>>
  listDirectory(sessionId: string, path: string): Promise<Result<{ path: string; entries: RemoteEntry[] }>>
  listHardLinks?(sessionId: string, path: string): Promise<Result<string[]>>
  preview(sessionId: string, path: string): Promise<Result<Preview>>
  previewPage?(sessionId: string, path: string, offset: number, version: string): Promise<Result<Preview>>
  thumbnail?(sessionId: string, path: string): Promise<Result<string>>
  saveText(sessionId: string, path: string, content: string, version: string): Promise<Result<Preview>>
  createEntry(sessionId: string, parent: string, name: string, directory: boolean): Promise<Result<void>>
  renameEntry(sessionId: string, path: string, name: string): Promise<Result<void>>
  deleteEntry(sessionId: string, path: string, directory: boolean): Promise<Result<void>>
  upload(sessionId: string, remoteDirectory: string, paths?: string[]): Promise<Result<void>>
  uploadDropped?(sessionId: string, remoteDirectory: string, tokens: string[]): Promise<Result<boolean>>
  nasSelect?(): Promise<Result<NasDirectory | null>>
  nasList?(path: string, rootId?: string): Promise<Result<NasDirectory | null>>
  nasUpload?(sessionId: string, remoteDirectory: string, rootId: string, paths: string[]): Promise<Result<void>>
  nasDownload?(sessionId: string, paths: string[], rootId: string, directory: string): Promise<Result<void>>
  download(sessionId: string, path: string): Promise<Result<void>>
  uploadDirectory?(sessionId: string, remoteParent: string): Promise<Result<number | null>>
  downloadDirectory?(sessionId: string, remotePath: string): Promise<Result<number | null>>
  compareDirectory?(sessionId: string, remotePath: string): Promise<Result<DirectoryComparison | null>>
  syncDirectory?(sessionId: string, token: string, direction: 'upload' | 'download'): Promise<Result<number>>
  deleteTree?(sessionId: string, remotePath: string): Promise<Result<boolean>>
  prepareDragDownload(sessionId: string, path: string): Promise<Result<DragDownload>>
  prepareDragDirectory?(sessionId: string, path: string): Promise<Result<DragDownload>>
  startDragDownload(sessionId: string, token: string): void
  cancelTransfer(id: string): Promise<Result<void>>
  retryTransfer(id: string): Promise<Result<void>>
  pauseTransfer?(id: string): Promise<Result<void>>
  resumeTransfer?(id: string, sessionId: string): Promise<Result<void>>
  discardTransferPartial?(id: string, sessionId: string): Promise<Result<void>>
  filePath(file: File): string
  onEvent(listener: (event: AppEvent) => void): () => void
}
