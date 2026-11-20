import { randomUUID } from 'node:crypto'
import { lstatSync } from 'node:fs'
import { lstat, mkdir, mkdtemp, rm } from 'node:fs/promises'
import { dirname, isAbsolute, join, posix, resolve } from 'node:path'
import type { SFTPWrapper, Stats } from 'ssh2'
import { sftpCall } from './files'
import { remotePath } from './validation'

export interface PreparedDragDownload { token: string; name: string; size: number; expiresAt: number }
interface CachedFile extends PreparedDragDownload {
  sessionId: string; identity: string; remotePath: string; localPath: string
  directory: string; remoteMtime: number; localMtime: number; expires: number
}
const CACHE_LIFETIME = 10 * 60 * 1000

// Native drag needs an existing local file. Keep every downloaded snapshot in its own
// directory, and give the renderer an opaque token rather than a local filesystem path.
export class DragDownloads {
  private runDirectory?: string
  private initialization?: Promise<void>
  private disposed = false
  private files = new Map<string, CachedFile>()
  private pending = new Map<string, Promise<PreparedDragDownload>>()

  constructor(
    private cacheRoot: string,
    private getSessionIdentity: (sessionId: string) => string,
    private getSftp: (sessionId: string) => SFTPWrapper,
    private download: (sessionId: string, localPath: string, remotePath: string) => Promise<void>,
    private now: () => number = Date.now
  ) {
    if (!isAbsolute(cacheRoot)) throw new Error('拖出缓存目录必须是绝对路径')
    this.cacheRoot = resolve(cacheRoot)
  }

  init(): Promise<void> {
    return this.initialization ||= (async () => {
      await mkdir(this.cacheRoot, { recursive: true })
      this.runDirectory = await mkdtemp(join(this.cacheRoot, 'drag-'))
    })()
  }

  prepare(sessionId: string, path: string): Promise<PreparedDragDownload> {
    if (this.disposed) return Promise.reject(new Error('拖出下载已停止'))
    path = remotePath(path)
    const identity = this.getSessionIdentity(sessionId)
    const key = JSON.stringify([sessionId, identity, path])
    const previous = this.pending.get(key)
    if (previous) return previous
    const work = this.prepareFile(sessionId, identity, path).finally(() => {
      if (this.pending.get(key) === work) this.pending.delete(key)
    })
    this.pending.set(key, work)
    return work
  }

  private async prepareFile(sessionId: string, identity: string, path: string): Promise<PreparedDragDownload> {
    const name = dragFileName(path)
    const sftp = this.getSftp(sessionId)
    const source = await regularFile(sftp, path)
    await this.init()
    this.assertIdentity(sessionId, identity)
    // Calling Prepare again checks remote metadata rather than silently reusing an old snapshot.
    for (const entry of this.files.values()) {
      if (entry.sessionId !== sessionId || entry.identity !== identity || entry.remotePath !== path) continue
      if (entry.expires > this.now() && entry.size === source.size && entry.remoteMtime === source.mtime && this.localFileMatches(entry)) return publicFile(entry)
      this.files.delete(entry.token)
      await this.removeItem(entry.directory)
    }
    const directory = await mkdtemp(join(this.runDirectory!, 'file-'))
    const localPath = join(directory, name)
    try {
      this.assertIdentity(sessionId, identity)
      await this.download(sessionId, localPath, path)
      this.assertIdentity(sessionId, identity)
      const current = await regularFile(sftp, path)
      if (current.size !== source.size || current.mtime !== source.mtime) throw new Error('源文件在下载期间发生变化，请重新准备拖出')
      const local = await lstat(localPath)
      if (!local.isFile() || local.isSymbolicLink() || local.size !== source.size) throw new Error('拖出缓存文件不完整，请重新准备')
      this.assertIdentity(sessionId, identity)
      const entry: CachedFile = {
        token: randomUUID(), name, size: source.size, expiresAt: this.now() + CACHE_LIFETIME, sessionId, identity, remotePath: path,
        localPath, directory, remoteMtime: source.mtime, localMtime: local.mtimeMs, expires: this.now() + CACHE_LIFETIME
      }
      this.files.set(entry.token, entry)
      return publicFile(entry)
    } catch (error) {
      await this.removeItem(directory).catch(() => {})
      throw error
    }
  }

  // Invoke synchronously from the actual dragstart IPC handler, then call startDrag.
  // The token cannot be used for another session, a reconnected identity, or an arbitrary path.
  fileForDrag(sessionId: string, token: string): string {
    const entry = this.files.get(token)
    if (!entry || entry.sessionId !== sessionId) throw new Error('请先准备此文件的拖出下载')
    this.assertIdentity(sessionId, entry.identity)
    if (entry.expires <= this.now()) throw new Error('拖出文件已过期，请重新准备下载')
    if (!this.localFileMatches(entry)) throw new Error('拖出缓存已失效，请重新准备下载')
    return entry.localPath
  }

  private localFileMatches(entry: CachedFile): boolean {
    try {
      const directory = lstatSync(entry.directory)
      const runDirectory = this.runDirectory && lstatSync(this.runDirectory)
      if (!directory.isDirectory() || directory.isSymbolicLink() || !runDirectory || !runDirectory.isDirectory() || runDirectory.isSymbolicLink()) return false
      const local = lstatSync(entry.localPath)
      return local.isFile() && !local.isSymbolicLink() && local.size === entry.size && local.mtimeMs === entry.localMtime
    } catch { return false }
  }

  private assertIdentity(sessionId: string, identity: string): void {
    if (this.disposed) throw new Error('拖出下载已停止')
    if (this.getSessionIdentity(sessionId) !== identity) throw new Error('连接已变化，请重新准备拖出下载')
  }

  private async removeItem(directory: string): Promise<void> {
    const absolute = resolve(directory)
    if (!this.runDirectory || dirname(absolute) !== this.runDirectory || !absolute.startsWith(this.runDirectory + (process.platform === 'win32' ? '\\' : '/'))) throw new Error('无效的拖出缓存清理路径')
    await rm(absolute, { recursive: true, force: true })
  }

  async dispose(): Promise<void> {
    this.disposed = true
    // Let in-flight transfers settle before removing their destination directories.
    // The caller should cancel its transfer queue first during application shutdown.
    await Promise.allSettled([...this.pending.values()])
    await this.initialization?.catch(() => {})
    this.files.clear()
    if (!this.runDirectory) return
    const directory = resolve(this.runDirectory)
    if (dirname(directory) !== this.cacheRoot || !directory.startsWith(this.cacheRoot + (process.platform === 'win32' ? '\\' : '/'))) throw new Error('无效的拖出缓存清理路径')
    await rm(directory, { recursive: true, force: true })
    this.runDirectory = undefined
  }
}

async function regularFile(sftp: SFTPWrapper, path: string): Promise<Stats> {
  const attrs = await sftpCall<Stats>(cb => sftp.lstat(path, cb))
  if (!attrs.isFile() || attrs.isSymbolicLink()) throw new Error('拖出下载仅支持普通文件，文件夹和符号链接请使用其他操作')
  if (!Number.isSafeInteger(attrs.size) || attrs.size < 0) throw new Error('远程文件大小无效')
  return attrs
}
function publicFile(entry: CachedFile): PreparedDragDownload { return { token: entry.token, name: entry.name, size: entry.size, expiresAt: entry.expires } }

export function dragFileName(path: string): string {
  const name = posix.basename(remotePath(path))
  // Preserve the original name. Names that Windows cannot represent must be downloaded
  // through Save As, where the user can choose a different name explicitly.
  if (!name || name === '.' || name === '..' || name.length > 255 || /[<>:"/\\|?*\x00-\x1f]/.test(name) || /[. ]$/.test(name) || /^(con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\.|$)/i.test(name)) throw new Error('此文件名不适合拖出到 Windows，请使用“下载文件”另选名称')
  return name
}
