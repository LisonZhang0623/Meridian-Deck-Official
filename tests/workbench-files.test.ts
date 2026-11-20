import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { state, connect, initialize, newProfile, openDirectoryTerminal, prepareDragDownload, dragDownloads, dragKey, dragRemoteFile, retryTransfer, dismissCompletedTransfers, loadDirectory, openFile, refreshFileTree } from '../src/renderer/src/workbench'
import { DEFAULT_SETTINGS } from '../src/shared/settings'
import { confirmAction } from '../src/renderer/src/confirmation'

vi.mock('../src/renderer/src/confirmation', () => ({ confirmAction: vi.fn() }))
import type { AppEvent, RemoteEntry, Result, Transfer } from '../src/shared/types'

const profile = { ...newProfile(), id: 'files-profile', name: 'Files', host: 'localhost', username: 'test', initialPath: '/home/test' }
const entry: RemoteEntry = { name: 'hello.txt', path: '/home/test/hello.txt', type: 'file', size: 20, modified: 10, mode: 0o100644 }
const directory: RemoteEntry = { name: 'logs', path: '/home/test/logs', type: 'directory', size: 0, modified: 10, mode: 0o40755 }
let listener: (event: AppEvent) => void
const api = { bootstrap: vi.fn(), onEvent: vi.fn(), connect: vi.fn(), newTerminal: vi.fn(), listDirectory: vi.fn(), prepareDragDownload: vi.fn(), prepareDragDirectory: vi.fn(), startDragDownload: vi.fn(), downloadDirectory: vi.fn(), retryTransfer: vi.fn() }
const confirm = vi.mocked(confirmAction)
beforeEach(async () => {
  vi.stubGlobal('window', { sshTool: { ...api } })
  confirm.mockReset().mockResolvedValue(true)
  for (const mock of Object.values(api)) mock.mockReset()
  api.onEvent.mockImplementation(callback => { listener = callback; return () => {} })
  api.bootstrap.mockResolvedValue({ ok: true, value: { profiles: [{ ...profile }], settings: { ...DEFAULT_SETTINGS } } })
  api.connect.mockResolvedValue({ ok: true }); api.newTerminal.mockResolvedValue({ ok: true })
  api.listDirectory.mockResolvedValue({ ok: true, value: { path: '/home/test', entries: [entry, directory] } })
  api.prepareDragDownload.mockResolvedValue({ ok: true, value: { token: 'prepared', name: entry.name, expiresAt: Date.now() + 600_000 } })
  api.prepareDragDirectory.mockResolvedValue({ ok: true, value: { token: 'folder', name: directory.name, expiresAt: Date.now() + 600_000 } })
  Object.assign(state, { sessions: [], activeId: '', transfers: [], connectionDialog: null, credentialsDialog: null, loading: true })
  for (const key of Object.keys(dragDownloads)) delete dragDownloads[key]
  await initialize(); await connect(profile, {})
  state.sessions[0].phase = 'connected'; state.sessions[0].directory = '/home/test'
})
afterEach(() => vi.unstubAllGlobals())

describe('file tree shortcuts', () => {
  it('enters a symbolic link to a directory without expanding it in place', async () => {
    const session = state.sessions[0]
    const mirror: RemoteEntry = { ...directory, name: 'linked-logs', path: '/home/test/linked-logs', type: 'link', linkTarget: 'logs', targetType: 'directory' }
    api.listDirectory.mockResolvedValueOnce({ ok: true, value: { path: directory.path, entries: [] } })
    await openFile(session, mirror)
    expect(api.listDirectory).toHaveBeenCalledExactlyOnceWith(session.id, mirror.path)
    expect(session.directory).toBe(directory.path)
    expect(session.expanded.has(mirror.path)).toBe(false)
  })

  it('refreshes the visible tree and queues an upload refresh behind an older listing', async () => {
    const session = state.sessions[0], root = session.directory
    session.tree[root] = [directory]
    session.expanded.add(directory.path)
    session.expanded.add('/home/test/previously-expanded')
    api.listDirectory.mockImplementation((_id, path) => Promise.resolve({ ok: true, value: { path, entries: path === root ? [directory] : [] } }))
    await refreshFileTree(session, true)
    expect(api.listDirectory.mock.calls).toEqual([[session.id, root], [session.id, directory.path]])

    let resolve!: (value: unknown) => void
    api.listDirectory.mockReturnValueOnce(new Promise(done => { resolve = done }))
    api.listDirectory.mockResolvedValueOnce({ ok: true, value: { path: root, entries: [directory, entry] } })
    const stale = loadDirectory(session, root, true, true)
    listener({ type: 'transfer', transfer: { id: 'recent-upload', sessionId: session.id, direction: 'upload', status: 'completed', remotePath: entry.path } as Transfer })
    resolve({ ok: true, value: { path: root, entries: [directory] } })
    await stale
    await vi.waitFor(() => expect(session.tree[root]).toEqual([directory, entry]))
    expect(api.listDirectory).toHaveBeenCalledTimes(4)
  })

  it('loads the directory without waiting for hard-link detection', async () => {
    const session = state.sessions[0], root = session.directory
    let resolve!: (value: Result<string[]>) => void
    window.sshTool.listHardLinks = vi.fn(() => new Promise<Result<string[]>>(done => { resolve = done }))
    await loadDirectory(session, root, true)
    expect(session.tree[root]).toEqual([entry, directory])
    resolve({ ok: true, value: [entry.name] })
    await vi.waitFor(() => expect(session.tree[root][0]?.hardLink).toBe(true))
    await loadDirectory(session, root, true, true)
    expect(window.sshTool.listHardLinks).toHaveBeenCalledExactlyOnceWith(session.id, root)
    expect(session.tree[root][0].hardLink).toBe(true)
  })

  it('opens the selected directory in a separate terminal and preserves the original workspace and profile', async () => {
    const original = state.sessions[0]
    await openDirectoryTerminal(original, "/home/test/space 'quotes'")
    expect(api.newTerminal).toHaveBeenCalledWith(state.sessions[1].id, original.id, profile.id, "/home/test/space 'quotes'")
    expect(original.phase).toBe('connected'); expect(original.directory).toBe('/home/test')
    expect(state.profiles[0].initialPath).toBe('/home/test')
    expect(state.credentialsDialog).toBeNull(); expect(state.connectionDialog).toBeNull()
  })
  it('keeps the source terminal usable when opening a new terminal fails', async () => {
    api.newTerminal.mockResolvedValue({ ok: false, error: 'directory unavailable' })
    await expect(openDirectoryTerminal(state.sessions[0], '/missing')).rejects.toThrow('directory unavailable')
    expect(state.sessions[0].phase).toBe('connected'); expect(state.sessions[1].phase).toBe('failed')
  })
  it('prepares on the first drag and starts native drag only on a later user gesture', async () => {
    const event = { preventDefault: vi.fn() } as unknown as DragEvent
    dragRemoteFile(event, state.sessions[0], entry)
    await vi.waitFor(() => expect(dragDownloads[dragKey(state.sessions[0].id, entry)]?.status).toBe('ready'))
    expect(api.startDragDownload).not.toHaveBeenCalled()
    dragRemoteFile(event, state.sessions[0], entry)
    expect(api.startDragDownload).toHaveBeenCalledWith(state.sessions[0].id, 'prepared')
    expect(dragDownloads[dragKey(state.sessions[0].id, entry)].expiresAt).toBe((await api.prepareDragDownload.mock.results[0].value).value.expiresAt)
  })
  it('asks before staging a directory and needs a new confirmation once its native export was attempted', async () => {
    const event = { preventDefault: vi.fn() } as unknown as DragEvent, session = state.sessions[0]
    const key = dragKey(session.id, directory)
    confirm.mockResolvedValueOnce(false)
    dragRemoteFile(event, session, directory)
    expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringContaining(directory.path) }))
    expect(api.prepareDragDirectory).not.toHaveBeenCalled()
    dragRemoteFile(event, session, directory)
    await vi.waitFor(() => expect(dragDownloads[key]?.status).toBe('ready'))
    expect(api.prepareDragDirectory).toHaveBeenCalledExactlyOnceWith(session.id, directory.path)
    dragRemoteFile(event, session, directory)
    expect(api.startDragDownload).toHaveBeenCalledExactlyOnceWith(session.id, 'folder')
    expect(dragDownloads[key]).toBeUndefined()
    expect(confirm).toHaveBeenCalledTimes(2)
    dragRemoteFile(event, session, directory)
    expect(confirm).toHaveBeenCalledTimes(3)
  })
  it('confirms a folder drag before using the platform folder picker when native export is unavailable', async () => {
    const event = { preventDefault: vi.fn() } as unknown as DragEvent, session = state.sessions[0]
    window.sshTool.nativeDragDownload = false
    api.downloadDirectory.mockResolvedValue({ ok: true, value: null })
    dragRemoteFile(event, session, directory)
    expect(confirm).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringContaining('选择保存位置') }))
    await vi.waitFor(() => expect(api.downloadDirectory).toHaveBeenCalledExactlyOnceWith(session.id, directory.path))
    expect(api.prepareDragDirectory).not.toHaveBeenCalled()
  })
  it('ignores a late preparation after disconnect and invalidates an unusable drag token', async () => {
    const session = state.sessions[0]
    await prepareDragDownload(session, entry)
    listener({ type: 'notice', dragToken: 'prepared', message: 'cache invalid' })
    expect(dragDownloads[dragKey(session.id, entry)]).toBeUndefined()
    let resolve!: (value: unknown) => void
    api.prepareDragDownload.mockReturnValue(new Promise(done => { resolve = done }))
    const pending = prepareDragDownload(session, entry)
    listener({ type: 'state', sessionId: session.id, phase: 'disconnected', message: 'closed' })
    resolve({ ok: true, value: { token: 'late', name: entry.name, expiresAt: Date.now() + 600_000 } })
    await pending
    expect(dragDownloads[dragKey(session.id, entry)]).toBeUndefined()
  })
  it('retries drag preparation with current remote metadata rather than a removed local destination', async () => {
    const task = { id: 'failed-prepare', sessionId: state.sessions[0].id, remotePath: entry.path, purpose: 'drag' } as Transfer
    await retryTransfer(task)
    expect(api.listDirectory).toHaveBeenCalledWith(task.sessionId, '/home/test')
    expect(api.prepareDragDownload).toHaveBeenCalledWith(task.sessionId, entry.path)
    expect(api.retryTransfer).not.toHaveBeenCalled()
    expect(dragDownloads[dragKey(task.sessionId, entry)].status).toBe('ready')
  })
  it('updates successful connection history and tags from main process events', () => {
    listener({ type: 'profiles', profiles: [{ ...profile, tags: ['训练'], lastConnectedAt: 12345 }] })
    expect(state.profiles[0].tags).toEqual(['训练']); expect(state.profiles[0].lastConnectedAt).toBe(12345)
  })
  it('announces and clears a failed native drag when the preload bridge throws', async () => {
    const dispatched: CustomEvent[] = []
    window.dispatchEvent = (event: Event) => { dispatched.push(event as CustomEvent); return true }
    const session = state.sessions[0]
    await prepareDragDownload(session, entry)
    api.startDragDownload.mockImplementationOnce(() => { throw new Error('bridge unavailable') })
    expect(() => dragRemoteFile({ preventDefault: vi.fn() } as unknown as DragEvent, session, entry)).not.toThrow()
    expect(dispatched.map(event => event.type)).toEqual(['sshtool:drag-out-start', 'sshtool:drag-out-failed'])
    expect(dispatched[1].detail).toEqual({ sessionId: session.id, token: 'prepared' })
    expect(session.phase).toBe('connected')
  })
})

describe('completed transfer records', () => {
  it('removes one success or all successes while preserving other transfer statuses and future completions', () => {
    const download: Transfer = {
      id: 'completed-download', sessionId: state.sessions[0].id, name: entry.name, direction: 'download',
      remotePath: entry.path, size: entry.size, transferred: entry.size, speed: 0, status: 'completed'
    }
    const upload: Transfer = { ...download, id: 'completed-upload', direction: 'upload' }
    const running: Transfer = { ...download, id: 'running-download', transferred: 1, status: 'running' }
    const failed: Transfer = { ...download, id: 'failed-download', transferred: 1, status: 'failed', error: '连接中断' }
    for (const transfer of [download, upload, running, failed]) listener({ type: 'transfer', transfer })

    dismissCompletedTransfers(download.id)
    dismissCompletedTransfers(running.id)
    expect(state.transfers.map(transfer => transfer.id)).toEqual([failed.id, running.id, upload.id])

    dismissCompletedTransfers()
    listener({ type: 'transfer', transfer: download })
    listener({ type: 'transfer', transfer: upload })
    expect(state.transfers).toEqual([failed, running])

    const finished = { ...running, status: 'completed', transferred: running.size } as const
    listener({ type: 'transfer', transfer: finished })
    expect(state.transfers).toEqual([failed, finished])
  })
})
