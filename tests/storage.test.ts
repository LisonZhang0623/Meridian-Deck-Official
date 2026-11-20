import { describe, it, expect } from 'vitest'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { createCipheriv, createDecipheriv, randomBytes } from 'node:crypto'
import { Storage } from '../src/main/storage'
import { profile, entryName, shellQuote, settings } from '../src/main/validation'
import { DEFAULT_SETTINGS } from '../src/shared/settings'
import type { Settings } from '../src/shared/types'
const sample = { id: 'test-profile', name: 'Test', group: '', host: '127.0.0.1', port: 22, username: 'test', authType: 'password' as const, keyPath: '', initialPath: '', timeout: 20, keepalive: true, rememberCredentials: true }
function cipher() {
  const key = randomBytes(32)
  return {
    async encrypt(text: string) { const iv = randomBytes(12), cipher = createCipheriv('aes-256-gcm', key, iv); const data = Buffer.concat([cipher.update(text), cipher.final()]); return Buffer.concat([iv, cipher.getAuthTag(), data]).toString('base64') },
    async decrypt(text: string) { const data = Buffer.from(text, 'base64'), decipher = createDecipheriv('aes-256-gcm', key, data.subarray(0,12)); decipher.setAuthTag(data.subarray(12,28)); return Buffer.concat([decipher.update(data.subarray(28)), decipher.final()]).toString() }
  }
}
describe('saved connections', () => {
  it('keeps credentials encrypted and clears them on identity changes or opt-out', async () => {
    const path = await mkdtemp(join(tmpdir(), 'sshtool-storage-')), crypto = cipher(), store = new Storage(path, crypto)
    await store.init(); await store.saveProfile(sample, { password: 'private-test-password' })
    expect(await readFile(join(path, 'settings.json'), 'utf8')).not.toContain('private-test-password')
    expect(JSON.stringify(store.list())).not.toContain('private-test-password')
    const reloaded = new Storage(path, crypto); await reloaded.init()
    expect((await reloaded.credentials(sample.id)).password).toBe('private-test-password')
    await reloaded.saveProfile({ ...sample, host: 'another.example' }, {})
    expect(await reloaded.credentials(sample.id)).toEqual({})
    await reloaded.saveProfile(sample, { password: 'another-secret' })
    await reloaded.saveProfile({ ...sample, rememberCredentials: false }, {})
    expect(await reloaded.credentials(sample.id)).toEqual({})
  })
  it('preserves an unreadable configuration instead of replacing it', async () => {
    const path = await mkdtemp(join(tmpdir(), 'sshtool-corrupt-'))
    await writeFile(join(path, 'settings.json'), 'broken json')
    await expect(new Storage(path, cipher()).init()).rejects.toThrow('原文件未被覆盖')
    expect(await readFile(join(path, 'settings.json'), 'utf8')).toBe('broken json')
  })
  it('migrates and persists terminal size, editor size, and workbench zoom', async () => {
    const path = await mkdtemp(join(tmpdir(), 'sshtool-settings-'))
    const legacy = { sidebarSide: 'right', sidebarCollapsed: true, theme: 'dark' }
    await writeFile(join(path, 'settings.json'), JSON.stringify({ version: 1, profiles: [], secrets: {}, hosts: {}, settings: legacy }))
    const store = new Storage(path, cipher()); await store.init()
    expect(store.getSettings()).toEqual({ ...DEFAULT_SETTINGS, ...legacy })
    await store.saveSettings({ ...store.getSettings(), terminalFontSize: 20, editorFontSize: 17, uiZoom: 125, startupAnimation: 'orbit', windowCloseAction: 'tray', checkUpdatesOnStartup: true })
    const restored = new Storage(path, cipher()); await restored.init()
    expect(restored.getSettings().terminalFontSize).toBe(20)
    expect(restored.getSettings().editorFontSize).toBe(17)
    expect(restored.getSettings().uiZoom).toBe(125)
    expect(restored.getSettings().windowCloseAction).toBe('tray')
    expect(restored.getSettings().startupAnimation).toBe('orbit')
    expect(settings({ ...restored.getSettings(), startupAnimation: 'random' }).startupAnimation).toBe('random')
    expect(settings({ ...restored.getSettings(), startupAnimation: 'off' }).startupAnimation).toBe('off')
    expect(restored.getSettings().checkUpdatesOnStartup).toBe(true)
    for (const invalid of [9, 33, 14.5, NaN, '18', null]) expect(() => settings({ ...restored.getSettings(), terminalFontSize: invalid } as Settings)).toThrow('终端字号')
    for (const invalid of [9, 33, 14.5, '18']) expect(() => settings({ ...restored.getSettings(), editorFontSize: invalid } as Settings)).toThrow('编辑器字号')
    for (const invalid of [79, 151, 109.5, '120']) expect(() => settings({ ...restored.getSettings(), uiZoom: invalid } as Settings)).toThrow('工作台缩放')
  })
  it('migrates the combined sidebar to the right and restores later panel and position choices', async () => {
    const path = await mkdtemp(join(tmpdir(), 'sshtool-sidebar-'))
    const legacy = { sidebarSide: 'left', sidebarCollapsed: false, theme: 'dark', terminalFontSize: 18 }
    await writeFile(join(path, 'settings.json'), JSON.stringify({ version: 1, profiles: [sample], secrets: {}, hosts: {}, settings: legacy }))
    const store = new Storage(path, cipher()); await store.init()
    expect(store.getSettings()).toEqual({ ...DEFAULT_SETTINGS, ...legacy, sidebarSide: 'right' })
    await store.saveSettings({ ...store.getSettings(), sidebarSide: 'left', sidebarPanel: 'files', sidebarCollapsed: true, sidebarWidth: 352 })
    const restored = new Storage(path, cipher()); await restored.init()
    expect(restored.getSettings()).toEqual({ ...DEFAULT_SETTINGS, ...legacy, sidebarPanel: 'files', sidebarCollapsed: true, sidebarWidth: 352 })
    expect(restored.list()[0].id).toBe(sample.id)
    for (const invalid of ['unknown', '', null, 1]) expect(() => settings({ ...restored.getSettings(), sidebarPanel: invalid } as Settings)).toThrow('侧栏面板')
    for (const invalid of [235, 641, 239.5, '300']) expect(() => settings({ ...restored.getSettings(), sidebarWidth: invalid } as Settings)).toThrow('侧栏宽度')
  })
  it('rejects traversal names and invalid ports; quotes shell paths literally', () => {
    expect(() => entryName('../file')).toThrow()
    expect(() => entryName('..')).toThrow()
    expect(() => profile({ ...sample, port: 70000 })).toThrow()
    expect(shellQuote("/tmp/it's $(unsafe)")).toBe("'/tmp/it'\\''s $(unsafe)'")
  })
  it('persists GPU refresh seconds and rejects invalid or unbounded intervals', async () => {
    const path = await mkdtemp(join(tmpdir(), 'sshtool-gpu-settings-'))
    const store = new Storage(path, cipher()); await store.init()
    expect(store.getSettings().gpuRefreshSeconds).toBe(2)
    await store.saveSettings({ ...store.getSettings(), sidebarPanel: 'gpu', gpuRefreshSeconds: 5 })
    const restored = new Storage(path, cipher()); await restored.init()
    expect(restored.getSettings()).toMatchObject({ sidebarPanel: 'gpu', gpuRefreshSeconds: 5 })
    for (const invalid of [0, -1, 3601, 1.5, NaN, '2', null]) expect(() => settings({ ...store.getSettings(), gpuRefreshSeconds: invalid } as Settings)).toThrow('显卡刷新间隔')
  })
})
