import { afterEach, describe, expect, it, vi } from 'vitest'
import { EventEmitter } from 'node:events'
import type { Client, ClientChannel } from 'ssh2'
import { queryNvidiaSmi } from '../src/main/gpu'

function fixture(delayed = false) {
  const channel = Object.assign(new EventEmitter(), { stderr: new EventEmitter(), signal: vi.fn(), close: vi.fn(), destroy: vi.fn() })
  let accept: ((error: Error | undefined, stream: ClientChannel) => void) | undefined
  const client = { exec: vi.fn((_command, callback) => { accept = callback; if (!delayed) callback(undefined, channel) }) } as unknown as Client
  return { client, channel, accept: () => accept!(undefined, channel as unknown as ClientChannel) }
}
afterEach(() => vi.useRealTimers())
describe('bounded GPU queries', () => {
  it('decodes split UTF-8 safely and requires a successful exit status', async () => {
    const { client, channel } = fixture(), task = queryNvidiaSmi(client)
    const bytes = Buffer.from('0, GPU-0, 显卡 0, 0, 0, 1024, 40, 10.5, 150, N/A, P8\n')
    const split = bytes.indexOf(Buffer.from('显')) + 1
    channel.emit('data', bytes.subarray(0, split)); channel.emit('data', bytes.subarray(split))
    channel.emit('exit', 0); channel.emit('close')
    expect((await task.promise).devices[0].name).toBe('显卡 0')
    const bad = fixture(), request = queryNvidiaSmi(bad.client)
    bad.channel.emit('data', Buffer.from('partial output')); bad.channel.emit('close')
    await expect(request.promise).rejects.toThrow('未收到成功退出状态')
  })
  it('bounds a hung channel and closes channels accepted after a timeout', async () => {
    vi.useFakeTimers()
    for (const delayed of [false, true]) {
      const { client, channel, accept } = fixture(delayed), task = queryNvidiaSmi(client)
      const failed = expect(task.promise).rejects.toThrow('超时')
      await vi.advanceTimersByTimeAsync(10000); await failed
      if (delayed) accept()
      expect(channel.close).toHaveBeenCalled()
      expect(channel.destroy).toHaveBeenCalled()
    }
  })
  it('rejects oversized output and stream errors without leaving a query pending', async () => {
    const huge = fixture(), task = queryNvidiaSmi(huge.client)
    huge.channel.emit('data', Buffer.alloc(256 * 1024 + 1))
    await expect(task.promise).rejects.toThrow('超过 256 KiB')
    expect(huge.channel.destroy).toHaveBeenCalled()
    const broken = fixture(), request = queryNvidiaSmi(broken.client)
    broken.channel.emit('error', new Error('channel lost'))
    await expect(request.promise).rejects.toThrow('channel lost')
  })
  it('returns no devices for an empty successful query and rejects incomplete device records', async () => {
    const empty = fixture(), query = queryNvidiaSmi(empty.client)
    empty.channel.emit('exit', 0); empty.channel.emit('close')
    expect((await query.promise).devices).toEqual([])
    const partial = fixture(), request = queryNvidiaSmi(partial.client)
    partial.channel.emit('data', Buffer.from('0, GPU-0, GPU name, 5')); partial.channel.emit('exit', 0); partial.channel.emit('close')
    await expect(request.promise).rejects.toThrow('字段不完整')
  })
})
