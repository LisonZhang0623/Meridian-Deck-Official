import { afterEach, describe, expect, it, vi } from 'vitest'
import { mkdtemp, readFile, readdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { PassThrough, Readable } from 'node:stream'
import type { SFTPWrapper, Stats } from 'ssh2'
import type { Transfer } from '../src/shared/types'
import { Transfers } from '../src/main/transfers'

const tasks: { service: Transfers; settled: Promise<unknown>[] }[] = []
afterEach(async () => {
  for (const task of tasks.splice(0)) {
    task.service.dispose()
    await Promise.allSettled(task.settled)
  }
})

async function fixture() {
  const directory = await mkdtemp(join(tmpdir(), 'sshtool-transfer-wait-'))
  const bytes = Buffer.from('完整的下载内容\n\0binary\xff', 'utf8')
  const readers = new Map<string, Readable>()
  const events: Transfer[] = []
  const sftp = {
    stat(_path: string, callback: (error: null, attrs: Stats) => void) {
      callback(null, { size: bytes.length, mtime: 100, isFile: () => true } as Stats)
    },
    createReadStream(path: string) {
      const reader = path.includes('blocked') ? new PassThrough()
        : path.includes('broken') ? Readable.from((async function* () { yield bytes.subarray(0, 4); throw new Error('远程读取中断') })())
        : Readable.from([bytes])
      readers.set(path, reader)
      return reader
    }
  } as unknown as SFTPWrapper
  const service = new Transfers(() => sftp, event => events.push(event), async () => true)
  const settled: Promise<unknown>[] = []
  tasks.push({ service, settled })
  function download(name: string): Promise<void> {
    const promise = service.enqueueDownload('session', join(directory, name), '/remote/' + name)
    // Observe every deliberately blocked promise immediately; assertions still use
    // the original promise, while teardown waits for all cancellation cleanup.
    settled.push(promise.catch(() => {}))
    return promise
  }
  function latest(name: string): Transfer | undefined { return [...events].reverse().find(event => event.name === name) }
  return { directory, bytes, readers, events, service, download, latest }
}

describe('awaitable transfer downloads', () => {
  it('resolves only after the full file is committed and temporary parts are removed', async () => {
    const f = await fixture()
    await f.download('complete.bin')
    expect(await readFile(join(f.directory, 'complete.bin'))).toEqual(f.bytes)
    expect(await readdir(f.directory)).toEqual(['complete.bin'])
    expect(f.latest('complete.bin')).toMatchObject({ status: 'completed', transferred: f.bytes.length, size: f.bytes.length })
  })

  it('rejects a running cancellation after stopping the stream and cleaning its partial file', async () => {
    const f = await fixture()
    await writeFile(join(f.directory, 'blocked.bin'), 'original local file')
    const download = f.download('blocked.bin')
    const cancelled = expect(download).rejects.toThrow('取消')
    await vi.waitFor(() => expect(f.readers.has('/remote/blocked.bin')).toBe(true))
    f.service.cancel(f.latest('blocked.bin')!.id)
    await cancelled
    expect(f.latest('blocked.bin')?.status).toBe('cancelled')
    expect(f.readers.get('/remote/blocked.bin')?.destroyed).toBe(true)
    expect(await readFile(join(f.directory, 'blocked.bin'), 'utf8')).toBe('original local file')
    expect(await readdir(f.directory)).toEqual(['blocked.bin'])
  })

  it('rejects a queued cancellation without waiting for occupied slots or opening a remote stream', async () => {
    const f = await fixture()
    void f.download('blocked-first.bin')
    void f.download('blocked-second.bin')
    await vi.waitFor(() => expect(f.readers.size).toBe(2))
    const queued = f.download('queued.bin')
    const cancelled = expect(queued).rejects.toThrow('取消')
    expect(f.latest('queued.bin')?.status).toBe('queued')
    f.service.cancel(f.latest('queued.bin')!.id)
    await cancelled
    expect(f.latest('queued.bin')?.status).toBe('cancelled')
    expect(f.readers.has('/remote/queued.bin')).toBe(false)
    expect(f.latest('blocked-first.bin')?.status).toBe('running')
    expect(f.latest('blocked-second.bin')?.status).toBe('running')
    expect((await readdir(f.directory)).includes('queued.bin')).toBe(false)
  })

  it('rejects a remote read failure, preserves the destination, and leaves no partial download', async () => {
    const f = await fixture()
    await writeFile(join(f.directory, 'broken.bin'), 'keep this file')
    await expect(f.download('broken.bin')).rejects.toThrow('远程读取中断')
    expect(f.latest('broken.bin')).toMatchObject({ status: 'failed', error: '远程读取中断' })
    expect(await readFile(join(f.directory, 'broken.bin'), 'utf8')).toBe('keep this file')
    expect(await readdir(f.directory)).toEqual(['broken.bin'])
  })
})
