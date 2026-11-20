import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'
import { state, connect, newTerminal, renameSession, sessionTooltip, requestConnection, newProfile, initialize, dragDownloads, dragKey, loadDirectory, navigate, jumpToTerminalDirectory, openFile, saveDocument, reloadDocument, closeSession, closeSessions, closeDocuments, type DocumentTab } from '../src/renderer/src/workbench'
import { terminalSplitEdge, useTerminalLayout } from '../src/renderer/src/terminal-layout'
import type { AppEvent, Profile, RemoteEntry, Result, Preview } from '../src/shared/types'
import { connectionIdentity } from '../src/shared/connection'
import { DEFAULT_SETTINGS } from '../src/shared/settings'
import { confirmAction } from '../src/renderer/src/confirmation'

vi.mock('../src/renderer/src/confirmation', () => ({ confirmAction: vi.fn() }))

const api = { connect: vi.fn(), newTerminal: vi.fn(), bootstrap: vi.fn(), onEvent: vi.fn(), listDirectory: vi.fn(), preview: vi.fn(), saveText: vi.fn(), disconnect: vi.fn() }
let listener: (event: AppEvent) => void
const profile: Profile = { ...newProfile(), id: 'test-profile', name: 'Test', host: '127.0.0.1', username: 'test' }
const entry: RemoteEntry = { name: 'hello.txt', path: '/home/test/hello.txt', type: 'file', size: 5, modified: 1, mode: 0o100644 }
const preview: Preview = { path: entry.path, kind: 'text', content: 'saved', version: 'hash', size: 5, modified: 1 }
function deferred<T>() { let resolve!: (result: Result<T>) => void; const promise = new Promise<Result<T>>(done => { resolve = done }); return { promise, resolve } }
function document(): DocumentTab { return { path: entry.path, preview: { ...preview }, content: 'unsaved edit', saved: preview.content, busy: false, error: '', conflict: false } }

beforeEach(async () => {
  vi.stubGlobal('window', { sshTool: api })
  vi.mocked(confirmAction).mockReset().mockResolvedValue(true)
  for (const mock of Object.values(api)) mock.mockReset()
  api.connect.mockReset().mockResolvedValue({ ok: true }); api.newTerminal.mockReset().mockResolvedValue({ ok: true })
  api.onEvent.mockImplementation(callback => { listener = callback; return () => {} })
  api.bootstrap.mockResolvedValue({ ok: true, value: { profiles: [{ ...profile }], settings: { ...DEFAULT_SETTINGS } } })
  api.listDirectory.mockResolvedValue({ ok: true, value: { path: '/home/test', entries: [] } })
  api.preview.mockResolvedValue({ ok: true, value: { ...preview } })
  api.saveText.mockResolvedValue({ ok: true, value: { ...preview } })
  api.disconnect.mockResolvedValue({ ok: true })
  Object.assign(state, { profiles: [{ ...profile }], sessions: [], activeId: '', selectedProfileId: profile.id, connectionDialog: null, credentialsDialog: null, trust: [], transfers: [], fileBusy: false, toast: '', fatalError: '' })
  for (const key of Object.keys(dragDownloads)) delete dragDownloads[key]
  await initialize()
})

describe('reconnection workspace isolation', () => {
  it('preserves documents and history while replacing the connection UUID and clearing old transient state', async () => {
    await connect(profile, { password: 'test-only' })
    const session = state.sessions[0], previousId = session.id
    session.phase = 'failed'; session.directory = '/home/test'; session.sftpError = 'old SFTP failure'
    session.documents.push(document()); session.documents[0].busy = true
    session.terminalBuffer.push('b2xkIGhpc3Rvcnk='); session.terminalBytes = 16
    session.loading.add('/home/test')
    const oldKey = dragKey(previousId, entry), otherKey = dragKey('other-session', entry)
    dragDownloads[oldKey] = { status: 'ready', file: { token: 'old-token', name: entry.name, expiresAt: Date.now() + 1000 } }
    dragDownloads[otherKey] = { status: 'preparing', request: 2 }
    state.trust.push({ type: 'hostkey', sessionId: previousId, host: 'old.example', fingerprint: 'old', keyType: 'ssh-rsa' }, { type: 'hostkey', sessionId: 'other-session', host: 'other.example', fingerprint: 'other', keyType: 'ssh-rsa' })
    const doc = session.documents[0], history = session.terminalBuffer
    api.connect.mockClear()
    await connect(profile, { password: 'new-login' }, previousId)
    expect(state.sessions).toHaveLength(1); expect(state.sessions[0]).toBe(session)
    expect(session.id).not.toBe(previousId); expect(session.id).toMatch(/^[a-f0-9-]{36}$/)
    expect(api.connect).toHaveBeenCalledExactlyOnceWith(session.id, profile.id, { password: 'new-login' }, undefined, connectionIdentity(profile))
    expect(state.activeId).toBe(session.id); expect(session.phase).toBe('connecting')
    expect(session.documents[0]).toBe(doc); expect(doc.content).toBe('unsaved edit'); expect(doc.saved).toBe('saved'); expect(doc.busy).toBe(false)
    expect(session.terminalBuffer).toBe(history); expect(session.terminalBytes).toBe(16)
    expect(session.loading.size).toBe(0); expect(session.sftpError).toBeUndefined()
    expect(dragDownloads[oldKey]).toBeUndefined(); expect(dragDownloads[otherKey]).toBeDefined()
    expect(state.trust.map(item => item.sessionId)).toEqual(['other-session'])
    listener({ type: 'terminal', sessionId: previousId, data: 'c3RhbGU=' })
    listener({ type: 'hostkey', sessionId: previousId, host: 'stale.example', fingerprint: 'stale', keyType: 'ssh-rsa' })
    listener({ type: 'state', sessionId: previousId, phase: 'connected', message: 'stale connection', home: '/stale' })
    expect(session.phase).toBe('connecting'); expect(session.terminalBuffer).toEqual(['b2xkIGhpc3Rvcnk='])
    expect(state.trust.map(item => item.sessionId)).toEqual(['other-session']); expect(api.listDirectory).not.toHaveBeenCalled()
    listener({ type: 'state', sessionId: session.id, phase: 'connected', message: 'new connection', home: '/home/test' })
    await vi.waitFor(() => expect(api.listDirectory).toHaveBeenCalledExactlyOnceWith(session.id, '/home/test'))
  })

  it('keeps the old endpoint and unsaved file in the original workspace after connecting to an edited target', async () => {
    await connect(profile, {})
    const original = state.sessions[0], originalId = original.id
    original.phase = 'connected'
    original.terminalBuffer.push('b2xkIGhpc3Rvcnk=')
    original.documents.push(document())
    const oldDoc = original.documents[0]
    original.activeDocument = oldDoc.path
    original.tree['/home/test'] = [entry]
    const updated = { ...profile, host: '2001:db8::1', port: 2222, username: 'other', hasCredentials: true }
    listener({ type: 'profiles', profiles: [updated] })
    expect(original.endpoint).toBe('test@127.0.0.1:22')
    original.phase = 'disconnected'

    requestConnection(updated, original.id)
    await vi.waitFor(() => expect(api.connect).toHaveBeenCalledTimes(2))
    const replacement = state.sessions[1]
    expect(original.id).toBe(originalId)
    expect(original.phase).toBe('disconnected')
    expect(original.endpoint).toBe('test@127.0.0.1:22')
    expect(original.documents[0]).toBe(oldDoc)
    expect(original.activeDocument).toBe(oldDoc.path)
    expect(original.terminalBuffer).toEqual(['b2xkIGhpc3Rvcnk='])
    expect(replacement.endpoint).toBe('other@[2001:db8::1]:2222')
    expect(replacement.id).not.toBe(originalId)
    expect(state.activeId).toBe(replacement.id)
    expect(replacement.documents).toEqual([])
    expect(replacement.terminalBuffer).toEqual([])
    expect(replacement.tree).toEqual({})
    expect(api.connect).toHaveBeenLastCalledWith(replacement.id, profile.id, {}, undefined, connectionIdentity(updated))

    replacement.phase = 'connected'
    await openFile(replacement, entry)
    expect(replacement.documents[0].content).toBe(preview.content)
    await saveDocument(original, oldDoc)
    await saveDocument(replacement, oldDoc)
    expect(api.saveText).not.toHaveBeenCalled()
    const newDoc = replacement.documents[0]
    newDoc.content = 'new target edit'
    await saveDocument(replacement, newDoc)
    expect(api.saveText).toHaveBeenCalledExactlyOnceWith(replacement.id, entry.path, newDoc.content, preview.version)
    expect(oldDoc.content).toBe('unsaved edit')
  })

  it('opens a separate workspace when the saved jump chain changes', async () => {
    const gateway = { ...profile, id: 'gateway', host: 'first-gateway.test' }
    const target = { ...profile, jumpProfileId: gateway.id, jumpIdentity: connectionIdentity(gateway) }
    state.profiles = [gateway, target]
    await connect(target, {}, undefined, undefined, undefined, { password: 'jump-only' })
    const original = state.sessions[0]
    original.phase = 'disconnected'
    original.documents.push(document())

    const changedGateway = { ...gateway, host: 'other-gateway.test' }
    const changedTarget = { ...target, jumpIdentity: connectionIdentity(changedGateway) }
    state.profiles = [changedGateway, changedTarget]
    await connect(changedTarget, {}, original.id, undefined, undefined, { password: 'jump-only' })

    expect(state.sessions).toHaveLength(2)
    expect(original.documents[0].content).toBe('unsaved edit')
    expect(state.sessions[1].documents).toEqual([])
    expect(api.connect).toHaveBeenLastCalledWith(state.sessions[1].id, profile.id, {}, { password: 'jump-only' }, connectionIdentity(changedTarget))
  })

  it('ignores old directory RPC results and leaves a new request for the same path loading', async () => {
    await connect(profile, {}); const session = state.sessions[0]; session.phase = 'connected'; session.directory = '/home/test'
    const oldID = session.id, old = deferred<{ path: string; entries: RemoteEntry[] }>(), current = deferred<{ path: string; entries: RemoteEntry[] }>()
    api.listDirectory.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const oldRequest = loadDirectory(session, '/home/test', true)
    expect(api.listDirectory).toHaveBeenLastCalledWith(oldID, '/home/test')
    session.phase = 'failed'; await connect(profile, {}, oldID); session.phase = 'connected'
    const newRequest = loadDirectory(session, '/home/test', true)
    expect(api.listDirectory).toHaveBeenLastCalledWith(session.id, '/home/test')
    old.resolve({ ok: true, value: { path: '/stale', entries: [{ ...entry, name: 'stale.txt' }] } }); await oldRequest
    expect(session.tree['/home/test']).toBeUndefined(); expect(session.tree['/stale']).toBeUndefined()
    expect(session.directory).toBe('/home/test'); expect(session.loading.has('/home/test')).toBe(true)
    current.resolve({ ok: true, value: { path: '/home/test', entries: [entry] } }); await newRequest
    expect(session.tree['/home/test']).toEqual([entry]); expect(session.loading.size).toBe(0)
  })

  it('ignores a late preview and keeps the new preview busy until its own response arrives', async () => {
    await connect(profile, {}); const session = state.sessions[0]; session.phase = 'connected'
    const old = deferred<Preview>(), current = deferred<Preview>(); api.preview.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const oldRequest = openFile(session, entry); expect(state.fileBusy).toBe(true)
    session.phase = 'failed'; await connect(profile, {}, session.id); expect(state.fileBusy).toBe(false); session.phase = 'connected'
    const newRequest = openFile(session, entry); expect(state.fileBusy).toBe(true)
    old.resolve({ ok: true, value: { ...preview, content: 'stale preview' } }); await oldRequest
    expect(session.documents).toEqual([]); expect(session.activeDocument).toBe('terminal'); expect(state.fileBusy).toBe(true)
    current.resolve({ ok: true, value: { ...preview, content: 'current preview' } }); await newRequest
    expect(session.documents).toHaveLength(1); expect(session.documents[0].content).toBe('current preview'); expect(state.fileBusy).toBe(false)
  })

  it('shares duplicate preview reads and preserves the most recently selected file', async () => {
    await connect(profile, {})
    const session = state.sessions[0]; session.phase = 'connected'
    const second = { ...entry, path: '/home/test/second.txt' }
    const older = deferred<Preview>(), latest = deferred<Preview>()
    api.preview.mockReturnValueOnce(older.promise).mockReturnValueOnce(latest.promise)
    const firstOpen = openFile(session, entry)
    const doubleClick = openFile(session, entry)
    const secondOpen = openFile(session, second)
    expect(api.preview.mock.calls.map(args => args[1])).toEqual([entry.path, second.path])
    latest.resolve({ ok: true, value: { ...preview, path: second.path } })
    await secondOpen
    older.resolve({ ok: true, value: preview })
    await Promise.all([firstOpen, doubleClick])
    expect(session.documents.map(doc => doc.path)).toEqual([second.path, entry.path])
    expect(session.activeDocument).toBe(second.path)
  })

  it('does not let an old save response change the new document state or clear its current save marker', async () => {
    await connect(profile, {}); const session = state.sessions[0]; session.phase = 'connected'; session.documents.push(document()); const doc = session.documents[0]
    const old = deferred<Preview>(), current = deferred<Preview>(); api.saveText.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const oldRequest = saveDocument(session, doc); expect(doc.busy).toBe(true)
    session.phase = 'failed'; await connect(profile, {}, session.id); expect(doc.busy).toBe(false); session.phase = 'connected'; doc.content = 'latest edit'
    const newRequest = saveDocument(session, doc); expect(doc.busy).toBe(true)
    old.resolve({ ok: true, value: { ...preview, content: 'unsaved edit', version: 'old-commit' } }); await oldRequest
    expect(doc.saved).toBe('saved'); expect(doc.preview.version).toBe('hash'); expect(doc.content).toBe('latest edit'); expect(doc.busy).toBe(true)
    current.resolve({ ok: true, value: { ...preview, content: 'latest edit', version: 'new-commit' } }); await newRequest
    expect(doc.saved).toBe('latest edit'); expect(doc.preview.version).toBe('new-commit'); expect(doc.busy).toBe(false)
  })

  it('does not apply a late connection failure to the replacement workspace', async () => {
    const old = deferred<void>(); api.connect.mockReturnValueOnce(old.promise)
    const oldRequest = connect(profile, {})
    await vi.waitFor(() => expect(api.connect).toHaveBeenCalledTimes(1))
    const session = state.sessions[0], previousID = session.id; session.phase = 'failed'
    await connect(profile, {}, previousID); const newID = session.id; session.phase = 'connected'; session.message = 'new session works'
    old.resolve({ ok: false, error: 'old request failed late' }); await expect(oldRequest).resolves.toBeUndefined()
    expect(session.id).toBe(newID); expect(session.phase).toBe('connected'); expect(session.message).toBe('new session works')
    expect(api.connect.mock.calls.map(args => args[0])).toEqual([previousID, newID])
  })

  it('ignores old navigation and reload responses after reconnection without replacing preserved edits', async () => {
    await connect(profile, {}); const session = state.sessions[0]; session.phase = 'connected'; session.documents.push(document()); const doc = session.documents[0]
    const oldDirectory = deferred<{ path: string; entries: RemoteEntry[] }>(), oldPreview = deferred<Preview>()
    api.listDirectory.mockReturnValueOnce(oldDirectory.promise); api.preview.mockReturnValueOnce(oldPreview.promise)
    const navigating = navigate('/stale-directory'), reloading = reloadDocument(session, doc)
    await vi.waitFor(() => expect(api.preview).toHaveBeenCalledTimes(1))
    session.phase = 'failed'; await connect(profile, {}, session.id); session.phase = 'connected'; session.directory = '/new-directory'; doc.content = 'preserved newer edit'
    oldDirectory.resolve({ ok: true, value: { path: '/stale-directory', entries: [entry] } }); oldPreview.resolve({ ok: true, value: { ...preview, content: 'stale read', version: 'stale-hash' } })
    await Promise.all([navigating, reloading])
    expect(session.directory).toBe('/new-directory'); expect(session.tree['/stale-directory']).toBeUndefined()
    expect(doc.content).toBe('preserved newer edit'); expect(doc.saved).toBe('saved'); expect(doc.preview.version).toBe('hash')
    // Even in the current connection, edits made during a pending reload win.
    const latest = deferred<Preview>(); api.preview.mockReturnValueOnce(latest.promise)
    const currentReload = reloadDocument(session, doc)
    await vi.waitFor(() => expect(api.preview).toHaveBeenCalledTimes(2))
    doc.content = 'typed while loading'
    latest.resolve({ ok: true, value: { ...preview, content: 'read before typing' } }); await currentReload
    expect(doc.content).toBe('typed while loading'); expect(doc.saved).toBe('saved')
  })

  it('does not remove a reconnected workspace when the old close RPC returns late', async () => {
    await connect(profile, {}); const session = state.sessions[0]; session.phase = 'failed'; const oldID = session.id
    const closing = deferred<void>(); api.disconnect.mockReturnValueOnce(closing.promise)
    const oldClose = closeSession(session)
    await connect(profile, {}, oldID); const newID = session.id
    closing.resolve({ ok: true, value: undefined }); await oldClose
    expect(state.sessions).toHaveLength(1); expect(state.sessions[0]).toBe(session); expect(session.id).toBe(newID)
    expect(api.disconnect).toHaveBeenCalledExactlyOnceWith(oldID)
  })
})
afterEach(() => vi.unstubAllGlobals())

describe('closing groups of tabs', () => {
  it('combines disconnect and unsaved changes in a single confirmation for one tab', async () => {
    await connect(profile, {})
    const [session] = state.sessions
    session.documents.push(document())
    vi.mocked(confirmAction).mockResolvedValueOnce(false)

    await closeSession(session)
    expect(confirmAction).toHaveBeenCalledExactlyOnceWith({
      title: '关闭会话标签？',
      message: '将断开 SSH 连接。\n未保存的文件修改将丢失。',
      action: '关闭',
      danger: true
    })
    expect(api.disconnect).not.toHaveBeenCalled()
    expect(state.sessions).toEqual([session])
  })

  it('keeps the right-clicked session and confirms the combined consequences before disconnecting', async () => {
    await connect(profile, {})
    await connect(profile, {})
    await connect(profile, {})
    const [keep, second, third] = state.sessions
    second.documents.push(document())
    vi.mocked(confirmAction).mockResolvedValueOnce(false)

    await closeSessions(keep)
    expect(api.disconnect).not.toHaveBeenCalled()
    expect(confirmAction).toHaveBeenCalledExactlyOnceWith({ title: '关闭 2 个会话标签？', message: '将断开 2 个 SSH 连接。\n将丢弃 1 个文件的未保存修改。', action: '关闭', danger: true })
    expect(state.sessions).toHaveLength(3)

    await closeSessions(keep)
    expect(api.disconnect.mock.calls).toEqual([[second.id], [third.id]])
    expect(state.sessions).toEqual([keep])
    expect(state.activeId).toBe(keep.id)

    await closeSessions()
    expect(state.sessions).toEqual([])
    expect(state.activeId).toBe('')
  })

  it('keeps a tab whose disconnect fails and avoids duplicate requests during a batch close', async () => {
    await connect(profile, {})
    await connect(profile, {})
    const [first, second] = state.sessions
    const disconnect = deferred<void>()
    api.disconnect.mockReturnValueOnce(disconnect.promise)

    const closing = closeSessions()
    await vi.waitFor(() => expect(api.disconnect).toHaveBeenCalledTimes(2))
    await closeSession(first)
    disconnect.resolve({ ok: false, error: 'disconnect failed' })
    await expect(closing).rejects.toThrow('disconnect failed')
    expect(api.disconnect.mock.calls).toEqual([[first.id], [second.id]])
    expect(state.sessions).toEqual([first])
    expect(state.activeId).toBe(first.id)
  })

  it('keeps the requested file and saves already in progress when closing other files', async () => {
    await connect(profile, {})
    const [session] = state.sessions
    session.documents.push(document(), { ...document(), path: '/kept.txt' }, { ...document(), path: '/saving.txt', busy: true })
    const [dirty, keep, saving] = session.documents
    session.activeDocument = dirty.path
    vi.mocked(confirmAction).mockResolvedValueOnce(false)

    await closeDocuments(session, keep)
    expect(session.documents).toHaveLength(3)
    await closeDocuments(session, keep)
    expect(session.documents).toEqual([keep, saving])
    expect(session.activeDocument).toBe(keep.path)
    await closeDocuments(session)
    expect(session.documents).toEqual([saving])
    expect(session.activeDocument).toBe('terminal')
  })
})

describe('jump to terminal directory', () => {
  it('opens the focused split terminal directory with automatic following off', async () => {
    await connect(profile, {})
    await connect(profile, {})
    const [first, second] = state.sessions
    Object.assign(first, { phase: 'connected', directory: '/home/test', terminalDirectory: '/work/first run' })
    Object.assign(second, { phase: 'connected', directory: '/home/test', terminalDirectory: '/work/second' })
    const scope = effectScope()
    try {
      const { openSplit, confirmSplit, focusPane } = scope.run(useTerminalLayout)!
      openSplit('horizontal')
      await confirmSplit(first.id)
      focusPane(first)
      api.listDirectory.mockResolvedValueOnce({ ok: true, value: { path: '/work/first run', entries: [] } })
      await jumpToTerminalDirectory()
      expect(api.listDirectory).toHaveBeenCalledExactlyOnceWith(first.id, '/work/first run')
      expect(first.directory).toBe('/work/first run')
      expect(second.directory).toBe('/home/test')
    } finally { scope.stop() }
  })

  it('asks for a directory report when the connected terminal has not sent one', async () => {
    await connect(profile, {})
    state.sessions[0].phase = 'connected'
    await expect(jumpToTerminalDirectory()).rejects.toThrow('OSC 7')
    expect(api.listDirectory).not.toHaveBeenCalled()
  })

  it('keeps the latest followed directory when an earlier jump completes late', async () => {
    await connect(profile, {})
    const session = state.sessions[0]
    Object.assign(session, { phase: 'connected', directory: '/home/test', terminalDirectory: '/work/previous' })
    const previous = deferred<{ path: string; entries: RemoteEntry[] }>()
    api.listDirectory.mockReturnValueOnce(previous.promise)
    const scope = effectScope()
    try {
      const { followDirectory } = scope.run(useTerminalLayout)!
      const jumping = jumpToTerminalDirectory()
      session.terminalDirectory = '/work/current'
      api.listDirectory.mockResolvedValueOnce({ ok: true, value: { path: '/work/current', entries: [] } })
      followDirectory.value = true
      await nextTick()
      expect(api.listDirectory).toHaveBeenLastCalledWith(session.id, '/work/current')
      await vi.waitFor(() => expect(session.directory).toBe('/work/current'))
      previous.resolve({ ok: true, value: { path: '/work/previous', entries: [] } })
      await jumping
      expect(session.directory).toBe('/work/current')
    } finally { scope.stop() }
  })
})

describe('new terminal routing', () => {
  it('numbers terminal names per saved connection and retains a custom name through reconnect', async () => {
    const other = { ...profile, id: 'other', name: 'Other' }
    const first = (await connect(profile, {}))!
    const second = (await connect(profile, {}))!
    const independent = (await connect(other, {}))!
    expect([first.name, second.name, independent.name]).toEqual(['Test · 终端 1', 'Test · 终端 2', 'Other · 终端 1'])
    renameSession(first, '  训练日志  ')
    first.terminalDirectory = '/home/test/train'
    expect(sessionTooltip(first)).toBe('训练日志\ntest@127.0.0.1:22\n终端目录 /home/test/train')
    first.phase = 'failed'
    await connect(profile, {}, first.id)
    expect(first.name).toBe('训练日志')
    expect(first.terminalNumber).toBe(1)
  })

  it('creates a terminal from the logged-in session without opening the editor or asking for credentials', async () => {
    await connect(profile, { password: 'test-only' })
    const original = state.sessions[0]; original.phase = 'connected'
    newTerminal()
    await vi.waitFor(() => expect(api.newTerminal).toHaveBeenCalledWith(expect.any(String), original.id, profile.id))
    expect(state.sessions).toHaveLength(2)
    expect(state.activeId).not.toBe(original.id)
    expect(api.connect).toHaveBeenCalledTimes(1)
    expect(state.connectionDialog).toBeNull()
    expect(state.credentialsDialog).toBeNull()
  })
  it('requests only credentials when no authenticated connection or saved password is available', () => {
    newTerminal()
    expect(state.credentialsDialog?.profile.id).toBe(profile.id)
    expect(state.connectionDialog).toBeNull()
    expect(api.newTerminal).not.toHaveBeenCalled()
  })
  it('does not reuse the previous server after a profile changes its login identity', async () => {
    await connect(profile, { password: 'test-only' }); state.sessions[0].phase = 'connected'
    const changed = { ...profile, host: 'another.example' }
    requestConnection(changed)
    expect(api.newTerminal).not.toHaveBeenCalled()
    expect(state.credentialsDialog?.profile.host).toBe('another.example')
    expect(state.connectionDialog).toBeNull()
  })
})

describe('terminal split selection', () => {
  it('uses the explicitly selected terminal, bounds the ratio and preserves the pair on reconnect', async () => {
    const first = (await connect(profile, {}))!
    const second = (await connect(profile, {}))!
    const third = (await connect(profile, {}))!
    for (const session of [first, second, third]) session.phase = 'connected'
    first.activeDocument = entry.path
    const scope = effectScope()
    try {
      const { openSplit, confirmSplit, splitChoice, splitTargets, shown, splitRatio, setSplitRatio } = scope.run(useTerminalLayout)!
      openSplit('horizontal')
      expect(splitChoice.value?.source).toBe(third)
      expect(splitTargets.value.map(session => session.id)).toEqual([first.id, second.id])
      expect(shown.value).toBeNull()
      await confirmSplit(first.id)
      expect(shown.value).toEqual({ first: third.id, second: first.id, direction: 'horizontal' })
      expect(first.activeDocument).toBe(entry.path)
      expect(api.newTerminal).not.toHaveBeenCalled()
      setSplitRatio(95)
      expect(splitRatio.value).toBe(80)
      setSplitRatio(10)
      expect(splitRatio.value).toBe(20)
      setSplitRatio(65)
      first.phase = 'disconnected'
      await connect(profile, {}, first.id)
      expect(shown.value).toEqual({ first: third.id, second: first.id, direction: 'horizontal' })
      expect(splitRatio.value).toBe(65)
      await closeSession(first)
      expect(shown.value).toBeNull()
    } finally { scope.stop() }
  })

  it('opens a new shell on the source transport only when chosen', async () => {
    const source = (await connect(profile, {}))!
    source.phase = 'connected'
    const scope = effectScope()
    try {
      const { openSplit, confirmSplit, canCreateSplit, shown } = scope.run(useTerminalLayout)!
      openSplit('vertical')
      expect(canCreateSplit.value).toBe(true)
      expect(api.newTerminal).not.toHaveBeenCalled()
      await confirmSplit('')
      expect(api.newTerminal).toHaveBeenCalledExactlyOnceWith(state.sessions[1].id, source.id, profile.id)
      expect(shown.value).toEqual({ first: source.id, second: state.sessions[1].id, direction: 'vertical' })
    } finally { scope.stop() }
  })
})

describe('terminal tab drag split', () => {
  function pointerFixture(...ids: string[]) {
    const view = new EventTarget()
    const button = { ownerDocument: { defaultView: view }, setPointerCapture: vi.fn(), hasPointerCapture: vi.fn(() => true), releasePointerCapture: vi.fn() }
    const workspace = {
      querySelectorAll: () => ids.map((id, index) => ({
        dataset: { terminalSession: id },
        getBoundingClientRect: () => ({ left: index * 510, top: 100, width: 500, height: 300 })
      }))
    } as unknown as HTMLElement
    const pointer = (clientX = 250, clientY = 15, buttons = 1) => ({
      currentTarget: button, clientX, clientY, isPrimary: true, pointerId: 1, button: 0, buttons, preventDefault: vi.fn()
    }) as unknown as PointerEvent
    return { button, view, workspace, pointer }
  }

  function tabClick(type = 'click', detail = 1) { return { type, detail } as MouseEvent }

  it('maps visible edges and rejects points in the middle, outside, or in a collapsed pane', () => {
    const bounds = { left: 20, top: 50, width: 500, height: 300 }
    expect(terminalSplitEdge(bounds, 75, 180)).toBe('left')
    expect(terminalSplitEdge(bounds, 455, 180)).toBe('right')
    expect(terminalSplitEdge(bounds, 300, 79)).toBe('top')
    expect(terminalSplitEdge(bounds, 300, 320)).toBe('bottom')
    expect(terminalSplitEdge(bounds, 270, 200)).toBeUndefined()
    expect(terminalSplitEdge(bounds, 19, 170)).toBeUndefined()
    expect(terminalSplitEdge({ ...bounds, width: 0 }, 20, 170)).toBeUndefined()
  })

  it('places a dragged tab at the requested edge without changing the other tab or losing an open document', async () => {
    const dragged = (await connect(profile, {}))!
    const anchor = (await connect(profile, {}))!
    for (const session of [dragged, anchor]) {
      session.phase = 'connected'
      session.documents.push(document())
      session.activeDocument = entry.path
    }
    const scope = effectScope()
    const { button, workspace, pointer } = pointerFixture(anchor.id)
    try {
      const { startSplitDrag, moveSplitDrag, finishSplitDrag, splitDragSource, splitDragTarget, consumeSplitDragClick, shown } = scope.run(() => useTerminalLayout(() => workspace))!
      startSplitDrag(pointer(), dragged)
      expect(button.setPointerCapture).toHaveBeenCalledExactlyOnceWith(1)
      expect(splitDragSource.value).toBeNull()
      moveSplitDrag(pointer(20, 250))
      expect(splitDragSource.value).toBe(dragged)
      expect(splitDragTarget.value).toEqual({ anchorId: anchor.id, edge: 'left' })
      finishSplitDrag(pointer(20, 250, 0))
      expect(splitDragSource.value).toBeNull()
      expect(splitDragTarget.value).toBeUndefined()
      expect(button.releasePointerCapture).toHaveBeenCalledExactlyOnceWith(1)
      expect(consumeSplitDragClick(tabClick())).toBe(true)
      expect(consumeSplitDragClick(tabClick('dblclick', 2))).toBe(true)
      expect(shown.value).toEqual({ first: dragged.id, second: anchor.id, direction: 'horizontal' })
      expect(state.activeId).toBe(anchor.id)
      expect(anchor.activeDocument).toBe('terminal')
      expect(dragged.activeDocument).toBe(entry.path)
      expect(anchor.documents[0].content).toBe('unsaved edit')
      expect(dragged.documents[0].content).toBe('unsaved edit')
      expect(api.newTerminal).not.toHaveBeenCalled()
    } finally { scope.stop() }
  })

  it('reorders or replaces a visible split and rejects unrelated or disconnected targets', async () => {
    const first = (await connect(profile, {}))!
    const second = (await connect(profile, {}))!
    const third = (await connect(profile, {}))!
    for (const session of [first, second, third]) session.phase = 'connected'
    state.activeId = first.id
    const scope = effectScope()
    const { workspace, pointer } = pointerFixture(first.id, second.id, third.id)
    try {
      const { startSplitDrag, moveSplitDrag, finishSplitDrag, splitDragSource, shown, setSplitRatio, splitRatio } = scope.run(() => useTerminalLayout(() => workspace))!
      const dragTo = (session: typeof first, x: number, y: number) => {
        startSplitDrag(pointer(), session)
        finishSplitDrag(pointer(x, y, 0))
      }
      dragTo(second, 1040, 250)
      expect(shown.value).toBeNull()
      dragTo(first, 20, 250)
      expect(shown.value).toBeNull()
      dragTo(second, 480, 250)
      setSplitRatio(65)
      dragTo(second, 20, 250)
      expect(shown.value).toEqual({ first: second.id, second: first.id, direction: 'horizontal' })
      expect(splitRatio.value).toBe(35)
      dragTo(third, 740, 380)
      expect(shown.value).toEqual({ first: second.id, second: third.id, direction: 'vertical' })
      expect(state.activeId).toBe(second.id)
      expect(splitRatio.value).toBe(50)
      startSplitDrag(pointer(), first)
      moveSplitDrag(pointer(1250, 115))
      expect(splitDragSource.value).toBe(first)
      first.phase = 'disconnected'
      expect(splitDragSource.value).toBeNull()
      finishSplitDrag(pointer(1250, 115, 0))
      expect(shown.value).toEqual({ first: second.id, second: third.id, direction: 'vertical' })
    } finally { scope.stop() }
  })

  it('allows regular tab clicks and cancels a drag on Escape, pointer cancellation, or window blur', async () => {
    const dragged = (await connect(profile, {}))!
    const anchor = (await connect(profile, {}))!
    for (const session of [dragged, anchor]) session.phase = 'connected'
    const scope = effectScope()
    const { view, workspace, pointer } = pointerFixture(anchor.id)
    try {
      const { startSplitDrag, moveSplitDrag, finishSplitDrag, cancelSplitDrag, consumeSplitDragClick, splitDragTarget, shown } = scope.run(() => useTerminalLayout(() => workspace))!
      startSplitDrag(pointer(), dragged)
      finishSplitDrag(pointer(251, 16, 0))
      expect(consumeSplitDragClick(tabClick())).toBe(false)
      expect(consumeSplitDragClick(tabClick('dblclick', 2))).toBe(false)
      const escape = Object.assign(new Event('keydown', { cancelable: true }), { key: 'Escape' })
      const cancellations = [
        () => view.dispatchEvent(escape),
        () => cancelSplitDrag(pointer()),
        () => view.dispatchEvent(new Event('blur'))
      ]
      for (const cancel of cancellations) {
        startSplitDrag(pointer(), dragged)
        moveSplitDrag(pointer(245, 113))
        expect(splitDragTarget.value).toEqual({ anchorId: anchor.id, edge: 'top' })
        cancel()
        finishSplitDrag(pointer(245, 113, 0))
        expect(splitDragTarget.value).toBeUndefined()
        expect(shown.value).toBeNull()
      }
      expect(escape.defaultPrevented).toBe(true)
    } finally { scope.stop() }
  })
})
