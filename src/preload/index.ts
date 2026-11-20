import { contextBridge, ipcRenderer, webUtils } from 'electron'
import type { Api, AppEvent } from '../shared/types'

const api: Api = {
  bootstrap: () => ipcRenderer.invoke('bootstrap'),
  saveProfile: (profile, credentials) => ipcRenderer.invoke('profile:save', profile, credentials),
  deleteProfile: id => ipcRenderer.invoke('profile:delete', id),
  saveSettings: settings => ipcRenderer.invoke('settings:save', settings),
  selectKey: () => ipcRenderer.invoke('key:select'),
  connect: (sessionId, profileId, credentials, jumpCredentials, expectedIdentity) => ipcRenderer.invoke('ssh:connect', sessionId, profileId, credentials, jumpCredentials, expectedIdentity),
  newTerminal: (id, sourceId, profileId, initialPath) => ipcRenderer.invoke('ssh:newTerminal', id, sourceId, profileId, initialPath),
  disconnect: id => ipcRenderer.invoke('ssh:disconnect', id),
  trust: (id, accept) => ipcRenderer.invoke('ssh:trust', id, accept),
  terminalInput: (id, data) => ipcRenderer.send('terminal:input', id, data),
  terminalResize: (id, cols, rows) => ipcRenderer.send('terminal:resize', id, cols, rows),
  gpuStatus: id => ipcRenderer.invoke('gpu:status', id),
  cancelGpuStatus: id => ipcRenderer.invoke('gpu:cancel', id),
  listCommands: () => ipcRenderer.invoke('commands:list'),
  saveCommand: command => ipcRenderer.invoke('commands:save', command),
  deleteCommand: id => ipcRenderer.invoke('commands:delete', id),
  renderCommand: (id, parameters) => ipcRenderer.invoke('commands:render', id, parameters),
  startLog: (id, options) => ipcRenderer.invoke('logs:start', id, options),
  stopLog: id => ipcRenderer.invoke('logs:stop', id),
  listForwards: () => ipcRenderer.invoke('forwards:list'),
  startForward: (id, config) => ipcRenderer.invoke('forwards:start', id, config),
  stopForward: id => ipcRenderer.invoke('forwards:stop', id),
  openForward: id => ipcRenderer.invoke('forwards:open', id),
  resourceStatus: id => ipcRenderer.invoke('resources:status', id),
  cancelResourceStatus: id => ipcRenderer.invoke('resources:cancel', id),
  listDirectory: (id, path) => ipcRenderer.invoke('files:list', id, path),
  preview: (id, path) => ipcRenderer.invoke('files:preview', id, path),
  saveText: (id, path, content, version) => ipcRenderer.invoke('files:save', id, path, content, version),
  createEntry: (id, path, name, directory) => ipcRenderer.invoke('files:create', id, path, name, directory),
  renameEntry: (id, path, name) => ipcRenderer.invoke('files:rename', id, path, name),
  deleteEntry: (id, path, directory) => ipcRenderer.invoke('files:delete', id, path, directory),
  upload: (id, path, paths) => ipcRenderer.invoke('transfer:upload', id, path, paths),
  download: (id, path) => ipcRenderer.invoke('transfer:download', id, path),
  prepareDragDownload: (id, path) => ipcRenderer.invoke('drag:prepare', id, path),
  startDragDownload: (id, token) => ipcRenderer.send('drag:start', id, token),
  cancelTransfer: id => ipcRenderer.invoke('transfer:cancel', id),
  retryTransfer: id => ipcRenderer.invoke('transfer:retry', id),
  filePath: file => webUtils.getPathForFile(file),
  onEvent(listener) {
    const callback = (_event: Electron.IpcRendererEvent, payload: AppEvent) => listener(payload)
    ipcRenderer.on('app:event', callback)
    return () => ipcRenderer.removeListener('app:event', callback)
  }
}
contextBridge.exposeInMainWorld('sshTool', api)
