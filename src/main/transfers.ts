import { randomUUID } from 'node:crypto'
import { createReadStream, createWriteStream } from 'node:fs'
import { stat as localStat, rename as localRename, unlink } from 'node:fs/promises'
import { basename, dirname, join, posix } from 'node:path'
import { pipeline } from 'node:stream/promises'
import { Transform } from 'node:stream'
import type { SFTPWrapper } from 'ssh2'
import type { Transfer } from '../shared/types'
import { sftpVoid, stat } from './files'

interface Job { transfer: Transfer; localPath: string; abort: AbortController; committing: boolean; settle?: (error?: Error) => void }
export class Transfers {
  private jobs = new Map<string, Job>()
  private active = 0
  constructor(private getSftp: (id: string) => SFTPWrapper, private emit: (transfer: Transfer) => void, private overwrite: (path: string) => Promise<boolean>) {}
  enqueue(sessionId: string, direction: Transfer['direction'], localPath: string, remotePath: string, settle?: (error?: Error) => void): string {
    const transfer: Transfer = { id: randomUUID(), sessionId, direction, remotePath, ...(settle ? { purpose: 'drag' as const } : {}), name: direction === 'upload' ? basename(localPath) : posix.basename(remotePath), size: 0, transferred: 0, speed: 0, status: 'queued' }
    this.jobs.set(transfer.id, { transfer, localPath, abort: new AbortController(), committing: false, settle })
    this.emit({ ...transfer }); this.pump()
    return transfer.id
  }
  enqueueDownload(sessionId: string, localPath: string, remotePath: string): Promise<void> {
    return new Promise((resolve, reject) => this.enqueue(sessionId, 'download', localPath, remotePath, error => error ? reject(error) : resolve()))
  }
  private pump(): void {
    for (const job of this.jobs.values()) {
      if (this.active >= 2) return
      if (job.transfer.status !== 'queued') continue
      this.active++; job.transfer.status = 'running'; this.emit({ ...job.transfer })
      void this.run(job).finally(() => { this.active--; this.pump() })
    }
  }
  cancel(id: string): void {
    const job = this.jobs.get(id)
    if (!job || job.committing || !['running', 'queued'].includes(job.transfer.status)) return
    if (job.transfer.status === 'queued') { job.transfer.status = 'cancelled'; this.emit({ ...job.transfer }); job.settle?.(new Error('下载已取消')); job.settle = undefined }
    job.abort.abort()
  }
  retry(id: string): void {
    const job = this.jobs.get(id)
    if (!job || !['failed', 'cancelled'].includes(job.transfer.status)) throw new Error('该任务不能重试')
    if (job.transfer.purpose === 'drag') throw new Error('请从文件树重新准备拖出下载')
    this.getSftp(job.transfer.sessionId)
    job.abort = new AbortController(); job.committing = false
    Object.assign(job.transfer, { transferred: 0, speed: 0, status: 'queued', error: undefined })
    this.emit({ ...job.transfer }); this.pump()
  }
  dispose(): void { for (const id of this.jobs.keys()) this.cancel(id) }
  private async run(job: Job): Promise<void> {
    const t = job.transfer
    let cleanup: (() => Promise<void>) | undefined
    try {
      const sftp = this.getSftp(t.sessionId)
      const start = Date.now(); let lastEvent = 0
      const meter = new Transform({ transform: (chunk: Buffer, _encoding, callback) => {
        t.transferred += chunk.length
        t.speed = t.transferred / Math.max((Date.now() - start) / 1000, .1)
        if (Date.now() - lastEvent > 120) { lastEvent = Date.now(); this.emit({ ...t }) }
        callback(null, chunk)
      } })
      if (t.direction === 'upload') {
        const source = await localStat(job.localPath)
        if (!source.isFile()) throw new Error('第一版支持单个或多个文件上传，暂不支持整个文件夹')
        t.size = source.size
        let baseline: string | undefined
        let destinationMode = 0o600
        try {
          const existing = await stat(sftp, t.remotePath)
          if (!existing.isFile()) throw new Error('目标路径不是普通文件')
          baseline = `${existing.size}:${existing.mtime}`
          destinationMode = existing.mode & 0o777
          if (!await this.overwrite(t.remotePath)) { job.abort.abort(); throw new Error('已取消覆盖') }
        } catch (error) { if ((error as { code?: number }).code !== 2) throw error }
        job.abort.signal.throwIfAborted()
        const temp = posix.join(posix.dirname(t.remotePath), `.${t.name}.sshtool-${t.id}.part`)
        cleanup = () => sftpVoid(cb => sftp.unlink(temp, cb))
        await pipeline(createReadStream(job.localPath), meter, sftp.createWriteStream(temp, { flags: 'wx', mode: destinationMode }), { signal: job.abort.signal })
        job.abort.signal.throwIfAborted()
        let current: string | undefined
        try { const attrs = await stat(sftp, t.remotePath); current = `${attrs.size}:${attrs.mtime}` } catch (error) { if ((error as { code?: number }).code !== 2) throw error }
        if (current !== baseline) throw new Error('目标文件在传输期间发生变化，请确认后重试')
        job.committing = true
        if (baseline) {
          try { await sftpVoid(cb => sftp.ext_openssh_rename(temp, t.remotePath, cb)) }
          catch (error) {
            if ((error as { code?: number }).code !== 8 && !/not support/i.test((error as Error).message)) throw error
            await sftpVoid(cb => sftp.rename(temp, t.remotePath, cb))
          }
        } else await sftpVoid(cb => sftp.rename(temp, t.remotePath, cb))
      } else {
        const source = await stat(sftp, t.remotePath)
        if (!source.isFile()) throw new Error('请选择普通文件下载')
        t.size = source.size
        const temp = join(dirname(job.localPath), `.${basename(job.localPath)}.sshtool-${t.id}.part`)
        cleanup = () => unlink(temp)
        await pipeline(sftp.createReadStream(t.remotePath), meter, createWriteStream(temp, { flags: 'wx' }), { signal: job.abort.signal })
        job.abort.signal.throwIfAborted()
        const current = await stat(sftp, t.remotePath)
        if (current.size !== source.size || current.mtime !== source.mtime) throw new Error('源文件在下载期间发生变化，请重试')
        job.committing = true
        await localRename(temp, job.localPath)
      }
      t.status = 'completed'; t.transferred = t.size; t.speed = 0
    } catch (error) {
      t.status = job.abort.signal.aborted ? 'cancelled' : 'failed'
      t.error = t.status === 'failed' ? (error as Error).message : undefined
      t.speed = 0
    } finally {
      await cleanup?.().catch(() => {})
      this.emit({ ...t })
      job.settle?.(t.status === 'completed' ? undefined : new Error(t.error || '下载已取消'))
      job.settle = undefined
    }
  }
}
