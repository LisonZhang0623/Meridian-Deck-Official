import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { answerHostKey, connect, initialize, newProfile, newTerminal, requestConnection, state } from '../src/renderer/src/workbench'
import { connectionIdentity } from '../src/shared/connection'
import { DEFAULT_SETTINGS } from '../src/shared/settings'
import type { AppEvent, Profile } from '../src/shared/types'

const gateway = { ...newProfile(), id: 'gateway', name: 'Gateway', host: 'gateway.test', username: 'test', hasCredentials: false }
const target = { ...newProfile(), id: 'target', name: 'Target', host: 'internal.test', username: 'test', jumpProfileId: gateway.id, jumpIdentity: connectionIdentity(gateway), hasCredentials: true }
const api = { onEvent: vi.fn(), bootstrap: vi.fn(), connect: vi.fn(), connectChain: vi.fn(), newTerminal: vi.fn(), trust: vi.fn() }
let event: (value: AppEvent) => void
beforeEach(async () => {
  vi.stubGlobal('window', { sshTool: api })
  for (const mock of Object.values(api)) mock.mockReset()
  api.onEvent.mockImplementation(callback => { event = callback; return () => {} })
  api.bootstrap.mockResolvedValue({ ok: true, value: { settings: DEFAULT_SETTINGS, profiles: [gateway, target] } })
  api.connect.mockResolvedValue({ ok: true }); api.connectChain.mockResolvedValue({ ok: true }); api.newTerminal.mockResolvedValue({ ok: true })
  Object.assign(state, { sessions: [], trust: [], activeId: '', selectedProfileId: target.id, connectionDialog: null, credentialsDialog: null })
  await initialize(); state.selectedProfileId = target.id
})
afterEach(() => vi.unstubAllGlobals())

describe('jump login routing', () => {
  it('routes a saved chain with distinct credentials for each unsaved hop', async () => {
    const outer = { ...gateway, id: 'outer', authType: 'password' as const }
    const inner = { ...gateway, jumpProfileId: outer.id }
    state.profiles = [outer, inner, target]
    requestConnection(target)
    expect(state.credentialsDialog?.profile.id).toBe(target.id)
    const hops = { [outer.id]: { password: 'outer-only' }, [inner.id]: { password: 'inner-only' } }
    await connect(target, {}, undefined, undefined, undefined, undefined, hops)
    expect(api.connectChain).toHaveBeenCalledWith(state.sessions[0].id, target.id, {}, hops, connectionIdentity(target))
    expect(api.connect).not.toHaveBeenCalled()
  })
  it('requests missing gateway credentials and passes both identities separately', async () => {
    requestConnection(target)
    expect(state.credentialsDialog?.profile.id).toBe(target.id)
    await connect(target, { password: 'target-only' }, undefined, undefined, undefined, { password: 'gateway-only' })
    expect(api.connect).toHaveBeenCalledWith(state.sessions[0].id, target.id, { password: 'target-only' }, { password: 'gateway-only' }, connectionIdentity(target))
  })
  it('reuses an authenticated jump transport but asks for credentials after gateway identity changes', async () => {
    await connect(target, {})
    state.sessions[0].phase = 'connected'; state.credentialsDialog = null
    newTerminal()
    await vi.waitFor(() => expect(api.newTerminal).toHaveBeenCalledTimes(1))
    expect(state.credentialsDialog).toBeNull()
    requestConnection({ ...target, jumpIdentity: 'new-gateway-identity' })
    expect((state.credentialsDialog as { profile: Profile } | null)?.profile.jumpIdentity).toBe('new-gateway-identity')
    expect(api.newTerminal).toHaveBeenCalledTimes(1)
  })
  it('preserves the target fingerprint prompt arriving before gateway trust completes', async () => {
    await connect(target, {})
    const sessionId = state.sessions[0].id
    const first = { type: 'hostkey' as const, sessionId, host: '[gateway.test]:22', fingerprint: 'SHA256:first', keyType: 'ssh-rsa' }
    const second = { ...first, host: '[internal.test]:22', fingerprint: 'SHA256:second' }
    event(first)
    let finish!: () => void
    api.trust.mockReturnValue(new Promise(resolve => { finish = () => resolve({ ok: true }) }))
    const pending = answerHostKey(first, true)
    event(second); finish(); await pending
    expect(state.trust).toEqual([second])
    expect(api.trust).toHaveBeenCalledWith(sessionId, true)
  })
})
