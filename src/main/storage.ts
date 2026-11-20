import { mkdir, readFile, writeFile, rename } from 'node:fs/promises'
import { join } from 'node:path'
import type { Credentials, Profile, Settings } from '../shared/types'
import * as validate from './validation'
import { DEFAULT_SETTINGS } from '../shared/settings'
import { connectionIdentity } from '../shared/connection'

interface Data {
  version: 1; profiles: Profile[]; secrets: Record<string, string>; hosts: Record<string, string>; settings: Settings
}
interface Cipher { encrypt(value: string): Promise<string>; decrypt(value: string): Promise<string> }
export class Storage {
  private data: Data = { version: 1, profiles: [], secrets: {}, hosts: {}, settings: { ...DEFAULT_SETTINGS } }
  private queue: Promise<unknown> = Promise.resolve()
  constructor(private directory: string, private cipher: Cipher) {}
  async init(): Promise<void> {
    await mkdir(this.directory, { recursive: true })
    try {
      const raw = JSON.parse(await readFile(join(this.directory, 'settings.json'), 'utf8')) as Data
      if (raw.version !== 1 || !Array.isArray(raw.profiles)) throw new Error('不支持的配置文件版本')
      this.data = { version: 1, profiles: raw.profiles.map(validate.profile), settings: validate.settings(raw.settings), secrets: { ...raw.secrets }, hosts: { ...raw.hosts } }
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw new Error('读取配置失败，请检查 settings.json；原文件未被覆盖。', { cause: error })
    }
  }
  private persist(): Promise<void> {
    const snapshot = JSON.stringify(this.data, null, 2)
    const work = this.queue.catch(() => {}).then(async () => {
      const file = join(this.directory, 'settings.json')
      await writeFile(file + '.tmp', snapshot, { encoding: 'utf8', mode: 0o600 })
      await rename(file + '.tmp', file)
    })
    this.queue = work
    return work
  }
  private publicProfile(p: Profile): Profile {
    const jump = p.jumpProfileId && this.data.profiles.find(item => item.id === p.jumpProfileId)
    return { ...p, tags: [...(p.tags || [])], hasCredentials: !!this.data.secrets[p.id], ...(jump ? { jumpIdentity: connectionIdentity(jump) } : {}) }
  }
  list(): Profile[] { return this.data.profiles.map(p => this.publicProfile(p)) }
  get(id: string): Profile { const p = this.data.profiles.find(p => p.id === id); if (!p) throw new Error('连接配置不存在'); return this.publicProfile(p) }
  getSettings(): Settings { return { ...this.data.settings } }
  async saveSettings(value: Settings): Promise<void> { this.data.settings = validate.settings(value); await this.persist() }
  async saveProfile(input: Profile, secrets: Credentials): Promise<Profile> {
    const profile = validate.profile(input)
    if (profile.jumpProfileId) {
      const jump = this.data.profiles.find(p => p.id === profile.jumpProfileId)
      if (!jump) throw new Error('跳板机配置不存在，请先保存跳板机')
      if (jump.id === profile.id) throw new Error('不能将连接自身设为跳板机')
      if (jump.jumpProfileId) throw new Error('当前仅支持单跳跳板机')
      if (this.data.profiles.some(p => p.jumpProfileId === profile.id)) throw new Error('此连接正被用作跳板机，不能再配置另一跳')
    }
    const old = this.data.profiles.find(p => p.id === profile.id)
    const sameIdentity = old && old.host === profile.host && old.port === profile.port && old.username === profile.username && old.authType === profile.authType && old.keyPath === profile.keyPath
    const secret = profile.authType === 'password' ? secrets.password : secrets.passphrase
    let encrypted = sameIdentity ? this.data.secrets[profile.id] : undefined
    if (profile.rememberCredentials && secret !== undefined) encrypted = await this.cipher.encrypt(JSON.stringify(profile.authType === 'password' ? { password: secret } : { passphrase: secret }))
    if (!profile.rememberCredentials) encrypted = undefined
    // Preserve a successful connection recorded while encryption was in flight.
    // Connection history is written only by the SSH service, never by the form.
    const latest = this.data.profiles.find(p => p.id === profile.id)
    delete profile.lastConnectedAt
    if (latest?.lastConnectedAt !== undefined && connectionIdentity(this.publicProfile(latest)) === connectionIdentity(this.publicProfile(profile))) profile.lastConnectedAt = latest.lastConnectedAt
    if (encrypted) this.data.secrets[profile.id] = encrypted; else delete this.data.secrets[profile.id]
    const index = this.data.profiles.findIndex(p => p.id === profile.id)
    if (index < 0) this.data.profiles.push(profile); else this.data.profiles[index] = profile
    if (old && connectionIdentity(old) !== connectionIdentity(profile)) for (const dependent of this.data.profiles) if (dependent.jumpProfileId === profile.id) delete dependent.lastConnectedAt
    await this.persist()
    return this.publicProfile(profile)
  }
  async recordConnected(profileId: string, identity: string, now = Date.now()): Promise<void> {
    if (!Number.isSafeInteger(now) || now < 0) throw new Error('最近连接时间格式不正确')
    const profile = this.data.profiles.find(p => p.id === profileId)
    // Ignore a late success from a deleted profile or a previous login identity.
    if (!profile || connectionIdentity(this.publicProfile(profile)) !== identity) return
    profile.lastConnectedAt = Math.max(profile.lastConnectedAt || 0, now)
    await this.persist()
  }
  async credentials(id: string): Promise<Credentials> {
    const encrypted = this.data.secrets[id]
    if (!encrypted) return {}
    return validate.credentials(JSON.parse(await this.cipher.decrypt(encrypted)))
  }
  async delete(id: string): Promise<void> {
    if (this.data.profiles.some(p => p.jumpProfileId === id)) throw new Error('此连接正被其他服务器用作跳板机，请先移除引用')
    this.data.profiles = this.data.profiles.filter(p => p.id !== id); delete this.data.secrets[id]; await this.persist()
  }
  fingerprint(host: string): string | undefined { return Object.prototype.hasOwnProperty.call(this.data.hosts, host) ? this.data.hosts[host] : undefined }
  async trust(host: string, fingerprint: string): Promise<void> { this.data.hosts[host] = fingerprint; await this.persist() }
}
