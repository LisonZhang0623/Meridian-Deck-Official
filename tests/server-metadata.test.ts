import { describe, expect, it } from 'vitest'
import { mkdtemp, writeFile, readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { Storage } from '../src/main/storage'
import { profile, tags } from '../src/main/validation'
import { connectionIdentity } from '../src/shared/connection'
import { DEFAULT_SETTINGS } from '../src/shared/settings'
import { filterProfiles, profileTags } from '../src/renderer/src/server-profiles'
import type { Profile } from '../src/shared/types'

const sample: Profile = { id: 'metadata-profile', name: '训练服务器', group: '科研', host: 'server.example', port: 22, username: 'researcher', authType: 'password', keyPath: '', initialPath: '', timeout: 20, keepalive: true, rememberCredentials: false }
const cipher = { async encrypt(value: string) { return value }, async decrypt(value: string) { return value } }
async function createStore(): Promise<Storage> {
  const path = await mkdtemp(join(tmpdir(), 'sshtool-metadata-'))
  const store = new Storage(path, cipher); await store.init()
  return store
}

describe('server labels and connection history', () => {
  it('normalizes labels and rejects oversized or invalid values', () => {
    expect(tags(undefined)).toEqual([])
    expect(tags([' 训练 ', '', '训练', 'A100', ' 生产 '])).toEqual(['训练', 'A100', '生产'])
    expect(tags(Array(15).fill('训练'))).toEqual(['训练'])
    expect(() => tags(Array.from({ length: 13 }, (_, i) => `tag${i}`))).toThrow('最多 12')
    for (const value of [null, '训练', [1], ['a\0b'], ['a\nb'], ['a'.repeat(25)]]) expect(() => tags(value)).toThrow()
  })

  it('loads legacy profiles, persists history and returns independent tag arrays', async () => {
    const path = await mkdtemp(join(tmpdir(), 'sshtool-metadata-legacy-'))
    await writeFile(join(path, 'settings.json'), JSON.stringify({ version: 1, profiles: [sample], settings: DEFAULT_SETTINGS, secrets: {}, hosts: {} }))
    const store = new Storage(path, cipher); await store.init()
    expect(store.list()[0].tags).toEqual([])
    expect(store.list()[0].lastConnectedAt).toBeUndefined()
    await store.saveProfile({ ...sample, tags: [' 训练 ', 'A100'] }, {})
    await store.recordConnected(sample.id, connectionIdentity(sample), 1700000000000)
    const returned = store.list()[0]; returned.tags!.push('外部变更')
    const direct = store.get(sample.id); direct.tags!.push('另一处变更')
    expect(store.get(sample.id).tags).toEqual(['训练', 'A100'])
    const reloaded = new Storage(path, cipher); await reloaded.init()
    expect(reloaded.get(sample.id)).toMatchObject({ tags: ['训练', 'A100'], lastConnectedAt: 1700000000000 })
    expect(JSON.parse(await readFile(join(path, 'settings.json'), 'utf8')).profiles[0].lastConnectedAt).toBe(1700000000000)
  })

  it('owns timestamps in the main process and ignores late successes for changed or deleted profiles', async () => {
    const store = await createStore()
    const saved = await store.saveProfile({ ...sample, lastConnectedAt: 12345 }, {})
    expect(saved.lastConnectedAt).toBeUndefined()
    const identity = connectionIdentity(sample)
    await store.recordConnected(sample.id, identity, 2000)
    await store.recordConnected(sample.id, identity, 1000)
    expect(store.get(sample.id).lastConnectedAt).toBe(2000)
    const edited = await store.saveProfile({ ...sample, name: '改名', tags: ['生产'], lastConnectedAt: 9000 }, {})
    expect(edited.lastConnectedAt).toBe(2000)
    await store.saveProfile({ ...edited, host: 'different.example' }, {})
    expect(store.get(sample.id).lastConnectedAt).toBeUndefined()
    await store.recordConnected(sample.id, identity, 10000)
    expect(store.get(sample.id).lastConnectedAt).toBeUndefined()
    await store.delete(sample.id)
    await store.recordConnected(sample.id, identity, 20000)
    expect(store.list()).toEqual([])
    expect(() => profile({ ...sample, lastConnectedAt: Infinity })).toThrow('最近连接时间')
  })

  it('filters labels and searches together, while showing only successfully connected profiles in recency order', () => {
    const profiles = [
      { ...sample, id: 'older', tags: ['训练', 'A100'], lastConnectedAt: 1000 },
      { ...sample, id: 'never', tags: ['训练'] },
      { ...sample, id: 'newer', name: '推理服务器', tags: ['推理', 'A100'], lastConnectedAt: 2000 }
    ]
    expect(filterProfiles(profiles, '', '', true).map(p => p.id)).toEqual(['newer', 'older'])
    expect(filterProfiles(profiles, 'a100', '训练', false).map(p => p.id)).toEqual(['older'])
    expect(filterProfiles(profiles, 'RESEARCHER', '推理', true).map(p => p.id)).toEqual(['newer'])
    expect(filterProfiles(profiles, '不存在', '', false)).toEqual([])
    expect(profileTags(profiles)).toEqual(expect.arrayContaining(['训练', '推理', 'A100']))
    expect(profileTags(profiles)).toHaveLength(3)
    expect(profiles.map(p => p.id)).toEqual(['older', 'never', 'newer'])
  })

  it('preserves a successful connection recorded while credentials are being encrypted', async () => {
    const path = await mkdtemp(join(tmpdir(), 'sshtool-metadata-race-'))
    let release!: () => void
    const store = new Storage(path, {
      async encrypt(value) { await new Promise<void>(resolve => { release = resolve }); return value },
      async decrypt(value) { return value }
    })
    await store.init(); await store.saveProfile(sample, {})
    const saving = store.saveProfile({ ...sample, rememberCredentials: true }, { password: 'test-only' })
    await store.recordConnected(sample.id, connectionIdentity(sample), 5000)
    release(); await saving
    expect(store.get(sample.id).lastConnectedAt).toBe(5000)
  })
})
