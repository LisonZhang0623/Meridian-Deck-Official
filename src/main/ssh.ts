import { Client, type ClientChannel, type SFTPWrapper, utils } from 'ssh2'
import { readFile } from 'node:fs/promises'
import { createHash, randomUUID } from 'node:crypto'
import type { AppEvent, Credentials, GpuSnapshot, Phase, Profile } from '../shared/types'
import { queryNvidiaSmi, type GpuQuery } from './gpu'
import { remotePath, shellQuote } from './validation'
import { connectionIdentity } from '../shared/connection'

export interface TrustStore {
  fingerprint(host: string): string | undefined
  trust(host: string, fingerprint: string): Promise<void>
  recordConnected?(profileId: string, identity: string): Promise<void>
}
interface Session {
  id: string; generation: string; profile: Profile; client: Client; stream?: ClientChannel; sftp?: SFTPWrapper
  stopped: boolean; phase: Phase; timer?: ReturnType<typeof setTimeout>
  verification?: { accept: (accept: boolean) => void; host: string; fingerprint: string }
  gpuQuery?: GpuQuery
  initialPath?: string
  jumpClient?: Client
  jumpSocket?: ClientChannel
}
export function sshError(error: Error & { level?: string; code?: string }): string {
  if (error.level === 'client-authentication') return '身份验证失败，请检查用户名、密码或私钥。'
  if (error.code === 'ECONNREFUSED') return '连接被拒绝，请检查端口及远程 SSH 服务。'
  if (error.code === 'ENOTFOUND') return '无法解析服务器地址，请检查域名和网络。'
  if (error.code === 'ETIMEDOUT') return '连接超时，请检查服务器地址、网络和防火墙。'
  return error.message || 'SSH 连接失败'
}
export class SshService {
  private sessions = new Map<string, Session>()
  constructor(private emit: (event: AppEvent) => void, private store: TrustStore) {}
  connect(id: string, profile: Profile, credentials: Credentials, jump?: { profile: Profile; credentials: Credentials }): void {
    if (this.sessions.has(id)) throw new Error('会话已存在')
    if (profile.jumpProfileId && (!jump || jump.profile.id !== profile.jumpProfileId || jump.profile.id === profile.id || jump.profile.jumpProfileId)) throw new Error('跳板机配置无效，当前仅支持已保存的单跳连接')
    const session: Session = { id, generation: randomUUID(), profile: { ...profile }, client: new Client(), stopped: false, phase: 'connecting' }
    this.sessions.set(id, session)
    this.state(session, 'connecting', '正在连接服务器')
    void this.prepare(session, credentials, jump).catch(error => this.fail(session, error))
  }
  newTerminal(id: string, sourceId: string, profile: Profile, initialPath?: string): void {
    if (this.sessions.has(id)) throw new Error('会话已存在')
    const source = this.sessions.get(sourceId)
    if (!source || source.stopped || source.phase !== 'connected') throw new Error('原连接已断开，请重新连接服务器')
    if (connectionIdentity(source.profile) !== connectionIdentity(profile)) throw new Error('登录配置已变化，请重新连接服务器')
    if (initialPath !== undefined) initialPath = remotePath(initialPath)
    const session: Session = { id, generation: randomUUID(), profile: { ...profile }, client: source.client, jumpClient: source.jumpClient, jumpSocket: source.jumpSocket, stopped: false, phase: 'connecting', initialPath }
    this.sessions.set(id, session)
    this.armTimeout(session)
    void this.ready(session, true).catch(error => this.fail(session, error))
  }
  private stopConnection(client: Client, phase: Phase, message: string): void {
    for (const session of [...this.sessions.values()]) if (session.client === client) this.stop(session, phase, message)
  }
  private state(s: Session, phase: Phase, message: string, extra: { home?: string; sftpError?: string } = {}): void {
    s.phase = phase
    this.emit({ type: 'state', sessionId: s.id, phase, message, ...extra })
  }
  private armTimeout(s: Session): void {
    clearTimeout(s.timer)
    s.timer = setTimeout(() => this.fail(s, new Error('连接超时，请检查网络、端口或身份验证信息。')), s.profile.timeout * 1000)
  }
  private async prepare(s: Session, credentials: Credentials, jump?: { profile: Profile; credentials: Credentials }): Promise<void> {
    if (jump && s.profile.jumpProfileId) {
      const gateway = s.jumpClient = new Client()
      await this.openTransport(s, gateway, jump.profile, jump.credentials, '跳板机', () => {
        this.state(s, 'connecting', '跳板机已连接，正在访问目标服务器')
        this.armTimeout(s)
        gateway.forwardOut('127.0.0.1', 0, s.profile.host, s.profile.port, (error, stream) => {
          if (error) { this.fail(s, new Error('跳板机无法访问目标：' + error.message)); return }
          if (s.stopped) { stream.destroy(); return }
          s.jumpSocket = stream
          stream.on('error', (error: Error) => this.stopConnection(s.client, 'failed', '跳板机通道错误：' + error.message))
          void this.openTransport(s, s.client, s.profile, credentials, '目标服务器', () => { void this.ready(s).catch(error => this.fail(s, error)) }, stream).catch(error => this.fail(s, error))
        })
      })
    } else await this.openTransport(s, s.client, s.profile, credentials, '服务器', () => { void this.ready(s).catch(error => this.fail(s, error)) })
  }
  private async openTransport(s: Session, client: Client, profile: Profile, credentials: Credentials, label: string, ready: () => void, sock?: ClientChannel): Promise<void> {
    const privateKey = profile.authType === 'key' ? await readFile(profile.keyPath) : undefined
    if (s.stopped) return
    // A transport outlives the session that originally created it. Rekey checks
    // must use the already verified transport key, without reopening login UI
    // or moving a working terminal back into an authentication phase.
    let verifiedFingerprint: string | undefined, verificationFailure = ''
    this.armTimeout(s)
    client.on('error', error => this.stopConnection(s.client, 'failed', verificationFailure || label + '：' + sshError(error)))
    client.on('close', () => this.stopConnection(s.client, 'disconnected', label + '连接已断开，终端输出已保留'))
    client.once('ready', ready)
    client.connect({
      host: profile.host, port: profile.port, username: profile.username, sock,
      password: profile.authType === 'password' ? credentials.password : undefined,
      privateKey, passphrase: credentials.passphrase,
      readyTimeout: 0, keepaliveInterval: profile.keepalive ? 15000 : 0, keepaliveCountMax: 3,
      hostVerifier: (key: Buffer, accept: (valid: boolean) => void) => {
        const live = [...this.sessions.values()].some(peer => !peer.stopped && (peer.client === client || peer.jumpClient === client))
        if (!live) { accept(false); return }
        const fingerprint = 'SHA256:' + createHash('sha256').update(key).digest('base64').replace(/=+$/, '')
        if (verifiedFingerprint) {
          if (fingerprint === verifiedFingerprint) { accept(true); return }
          verificationFailure = label + '在密钥更新时改变了主机指纹，已断开连接。请重新连接并核对服务器身份。'
          accept(false)
          this.stopConnection(s.client, 'failed', verificationFailure)
          return
        }
        if (s.stopped) { accept(false); return }
        const host = '[' + profile.host.toLowerCase() + ']:' + profile.port
        const previous = this.store.fingerprint(host)
        if (previous === fingerprint) {
          verifiedFingerprint = fingerprint
          this.state(s, 'authenticating', label + '身份已核验，正在登录')
          accept(true); return
        }
        clearTimeout(s.timer)
        s.verification = { host, fingerprint, accept: valid => { if (valid) verifiedFingerprint = fingerprint; accept(valid) } }
        const parsed = utils.parseKey(key)
        const keyType = parsed instanceof Error ? 'SSH' : Array.isArray(parsed) ? parsed[0].type : parsed.type
        this.state(s, 'verifying', previous ? label + '指纹发生变化，请核对' : '首次连接' + label + '，请确认指纹')
        this.emit({ type: 'hostkey', sessionId: s.id, host, fingerprint, previous, keyType })
      }
    })
  }
  async trust(id: string, accept: boolean): Promise<void> {
    const s = this.sessions.get(id)
    const pending = s?.verification
    if (!s || !pending) return
    s.verification = undefined
    if (!accept) { pending.accept(false); this.stop(s, 'disconnected', '已取消连接'); return }
    try {
      await this.store.trust(pending.host, pending.fingerprint)
      if (s.stopped) { pending.accept(false); return }
      this.state(s, 'authenticating', '正在验证登录信息')
      this.armTimeout(s)
      pending.accept(true)
    } catch (error) { pending.accept(false); this.fail(s, error as Error); throw error }
  }
  private async ready(s: Session, reused = false): Promise<void> {
    if (s.stopped) return
    this.state(s, 'authenticating', reused ? '正在打开新的终端' : '登录成功，正在打开终端')
    let home = s.initialPath ?? (s.profile.initialPath || '.')
    // A directory shortcut must validate its destination before opening a shell.
    // Keep the override on this session so it cannot change the saved profile or peers.
    if (s.initialPath !== undefined) {
      try {
        const sftp = await new Promise<SFTPWrapper>((resolve, reject) => s.client.sftp((error, sftp) => error ? reject(error) : resolve(sftp)))
        if (s.stopped) { sftp.end(); return }
        s.sftp = sftp
        sftp.on('error', () => { s.sftp = undefined })
        home = remotePath(await new Promise<string>((resolve, reject) => sftp.realpath(home, (error, value) => error ? reject(error) : resolve(value))))
        const isDirectory = await new Promise<boolean>((resolve, reject) => sftp.stat(home, (error, attrs) => error ? reject(error) : resolve(attrs.isDirectory())))
        if (!isDirectory) throw new Error('目标不是目录')
      } catch (error) { throw new Error('无法在此目录打开终端：' + (error as Error).message) }
      if (s.stopped) return
    }
    const stream = await new Promise<ClientChannel>((resolve, reject) => s.client.shell({ term: 'xterm-256color', cols: 100, rows: 30 }, (error, stream) => error ? reject(error) : resolve(stream)))
    if (s.stopped) { stream.destroy(); return }
    s.stream = stream
    stream.on('data', (data: Buffer) => this.emit({ type: 'terminal', sessionId: s.id, data: data.toString('base64') }))
    stream.stderr.on('data', (data: Buffer) => this.emit({ type: 'terminal', sessionId: s.id, data: data.toString('base64') }))
    stream.on('error', (error: Error) => this.fail(s, error))
    stream.on('close', () => this.stop(s, 'disconnected', '终端会话已结束'))
    let sftpError: string | undefined
    if (s.initialPath === undefined) try {
      const sftp = await new Promise<SFTPWrapper>((resolve, reject) => s.client.sftp((error, sftp) => error ? reject(error) : resolve(sftp)))
      if (s.stopped) { sftp.end(); return }
      s.sftp = sftp
      sftp.on('error', () => { s.sftp = undefined })
      home = await new Promise<string>((resolve, reject) => sftp.realpath(home, (error, value) => error ? reject(error) : resolve(value)))
    } catch (error) { sftpError = '文件浏览暂不可用：' + (error as Error).message }
    if (s.stopped) return
    clearTimeout(s.timer)
    if (s.initialPath !== undefined) stream.write(`cd -- ${shellQuote(home)} || exit\r`)
    else if (s.profile.initialPath) stream.write(`cd -- ${shellQuote(s.profile.initialPath)}\r`)
    this.state(s, 'connected', '已连接', { home, sftpError })
    // History persistence must never turn a working SSH session into a failure.
    void Promise.resolve().then(() => this.store.recordConnected?.(s.profile.id, connectionIdentity(s.profile))).catch(error => console.error('记录最近连接失败：', error))
  }
  input(id: string, value: string): void { const s = this.sessions.get(id); if (s && !s.stopped && s.stream && value.length <= 1024 * 1024) s.stream.write(value) }
  resize(id: string, cols: number, rows: number): void {
    if (!Number.isInteger(cols) || !Number.isInteger(rows) || cols < 2 || rows < 2 || cols > 1000 || rows > 1000) return
    this.sessions.get(id)?.stream?.setWindow(rows, cols, 0, 0)
  }
  sftp(id: string): SFTPWrapper { const s = this.sessions.get(id); if (!s || s.stopped || !s.sftp) throw new Error('文件连接不可用，请先连接服务器'); return s.sftp }
  clientFor(id: string): Client {
    const s = this.sessions.get(id)
    if (!s || s.stopped || s.phase !== 'connected') throw new Error('请先连接服务器')
    return s.client
  }
  connected(id: string): boolean { return this.sessions.get(id)?.phase === 'connected' }
  sessionIdentity(id: string): string {
    const s = this.sessions.get(id)
    if (!s || s.stopped || s.phase !== 'connected') throw new Error('连接已断开，请重新连接服务器')
    return JSON.stringify([connectionIdentity(s.profile), s.generation])
  }
  async gpuStatus(id: string): Promise<GpuSnapshot> {
    const s = this.sessions.get(id)
    if (!s || s.stopped || s.phase !== 'connected') throw new Error('请先连接服务器，再查看显卡状态。')
    const query = s.gpuQuery ||= queryNvidiaSmi(s.client)
    try { return await query.promise }
    finally { if (s.gpuQuery === query) s.gpuQuery = undefined }
  }
  cancelGpuStatus(id: string): void {
    const s = this.sessions.get(id)
    const query = s?.gpuQuery
    if (s) s.gpuQuery = undefined
    query?.cancel()
  }
  disconnect(id: string): void { const s = this.sessions.get(id); if (s) this.stop(s, 'disconnected', '已主动断开连接') }
  dispose(): void { for (const s of this.sessions.values()) this.stop(s, 'disconnected', '应用已关闭') }
  private fail(s: Session, error: Error): void { this.stop(s, 'failed', sshError(error)) }
  private stop(s: Session, phase: Phase, message: string): void {
    if (s.stopped) return
    s.stopped = true
    this.cancelGpuStatus(s.id)
    clearTimeout(s.timer)
    const pending = s.verification; s.verification = undefined
    pending?.accept(false)
    this.sessions.delete(s.id)
    s.stream?.destroy(); s.sftp?.end()
    if (![...this.sessions.values()].some(other => other.client === s.client)) { s.client.destroy(); s.jumpSocket?.destroy(); s.jumpClient?.destroy() }
    this.state(s, phase, message)
  }
}
