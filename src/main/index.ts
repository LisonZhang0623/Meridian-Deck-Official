import { app, BrowserWindow, dialog, ipcMain, safeStorage, Menu, shell, type NativeImage } from 'electron'
import { join, basename, posix } from 'node:path'
import { Storage } from './storage'
import { SshService } from './ssh'
import { RemoteFiles } from './files'
import { Transfers } from './transfers'
import { DragDownloads } from './drag-download'
import { CommandsStore } from './commands'
import { LogService } from './logs'
import { ForwardsService } from './forwards'
import { ResourceService } from './resources'
import type { CommandSnippet, LogOptions, ForwardConfig } from '../shared/v2'
import { connectionIdentity } from '../shared/connection'
import * as validate from './validation'
import type { AppEvent, Credentials, Profile, Result, Settings } from '../shared/types'

let window: BrowserWindow | null = null
let ssh: SshService
let transfers: Transfers
let dragDownloads: DragDownloads
let logs: LogService
let forwards: ForwardsService
let resources: ResourceService
const dragIcons = new Map<string, NativeImage>()
const appName = 'Meridian Deck'
let quitting = false
const userDataOverride = process.env.SSHTOOL_USER_DATA || process.argv.find(arg => arg.startsWith('--user-data-dir='))?.slice('--user-data-dir='.length)
app.setPath('userData', userDataOverride || join(app.getPath('appData'), 'ssh-workbench'))
const gotLock = app.requestSingleInstanceLock()
if (!gotLock) app.quit()
else {
  app.on('second-instance', () => { if (window?.isMinimized()) window.restore(); window?.focus() })
  app.whenReady().then(start).catch(error => { dialog.showErrorBox(appName + ' 启动失败', (error as Error).message); app.quit() })
}
async function start(): Promise<void> {
  const storage = new Storage(app.getPath('userData'), {
    async encrypt(value) {
      if (!await safeStorage.isAsyncEncryptionAvailable()) throw new Error('系统安全存储不可用，请取消“记住登录凭据”后重试')
      return (await safeStorage.encryptStringAsync(value)).toString('base64')
    },
    async decrypt(value) { return (await safeStorage.decryptStringAsync(Buffer.from(value, 'base64'))).result }
  })
  await storage.init()
  const emit = (event: AppEvent): void => {
    if (event.type === 'state' && event.phase !== 'connected') {
      logs?.stop(event.sessionId); forwards?.stopSession(event.sessionId); resources?.cancel(event.sessionId)
    }
    if (window && !window.isDestroyed()) window.webContents.send('app:event', event)
  }
  ssh = new SshService(emit, {
    fingerprint: host => storage.fingerprint(host),
    trust: (host, fingerprint) => storage.trust(host, fingerprint),
    async recordConnected(id, identity) { await storage.recordConnected(id, identity); emit({ type: 'profiles', profiles: storage.list() }) }
  })
  const commands = new CommandsStore(app.getPath('userData'))
  await commands.init()
  logs = new LogService(id => ssh.clientFor(id), id => ssh.sessionIdentity(id), emit)
  forwards = new ForwardsService(id => ssh.clientFor(id), id => ssh.sessionIdentity(id), list => emit({ type: 'forwards', forwards: list }))
  resources = new ResourceService(id => ssh.clientFor(id))
  const files = new RemoteFiles(id => ssh.sftp(id))
  transfers = new Transfers(id => ssh.sftp(id), transfer => emit({ type: 'transfer', transfer }), async path => {
    const result = await dialog.showMessageBox(window!, { type: 'question', title: '确认替换文件', message: '远程文件已存在，是否替换？', detail: path, buttons: ['取消', '替换'], defaultId: 0, cancelId: 0 })
    return result.response === 1
  })
  dragDownloads = new DragDownloads(join(app.getPath('temp'), 'sshtool-drag-downloads'), id => ssh.sessionIdentity(id), id => ssh.sftp(id), (id, localPath, remotePath) => transfers.enqueueDownload(id, localPath, remotePath))
  await dragDownloads.init()
  function authorized(event: Electron.IpcMainEvent | Electron.IpcMainInvokeEvent): boolean {
    return !!window && event.sender === window.webContents && event.senderFrame === window.webContents.mainFrame
  }
  function handle<T>(channel: string, fn: (...args: any[]) => Promise<T> | T): void {
    ipcMain.handle(channel, async (event, ...args): Promise<Result<T>> => {
      if (!authorized(event)) return { ok: false, error: '无权调用此接口' }
      try { return { ok: true, value: await fn(...args) } }
      catch (error) { const message = (error as Error).message || '操作失败'; return { ok: false, error: message.replace(/^CONFLICT:/, ''), code: message.startsWith('CONFLICT:') ? 'CONFLICT' : undefined } }
    })
  }
  handle('bootstrap', () => ({ profiles: storage.list(), settings: storage.getSettings() }))
  handle('profile:save', async (profile: Profile, secrets: Credentials) => { const saved = await storage.saveProfile(validate.profile(profile), validate.credentials(secrets)); emit({ type: 'profiles', profiles: storage.list() }); return saved })
  handle('profile:delete', async (id: string) => { await storage.delete(validate.id(id)); emit({ type: 'profiles', profiles: storage.list() }) })
  handle('settings:save', (settings: Settings) => storage.saveSettings(settings))
  handle('key:select', async () => { const result = await dialog.showOpenDialog(window!, { title: '选择 SSH 私钥', properties: ['openFile'] }); return result.canceled ? null : result.filePaths[0] })
  handle('ssh:connect', async (sessionId: string, profileId: string, input: Credentials, jumpInput?: Credentials, expectedIdentity?: string) => {
    validate.id(sessionId); validate.id(profileId)
    const supplied = validate.credentials(input)
    const profile = storage.get(profileId)
    const identity = connectionIdentity(profile)
    if (expectedIdentity !== undefined && expectedIdentity !== identity) throw new Error('连接配置已变化，请重新打开连接')
    const jumpProfile = profile.jumpProfileId ? storage.get(validate.id(profile.jumpProfileId)) : undefined
    let provided: Credentials = {}
    if (jumpProfile) {
      if (jumpProfile.id === profile.id || jumpProfile.jumpProfileId) throw new Error('仅支持单跳跳板机，不能使用自己或链式跳板')
      provided = validate.credentials(jumpInput || {})
    }
    const [saved, stored] = await Promise.all([
      supplied.password !== undefined || supplied.passphrase !== undefined ? {} : storage.credentials(profileId),
      jumpProfile && provided.password === undefined && provided.passphrase === undefined ? storage.credentials(jumpProfile.id) : {},
    ])
    if (connectionIdentity(storage.get(profileId)) !== identity) throw new Error('连接配置已变化，请重新打开连接')
    const jump = jumpProfile ? { profile: jumpProfile, credentials: { ...stored, ...Object.fromEntries(Object.entries(provided).filter(([, v]) => v !== undefined)) } } : undefined
    ssh.connect(sessionId, profile, { ...saved, ...Object.fromEntries(Object.entries(supplied).filter(([, v]) => v !== undefined)) }, jump)
  })
  handle('ssh:disconnect', (id: string) => ssh.disconnect(validate.id(id)))
  handle('ssh:newTerminal', (id: string, sourceId: string, profileId: string, initialPath?: string) => ssh.newTerminal(validate.id(id), validate.id(sourceId), storage.get(validate.id(profileId)), initialPath === undefined ? undefined : validate.remotePath(initialPath)))
  handle('gpu:status', (id: string) => ssh.gpuStatus(validate.id(id)))
  handle('gpu:cancel', (id: string) => ssh.cancelGpuStatus(validate.id(id)))
  handle('commands:list', () => commands.list())
  handle('commands:save', (command: CommandSnippet) => { if (command?.profileId) storage.get(validate.id(command.profileId)); return commands.save(command) })
  handle('commands:delete', (id: string) => commands.delete(validate.id(id)))
  handle('commands:render', (id: string, parameters: Record<string, string>) => commands.render(validate.id(id), parameters))
  handle('logs:start', (id: string, options: LogOptions) => logs.start(validate.id(id), options))
  handle('logs:stop', (id: string) => logs.stop(validate.id(id)))
  handle('forwards:list', () => forwards.list())
  handle('forwards:start', (id: string, config: ForwardConfig) => forwards.start(validate.id(id), config))
  handle('forwards:stop', (id: string) => forwards.stop(validate.id(id)))
  handle('forwards:open', async (id: string) => {
    const forward = forwards.list().find(item => item.id === validate.id(id) && item.status === 'listening')
    if (!forward || !Number.isInteger(forward.localPort) || forward.localPort < 1 || forward.localPort > 65535) throw new Error('转发尚未就绪，请先启动转发')
    ssh.clientFor(forward.sessionId)
    await shell.openExternal(`http://127.0.0.1:${forward.localPort}/`)
  })
  handle('resources:status', (id: string) => resources.status(validate.id(id)))
  handle('resources:cancel', (id: string) => resources.cancel(validate.id(id)))
  handle('ssh:trust', (id: string, accept: boolean) => { if (typeof accept !== 'boolean') throw new Error('无效的指纹确认'); return ssh.trust(validate.id(id), accept) })
  ipcMain.on('terminal:input', (event, id: string, data: string) => { if (authorized(event) && typeof id === 'string' && typeof data === 'string') ssh.input(id, data) })
  ipcMain.on('terminal:resize', (event, id: string, cols: number, rows: number) => { if (authorized(event) && typeof id === 'string') ssh.resize(id, cols, rows) })
  handle('files:list', (id: string, path: string) => files.list(validate.id(id), validate.remotePath(path)))
  handle('files:preview', (id: string, path: string) => files.preview(validate.id(id), validate.remotePath(path)))
  handle('files:save', (id: string, path: string, content: string, version: string) => files.save(validate.id(id), validate.remotePath(path), validate.text(content, '文本', 2 * 1024 * 1024), validate.text(version, '版本', 100)))
  handle('files:create', (id: string, parent: string, name: string, directory: boolean) => files.create(validate.id(id), validate.remotePath(parent), validate.entryName(name), directory === true))
  handle('files:rename', (id: string, path: string, name: string) => files.rename(validate.id(id), validate.remotePath(path), validate.entryName(name)))
  handle('files:delete', (id: string, path: string, directory: boolean) => files.delete(validate.id(id), validate.remotePath(path), directory === true))
  handle('transfer:upload', async (id: string, directory: string, paths?: string[]) => {
    validate.id(id); directory = validate.remotePath(directory); ssh.sftp(id)
    if (paths === undefined) { const result = await dialog.showOpenDialog(window!, { title: '选择上传的文件', properties: ['openFile', 'multiSelections'] }); if (result.canceled) return; paths = result.filePaths }
    if (!Array.isArray(paths) || paths.length > 100) throw new Error('每次最多上传 100 个文件')
    for (const path of paths) { validate.text(path, '本地路径'); transfers.enqueue(id, 'upload', path, posix.join(directory, basename(path))) }
  })
  handle('transfer:download', async (id: string, path: string) => {
    validate.id(id); path = validate.remotePath(path); ssh.sftp(id)
    const result = await dialog.showSaveDialog(window!, { title: '下载远程文件', defaultPath: posix.basename(path) })
    if (!result.canceled && result.filePath) transfers.enqueue(id, 'download', result.filePath, path)
  })
  handle('drag:prepare', async (id: string, path: string) => {
    validate.id(id)
    const prepared = await dragDownloads.prepare(id, validate.remotePath(path))
    const icon = await app.getFileIcon(dragDownloads.fileForDrag(id, prepared.token), { size: 'normal' })
    dragIcons.set(prepared.token, icon)
    if (dragIcons.size > 200) dragIcons.delete(dragIcons.keys().next().value!)
    return { token: prepared.token, name: prepared.name, expiresAt: prepared.expiresAt }
  })
  ipcMain.on('drag:start', (event, id: string, token: string) => {
    if (!authorized(event)) return
    try {
      validate.id(id); validate.id(token)
      const icon = dragIcons.get(token)
      if (!icon) throw new Error('请先准备拖出文件')
      event.sender.startDrag({ file: dragDownloads.fileForDrag(id, token), icon })
    } catch (error) { emit({ type: 'notice', dragToken: token, message: (error as Error).message + '；请右键重新准备拖出下载' }) }
    finally { emit({ type: 'drag-ended', sessionId: typeof id === 'string' ? id : '', token: typeof token === 'string' ? token : '' }) }
  })
  handle('transfer:cancel', (id: string) => transfers.cancel(validate.id(id)))
  handle('transfer:retry', (id: string) => transfers.retry(validate.id(id)))
  Menu.setApplicationMenu(Menu.buildFromTemplate([
    { label: '编辑', submenu: [{ role: 'undo' }, { role: 'redo' }, { type: 'separator' }, { role: 'cut' }, { role: 'copy' }, { role: 'paste' }, { role: 'selectAll' }] },
    { label: '视图', submenu: [{ role: 'resetZoom' }, { role: 'zoomIn' }, { role: 'zoomOut' }, { role: 'togglefullscreen' }, ...(!app.isPackaged ? [{ role: 'toggleDevTools' as const }] : [])] }
  ]))
  createWindow()
  app.on('activate', () => { if (!window) createWindow() })
}
function createWindow(): void {
  window = new BrowserWindow({ width: 1320, height: 860, minWidth: 900, minHeight: 620, title: appName, backgroundColor: '#f3f5f8', autoHideMenuBar: true,
    webPreferences: { preload: join(__dirname, '../preload/index.js'), contextIsolation: true, sandbox: true, nodeIntegration: false, spellcheck: false } })
  window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }))
  window.webContents.on('will-navigate', event => event.preventDefault())
  window.webContents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false))
  if (!app.isPackaged && process.env.ELECTRON_RENDERER_URL) void window.loadURL(process.env.ELECTRON_RENDERER_URL)
  else void window.loadFile(join(__dirname, '../renderer/index.html'))
  window.on('closed', () => { window = null; logs?.dispose(); forwards?.dispose(); resources?.dispose(); transfers?.dispose(); ssh?.dispose() })
}
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
app.on('before-quit', event => {
  if (quitting) return
  event.preventDefault(); quitting = true
  logs?.dispose(); forwards?.dispose(); resources?.dispose(); transfers?.dispose(); ssh?.dispose(); dragIcons.clear()
  void (dragDownloads?.dispose() || Promise.resolve()).finally(() => app.quit())
})
