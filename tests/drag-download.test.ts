import { afterEach, describe, expect, it } from 'vitest'
import { mkdtemp, readFile, readdir, writeFile } from 'node:fs/promises'
import { join, basename } from 'node:path'
import { tmpdir } from 'node:os'
import type { SFTPWrapper, Stats } from 'ssh2'
import { DragDownloads, dragFileName } from '../src/main/drag-download'

const instances: DragDownloads[] = []
afterEach(async () => { await Promise.all(instances.splice(0).map(item => item.dispose())) })
async function fixture() {
  const root = await mkdtemp(join(tmpdir(), 'sshtool-drag-test-'))
  const remote = { data: Buffer.from('download 内容\n'), mtime: 10, type: 'file' }
  let identity = 'server-a'
  let connected = true
  let downloads = 0
  let clock = 1000
  let beforeWrite: (() => Promise<void> | void) | undefined
  const sftp = { lstat(_path: string, callback: (error: null, attrs: Stats) => void) {
    callback(null, { size: remote.data.length, mtime: remote.mtime, isFile: () => remote.type === 'file', isSymbolicLink: () => remote.type === 'link' } as Stats)
  } } as unknown as SFTPWrapper
  const service = new DragDownloads(root, id => {
    if (!connected || id !== 'session') throw new Error('连接已断开')
    return identity
  }, () => sftp, async (_id, path) => {
    downloads++
    await beforeWrite?.()
    await writeFile(path, remote.data)
  }, () => clock)
  instances.push(service)
  return { service, root, remote, get downloads() { return downloads }, setIdentity(value: string) { identity = value }, disconnect() { connected = false }, advance(ms: number) { clock += ms }, onWrite(callback: () => Promise<void> | void) { beforeWrite = callback } }
}

describe('prepared native drag downloads', () => {
  it('downloads once for overlapping preparation, preserves the original name, and only exposes an opaque token', async () => {
    const f = await fixture()
    const [first, second] = await Promise.all([f.service.prepare('session', '/home/test/结果 文件.txt'), f.service.prepare('session', '/home/test/结果 文件.txt')])
    expect(first).toEqual(second)
    expect(f.downloads).toBe(1)
    expect(Object.keys(first).sort()).toEqual(['expiresAt', 'name', 'size', 'token'])
    const path = f.service.fileForDrag('session', first.token)
    expect(basename(path)).toBe('结果 文件.txt')
    expect(await readFile(path)).toEqual(f.remote.data)
    expect(() => f.service.fileForDrag('another-session', first.token)).toThrow('请先准备')
    expect(() => f.service.fileForDrag('session', path)).toThrow('请先准备')
    expect(await f.service.prepare('session', '/home/test/结果 文件.txt')).toEqual(first)
    expect(f.downloads).toBe(1)
  })

  it('rejects symlinks/directories and Windows reserved or unrepresentable names before downloading', async () => {
    const f = await fixture()
    for (const type of ['link', 'directory']) {
      f.remote.type = type
      await expect(f.service.prepare('session', '/home/test/file')).rejects.toThrow('普通文件')
    }
    expect(f.downloads).toBe(0)
    for (const name of ['CON', 'nul.txt', 'COM1.log', 'LPT².dat', 'a:b.txt', 'bad\\name.txt', 'end.', 'end ', 'a?.txt']) expect(() => dragFileName('/home/' + name)).toThrow('Windows')
    expect(dragFileName('/home/中文文件.txt')).toBe('中文文件.txt')
    expect(dragFileName('/home/config.json')).toBe('config.json')
  })

  it('invalidates cached snapshots on identity changes, disconnects, expiration, and local file modification', async () => {
    const f = await fixture()
    const first = await f.service.prepare('session', '/home/test/file.txt')
    f.setIdentity('server-b')
    expect(() => f.service.fileForDrag('session', first.token)).toThrow('连接已变化')
    f.setIdentity('server-a')
    f.advance(10 * 60 * 1000)
    expect(() => f.service.fileForDrag('session', first.token)).toThrow('过期')
    const second = await f.service.prepare('session', '/home/test/file.txt')
    expect(second.token).not.toBe(first.token)
    const path = f.service.fileForDrag('session', second.token)
    await writeFile(path, 'tampered')
    expect(() => f.service.fileForDrag('session', second.token)).toThrow('缓存已失效')
    const third = await f.service.prepare('session', '/home/test/file.txt')
    f.disconnect()
    expect(() => f.service.fileForDrag('session', third.token)).toThrow('断开')
  })

  it('rechecks remote metadata and makes a new snapshot after the remote file changes', async () => {
    const f = await fixture()
    const first = await f.service.prepare('session', '/home/test/file.txt')
    f.remote.mtime++
    f.remote.data = Buffer.from('new data')
    const next = await f.service.prepare('session', '/home/test/file.txt')
    expect(next.token).not.toBe(first.token)
    expect(f.downloads).toBe(2)
    expect(await readFile(f.service.fileForDrag('session', next.token))).toEqual(f.remote.data)
    expect(() => f.service.fileForDrag('session', first.token)).toThrow('请先准备')
  })

  it('rejects a file changed during transfer and never publishes a partially downloaded file', async () => {
    const f = await fixture()
    f.onWrite(() => { f.remote.mtime++ })
    await expect(f.service.prepare('session', '/home/test/file.txt')).rejects.toThrow('发生变化')
    const [run] = await readdir(f.root)
    expect(await readdir(join(f.root, run))).toEqual([])
    f.onWrite(() => { throw new Error('transfer cancelled') })
    await expect(f.service.prepare('session', '/home/test/file.txt')).rejects.toThrow('transfer cancelled')
    expect(await readdir(join(f.root, run))).toEqual([])
  })

  it('waits for in-flight downloads during disposal, rejects late completion, and only cleans its own cache', async () => {
    const f = await fixture()
    await writeFile(join(f.root, 'keep-user-file.txt'), 'keep')
    let release!: () => void
    const blocked = new Promise<void>(resolve => { release = resolve })
    let started!: () => void
    const starting = new Promise<void>(resolve => { started = resolve })
    f.onWrite(async () => { started(); await blocked })
    const preparation = f.service.prepare('session', '/home/test/file.txt')
    // Attach the rejection handler before the deliberately late completion.
    const failed = expect(preparation).rejects.toThrow('已停止')
    await starting
    const disposal = f.service.dispose()
    release()
    await failed
    await disposal
    expect(await readdir(f.root)).toEqual(['keep-user-file.txt'])
    await expect(f.service.prepare('session', '/home/test/file.txt')).rejects.toThrow('已停止')
  })
})
