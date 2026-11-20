import { createHash, randomUUID } from 'node:crypto'
import { posix } from 'node:path'
import type { FileEntryWithStats, SFTPWrapper, Stats } from 'ssh2'
import type { Preview, RemoteEntry } from '../shared/types'
import { entryName, remotePath } from './validation'

export const TEXT_LIMIT = 2 * 1024 * 1024
export const IMAGE_LIMIT = 20 * 1024 * 1024
export function sftpCall<T>(call: (callback: (error: Error | undefined | null, result: T) => void) => void): Promise<T> {
  return new Promise<T>((resolve, reject) => call((error, result) => error ? reject(error) : resolve(result)))
}
export function sftpVoid(call: (callback: (error?: Error | null) => void) => void): Promise<void> {
  return new Promise((resolve, reject) => call(error => error ? reject(error) : resolve()))
}
export function stat(sftp: SFTPWrapper, path: string): Promise<Stats> { return sftpCall(cb => sftp.stat(path, cb)) }
function lstat(sftp: SFTPWrapper, path: string): Promise<Stats> { return sftpCall(cb => sftp.lstat(path, cb)) }
function realpath(sftp: SFTPWrapper, path: string): Promise<string> { return sftpCall(cb => sftp.realpath(path, cb)) }
export function version(bytes: Buffer): string { return createHash('sha256').update(bytes).digest('hex') }
export async function readBounded(sftp: SFTPWrapper, path: string, limit: number): Promise<Buffer> {
  const attrs = await stat(sftp, path)
  if (!attrs.isFile()) throw new Error('只能预览普通文件')
  if (attrs.size > limit) throw new Error(`文件超过 ${limit / 1024 / 1024} MB 预览限制，请下载查看`)
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = []; let total = 0
    const stream = sftp.createReadStream(path)
    stream.on('data', (chunk: Buffer) => {
      total += chunk.length
      if (total > limit) { stream.destroy(new Error('文件大小超过预览限制')); return }
      chunks.push(chunk)
    })
    stream.on('end', () => resolve(Buffer.concat(chunks)))
    stream.on('error', reject)
  })
}
function imageMime(bytes: Buffer): string | undefined {
  if (bytes.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))) return 'image/png'
  if (bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255) return 'image/jpeg'
  if (/^GIF8[79]a/.test(bytes.toString('ascii', 0, 6))) return 'image/gif'
  if (bytes.toString('ascii', 0, 4) === 'RIFF' && bytes.toString('ascii', 8, 12) === 'WEBP') return 'image/webp'
  if (bytes.toString('ascii', 0, 2) === 'BM') return 'image/bmp'
  return undefined
}
export class RemoteFiles {
  constructor(private getSftp: (id: string) => SFTPWrapper) {}
  async list(id: string, path: string): Promise<{ path: string; entries: RemoteEntry[] }> {
    const sftp = this.getSftp(id)
    const resolved = remotePath(await realpath(sftp, remotePath(path)))
    const files = await sftpCall<FileEntryWithStats[]>(cb => sftp.readdir(resolved, cb))
    const entries: RemoteEntry[] = files.filter(f => f.filename && f.filename !== '.' && f.filename !== '..' && !/[/\x00]/.test(f.filename)).map(f => {
      const type = f.attrs.isSymbolicLink() ? 'link' : f.attrs.isDirectory() ? 'directory' : 'file'
      const links = type === 'file' && f.attrs.isFile() ? /^-[rwxstST-]{9}\s+(\d+)\s/.exec(f.longname) : null
      return {
        name: f.filename, path: posix.join(resolved, f.filename), size: f.attrs.size, modified: f.attrs.mtime * 1000, mode: f.attrs.mode, type,
        ...(links && Number(links[1]) > 1 ? { hardLink: true } : {}),
      }
    })
    const links = entries.filter(entry => entry.type === 'link')
    for (let start = 0; start < links.length; start += 8) {
      await Promise.all(links.slice(start, start + 8).map(entry => readLink(sftp, entry)))
    }
    entries.sort((a, b) => Number(b.type === 'directory') - Number(a.type === 'directory') || a.name.localeCompare(b.name))
    return { path: resolved, entries }
  }
  async preview(id: string, path: string): Promise<Preview> {
    const sftp = this.getSftp(id); path = remotePath(path)
    const attrs = await stat(sftp, path)
    const imageExtension = /\.(png|jpe?g|gif|webp|bmp)$/i.test(path)
    const bytes = await readBounded(sftp, path, imageExtension ? IMAGE_LIMIT : TEXT_LIMIT)
    const mime = imageMime(bytes)
    const base = { path, version: version(bytes), size: bytes.length, modified: attrs.mtime * 1000 }
    if (mime) return { ...base, kind: 'image', mime, content: `data:${mime};base64,${bytes.toString('base64')}` }
    if (bytes.length > TEXT_LIMIT) throw new Error('文本文件超过 2 MB 预览限制')
    if (bytes.includes(0)) throw new Error('该文件不是 UTF-8 文本，请下载后查看')
    let content: string
    try { content = new TextDecoder('utf-8', { fatal: true }).decode(bytes) } catch { throw new Error('暂不支持该编码，第一版文本预览支持 UTF-8') }
    return { ...base, kind: 'text', content }
  }
  async save(id: string, path: string, content: string, expectedVersion: string): Promise<Preview> {
    const sftp = this.getSftp(id); path = remotePath(path)
    const bytes = Buffer.from(content, 'utf8')
    if (bytes.length > TEXT_LIMIT) throw new Error('编辑后的文本超过 2 MB 限制')
    const requestedPath = path
    const link = (await lstat(sftp, path)).isSymbolicLink()
    if (link) path = remotePath(await realpath(sftp, path))
    const original = await readBounded(sftp, path, TEXT_LIMIT)
    if (version(original) !== expectedVersion) throw new Error('CONFLICT:远程文件已被修改，请重新加载并比较后再保存')
    const attrs = await stat(sftp, path)
    const temporary = posix.join(posix.dirname(path), `.${posix.basename(path)}.sshtool-${randomUUID()}.tmp`)
    try {
      await sftpVoid(cb => sftp.writeFile(temporary, bytes, { flag: 'wx', mode: attrs.mode & 0o777 }, cb))
      if (version(await readBounded(sftp, path, TEXT_LIMIT)) !== expectedVersion) throw new Error('CONFLICT:保存期间远程文件发生变化，请重新加载')
      if (link && await realpath(sftp, requestedPath).catch(() => '') !== path) throw new Error('CONFLICT:保存期间符号链接目标已变化，请重新加载')
      try { await sftpVoid(cb => sftp.ext_openssh_rename(temporary, path, cb)) }
      catch (error) {
        if ((error as { code?: number }).code !== 8 && !/not support/i.test((error as Error).message)) throw error
        await sftpVoid(cb => sftp.rename(temporary, path, cb))
      }
    } finally { await sftpVoid(cb => sftp.unlink(temporary, cb)).catch(() => {}) }
    return this.preview(id, requestedPath)
  }
  async create(id: string, parent: string, name: string, directory: boolean): Promise<void> {
    const sftp = this.getSftp(id); const path = posix.join(remotePath(parent), entryName(name))
    if (directory) await sftpVoid(cb => sftp.mkdir(path, cb))
    else await sftpVoid(cb => sftp.writeFile(path, Buffer.alloc(0), { flag: 'wx' }, cb))
  }
  async rename(id: string, path: string, name: string): Promise<void> {
    const sftp = this.getSftp(id); path = remotePath(path)
    const destination = posix.join(posix.dirname(path), entryName(name))
    if (destination === path) return
    try { await stat(sftp, destination); throw new Error('目标名称已存在') }
    catch (error) { if ((error as { code?: number }).code !== 2) throw error }
    await sftpVoid(cb => sftp.rename(path, destination, cb))
  }
  async delete(id: string, path: string, directory: boolean): Promise<void> {
    const sftp = this.getSftp(id); path = remotePath(path)
    if (path === '/') throw new Error('不能删除根目录')
    if (directory) await sftpVoid(cb => sftp.rmdir(path, cb))
    else await sftpVoid(cb => sftp.unlink(path, cb))
  }
}
async function readLink(sftp: SFTPWrapper, entry: RemoteEntry): Promise<void> {
  try { entry.linkTarget = await sftpCall<string>(cb => sftp.readlink(entry.path, cb)) } catch { /* The server might not expose the target path. */ }
  try {
    const target = await stat(sftp, entry.path)
    if (target.isDirectory()) entry.targetType = 'directory'
    else if (target.isFile()) entry.targetType = 'file'
  } catch (error) {
    if ((error as { code?: number }).code === 2) entry.targetType = 'missing'
  }
}
