import { onScopeDispose, ref, watch } from 'vue'
import type { Api, AppEvent } from '../../shared/types'
import type { LogEvent } from '../../shared/v2'

export interface LogSource { id: string; identity: string; connected: boolean }
const MAX_LINES = 2000, MAX_TEXT = 1024 * 1024
export function useLogMonitor(source: () => LogSource, api: Pick<Api, 'startLog' | 'stopLog' | 'onEvent'>) {
  const rows = ref<string[]>([]), running = ref(false), starting = ref(false), error = ref(''), status = ref('尚未开始追踪'), dropped = ref(false)
  let watchId = '', generation = 0, pendingId = '', disposed = false, queued: LogEvent[] = [], queuedChars = 0, queuedOverflow = false
  let tail = '', textSize = 0
  const encoder = new TextEncoder(), bytes = (text: string) => encoder.encode(text).byteLength
  const clear = () => { rows.value = []; tail = ''; textSize = 0; dropped.value = false }
  const append = (value: string) => {
    // Log text is rendered with Vue interpolation, never as HTML.
    const combined = tail + value.replace(/\r\n/g, '\n').replace(/\r/g, '\n')
    const lines = combined.split('\n'); tail = lines.pop() || ''
    if (tail.length > 16 * 1024) { tail = tail.slice(0, 16 * 1024) + '…'; dropped.value = true }
    const next = [...rows.value, ...lines]
    textSize = next.reduce((sum, line) => sum + bytes(line) + 1, bytes(tail))
    while (next.length + (tail ? 1 : 0) > MAX_LINES || textSize > MAX_TEXT) {
      const first = next.shift(); if (first === undefined) break
      textSize -= bytes(first) + 1; dropped.value = true
    }
    rows.value = next
  }
  const partial = ref('')
  const consume = (event: LogEvent) => {
    if (event.kind === 'data') { append(event.data); partial.value = tail }
    else {
      running.value = false; starting.value = false; pendingId = ''
      if (event.kind === 'error') { error.value = event.data; status.value = '追踪失败' }
      else status.value = event.data || '日志追踪已结束'
    }
  }
  const unsubscribe = api.onEvent((event: AppEvent) => {
    if (event.type !== 'log' || disposed || !pendingId || event.sessionId !== pendingId) return
    if (watchId) { if (event.watchId === watchId) consume(event); return }
    if (starting.value) {
      queued.push(event)
      queuedChars += event.data.length
      while (queuedChars > 64 * 1024 && queued.length > 1) { queuedChars -= queued.shift()!.data.length; queuedOverflow = true }
      if (queuedChars > 64 * 1024) { queued[0] = { ...event, data: event.data.slice(-64 * 1024) }; queuedChars = queued[0].data.length; queuedOverflow = true }
    }
  })
  let stopping: Promise<unknown> | undefined
  function stop(): void {
    generation++
    const id = pendingId
    pendingId = ''; watchId = ''; running.value = false; starting.value = false; queued = []; queuedChars = 0; queuedOverflow = false
    if (id) {
      const request = api.stopLog(id).catch(() => {})
      const pending = stopping ? Promise.all([stopping, request]) : request
      stopping = pending
      void pending.then(() => { if (stopping === pending) stopping = undefined })
    }
    status.value = rows.value.length || tail ? '已暂停 · 显示已接收内容' : '尚未开始追踪'
  }
  async function start(path: string, lines: number): Promise<void> {
    stop()
    const s = source()
    if (disposed || !s.id || !s.connected) { error.value = '请先连接服务器'; return }
    const token = generation, id = s.id
    pendingId = id; starting.value = true; error.value = ''; status.value = '正在打开日志…'
    try {
      if (stopping) {
        await stopping
        if (disposed || generation !== token) return
      }
      const result = await api.startLog(id, { path, lines })
      if (token !== generation || disposed) return
      if (!result.ok) throw new Error(result.error)
      const overflow = queuedOverflow
      clear(); dropped.value = overflow; partial.value = ''; watchId = result.value; starting.value = false; running.value = true; status.value = '正在追踪'
      const initial = queued; queued = []; queuedChars = 0
      for (const event of initial) if (event.watchId === watchId) consume(event)
    } catch (cause) {
      if (token !== generation || disposed) return
      pendingId = ''; watchId = ''; starting.value = false; running.value = false
      error.value = (cause as Error).message; status.value = '无法追踪日志'
    }
  }
  let identity = ''
  watch(() => [source().id, source().identity, source().connected], () => {
    stop()
    const key = JSON.stringify([source().id, source().identity])
    if (key !== identity) { identity = key; clear(); partial.value = ''; error.value = '' }
    if (!source().connected) status.value = '等待连接 · 追踪已停止'
  }, { immediate: true, flush: 'sync' })
  onScopeDispose(() => { disposed = true; stop(); unsubscribe() })
  return { rows, partial, running, starting, error, status, dropped, start, stop, clear: () => { clear(); partial.value = '' } }
}
