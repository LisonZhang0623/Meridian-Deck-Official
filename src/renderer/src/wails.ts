import { windowClosed } from './document-tab-drag'
import type { Api, AppEvent, Result } from '../../shared/types'

export interface WailsRuntime {
  call(operation: string, args: unknown[]): Promise<Result<unknown>>
  on(name: string, listener: (data: unknown) => void): () => void
}

export function createWailsApi(runtime: WailsRuntime, windows = /Windows/i.test(globalThis.navigator?.userAgent ?? '')): Api {
  const listeners = new Set<(event: AppEvent) => void>()
  const terminals = new Set<string>()
  const inputQueues = new Map<string, { tail: Promise<void>; bytes: number; cancelled: boolean }>()
  let dirtyQueue = Promise.resolve()
  const publish = (event: AppEvent) => { for (const listener of listeners) { try { listener(event) } catch (error) { console.error('工作台事件处理失败', error) } } }
  const invoke = async <T>(operation: string, ...args: unknown[]): Promise<Result<T>> => {
    try {
      return await runtime.call(operation, args.map(value => value === undefined ? null : value)) as Result<T>
    } catch (error) {
      console.error('桌面操作失败', operation, error)
      return { ok: false, error: '暂时无法连接桌面服务，请重试。', code: 'DESKTOP_UNAVAILABLE' }
    }
  }
  // Resize and frame acknowledgements are background work, not user actions.
  const fire = (operation: string, ...args: unknown[]) => { void invoke(operation, ...args) }
  const terminalInput = (id: string, data: string) => {
    let queue = inputQueues.get(id)
    if (!queue) { queue = { tail: Promise.resolve(), bytes: 0, cancelled: false }; inputQueues.set(id, queue) }
    const bytes = new TextEncoder().encode(data).byteLength
    if (queue.bytes + bytes > 1024 * 1024) { publish({ type: 'notice', message: '终端输入缓冲已满，请等待服务器响应后重试' }); return }
    queue.bytes += bytes
    const current = queue
    current.tail = current.tail.then(async () => {
      if (current.cancelled) return
      const result = await invoke('terminal:input', id, data)
      if (!current.cancelled && !result.ok) publish({ type: 'notice', message: result.error })
    }).finally(() => { current.bytes -= bytes })
  }
  runtime.on('app:event', data => {
    if (!data || typeof data !== 'object' || !('type' in data)) return
    const event = data as AppEvent
    if (event.type === 'state' && event.phase !== 'connected') { const queue = inputQueues.get(event.sessionId); if (queue) { queue.cancelled = true; inputQueues.delete(event.sessionId) } }
    publish(event)
    // Frames received before mounting are retained by bounded workbench history.
    // Mounted xterm instances acknowledge only after parsing their frame.
    if (event.type === 'terminal' && event.sequence && !terminals.has(event.sessionId)) fire('terminal:ack', event.sessionId, event.sequence)
  })
  runtime.on('files-dropped', data => {
    if (Array.isArray(data) && data.every(token => typeof token === 'string')) window.dispatchEvent(new CustomEvent('sshtool:files-dropped', { detail: data }))
  })
  return {
    openEditorWindow: token => invoke('window:editor', token),
    nativeFileDrop: true,
    nativeDragDownload: windows,
    bootstrap: () => invoke('bootstrap'),
    checkUpdate: () => invoke('updates:check'),
    openUpdateRelease: url => invoke('updates:open', url),
    saveProfile: (profile, credentials) => invoke('profile:save', profile, credentials),
    deleteProfile: id => invoke('profile:delete', id),
    saveSettings: settings => invoke('settings:save', settings),
    selectKey: () => invoke('key:select'),
    connect: (id, profile, credentials, jump, expected) => invoke('ssh:connect', id, profile, credentials, jump || {}, ...(expected === undefined ? [] : [expected])),
    connectChain: (id, profile, credentials, hops, expected) => invoke('ssh:connectChain', id, profile, credentials, hops, ...(expected === undefined ? [] : [expected])),
    connectTmux: (id, profile, credentials, hops, name, expected) => invoke('ssh:connectTmux', id, profile, credentials, hops, name, ...(expected === undefined ? [] : [expected])),
    answerChallenge: (id, challengeId, answers) => invoke('ssh:answerChallenge', id, challengeId, answers),
    newTerminal: (id, source, profile, initial) => invoke('ssh:newTerminal', id, source, profile, initial),
    newTmuxTerminal: (id, source, profile, name) => invoke('ssh:newTmuxTerminal', id, source, profile, name),
    importConfig: () => invoke('config:import'),
    exportConfig: () => invoke('config:export'),
    ftpConnect: config => invoke('ftp:connect', config),
    ftpDisconnect: () => invoke('ftp:disconnect'),
    ftpList: path => invoke('ftp:list', path),
    ftpUpload: path => invoke('ftp:upload', path),
    ftpDownload: path => invoke('ftp:download', path),
    disconnect: id => invoke('ssh:disconnect', id),
    trust: (id, accept) => invoke('ssh:trust', id, accept),
    trustHostKey: (id, host, fingerprint, accept) => invoke('ssh:trustHostKey', id, host, fingerprint, accept),
    terminalInput,
    terminalResize: (id, cols, rows) => fire('terminal:resize', id, cols, rows),
    terminalConsumer: (id, attached) => { if (attached) terminals.add(id); else terminals.delete(id) },
    ackTerminal: (id, sequence) => fire('terminal:ack', id, sequence),
    workspaceDirty: dirty => {
      dirtyQueue = dirtyQueue.then(async () => {
        const result = await invoke('window:dirty', dirty)
        if (dirty && !result.ok) publish({ type: 'notice', message: '未能开启退出保护，请先保存文件。' })
      })
    },
    gpuStatus: id => invoke('gpu:status', id),
    cancelGpuStatus: id => invoke('gpu:cancel', id),
    listCommands: () => invoke('commands:list'),
    saveCommand: command => invoke('commands:save', command),
    deleteCommand: id => invoke('commands:delete', id),
    renderCommand: (id, parameters) => invoke('commands:render', id, parameters),
    startLog: (id, options) => invoke('logs:start', id, options),
    stopLog: id => invoke('logs:stop', id),
    listForwards: () => invoke('forwards:list'),
    startForward: (id, config) => invoke('forwards:start', id, config),
    stopForward: id => invoke('forwards:stop', id),
    dismissForward: id => invoke('forwards:dismiss', id),
    openForward: id => invoke('forwards:open', id),
    resourceStatus: id => invoke('resources:status', id),
    cancelResourceStatus: id => invoke('resources:cancel', id),
    listDirectory: (id, path) => invoke('files:list', id, path),
    listHardLinks: (id, path) => invoke('files:hardLinks', id, path),
    preview: (id, path) => invoke('files:preview', id, path),
    previewPage: (id, path, offset, version) => invoke('files:previewPage', id, path, offset, version),
    thumbnail: (id, path) => invoke('files:thumbnail', id, path),
    saveText: (id, path, content, version) => invoke('files:save', id, path, content, version),
    createEntry: (id, parent, name, directory) => invoke('files:create', id, parent, name, directory),
    renameEntry: (id, path, name) => invoke('files:rename', id, path, name),
    deleteEntry: (id, path, directory) => invoke('files:delete', id, path, directory),
    upload: (id, directory, paths) => invoke('transfer:upload', id, directory, paths),
    uploadDropped: (id, directory, tokens) => invoke('transfer:dropUpload', id, directory, tokens),
    nasSelect: () => invoke('nas:select'),
    nasList: (path, rootId) => invoke('nas:list', path, rootId),
    nasUpload: (id, directory, rootId, paths) => invoke('nas:upload', id, directory, rootId, paths),
    nasDownload: (id, paths, rootId, directory) => invoke('nas:download', id, paths, rootId, directory),
    download: (id, path) => invoke('transfer:download', id, path),
    uploadDirectory: (id, path) => invoke('transfer:uploadDirectory', id, path),
    downloadDirectory: (id, path) => invoke('transfer:downloadDirectory', id, path),
    compareDirectory: (id, path) => invoke('files:compareDirectory', id, path),
    syncDirectory: (id, token, direction) => invoke('files:syncDirectory', id, token, direction),
    deleteTree: (id, path) => invoke('files:deleteTree', id, path),
    prepareDragDownload: (id, path) => invoke('drag:prepare', id, path),
    prepareDragDirectory: windows ? (id, path) => invoke('drag:prepareDirectory', id, path) : undefined,
    startDragDownload: (id, token) => {
      void invoke('drag:start', id, token).then(result => {
        if (result.ok) return
        window.dispatchEvent(new CustomEvent('sshtool:drag-out-failed', { detail: { sessionId: id, token } }))
        publish({ type: 'notice', message: result.error, dragToken: token })
      })
    },
    cancelTransfer: id => invoke('transfer:cancel', id),
    retryTransfer: id => invoke('transfer:retry', id),
    pauseTransfer: id => invoke('transfer:pause', id),
    resumeTransfer: (id, session) => invoke('transfer:resume', id, session),
    discardTransferPartial: (id, session) => invoke('transfer:discardPartial', id, session),
    // Paths come from native drop/dialog capabilities, never File.path.
    filePath: () => '',
    onEvent: listener => { listeners.add(listener); return () => listeners.delete(listener) },
  }
}

export async function installWailsBridge(editorOnly = false): Promise<void> {
  if (window.sshTool) return
  const { Call, Events } = await import('@wailsio/runtime')
  if (!editorOnly) {
    Events.On('editor-closed', event => { const token = Array.isArray(event.data) ? event.data[0] : event.data; windowClosed.get(token)?.() })
  }
  window.sshTool = createWailsApi({ call: (operation, args) => Call.ByName('sshtool/internal/desktop.Service.Call', operation, args), on: (name, listener) => editorOnly ? () => {} : Events.On(name, event => listener(event.data)) })
}
