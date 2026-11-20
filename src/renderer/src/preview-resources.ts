import type { Api } from '../../shared/types'

export function externalWebUrl(raw: string): string | undefined {
  if (!/^https?:\/\//i.test(raw) || /[\s\\\u0000-\u001f]/.test(raw)) return
  try {
    const url = new URL(raw)
    if (!url.username && !url.password) return url.href
  } catch { /* Unsupported destination stays a text label. */ }
}

export function relativeRemotePath(documentPath: string, reference: string): string | undefined {
  let relative: string
  try { relative = decodeURIComponent(reference.split(/[?#]/, 1)[0]) } catch { return }
  if (!relative || /^(?:\/|[a-z][a-z\d+.-]*:)/i.test(relative) || /[\\\u0000-\u001f]/.test(relative)) return
  const parts = documentPath.split('/').slice(0, -1).filter(Boolean)
  for (const part of relative.split('/')) {
    if (part === '..') parts.pop()
    else if (part && part !== '.') parts.push(part)
  }
  return '/' + parts.join('/')
}

export function rasterData(value: string) {
  return /^data:image\/(?:png|jpeg|webp|gif|bmp);base64,[a-z\d+/]+=*$/i.test(value)
}

// A document can reference many assets or repeat the same image on every edit.
// Read serially and keep a bounded cache for the lifetime of this preview.
export function previewResources(api: Pick<Api, 'preview'>, sessionId: string, documentPath: string) {
  const cache = new Map<string, Promise<string | undefined>>()
  let queue = Promise.resolve(), size = 0, disposed = false
  function read(reference: string, kind: 'image' | 'css' | 'script') {
    const path = relativeRemotePath(documentPath, reference)
    const extension = { image: /\.(png|jpe?g|webp|gif|bmp)$/i, css: /\.css$/i, script: /\.(js|mjs)$/i }[kind]
    if (disposed || !path || !extension.test(path)) return Promise.resolve(undefined)
    const cached = cache.get(path)
    if (cached) return cached
    if (cache.size >= 32) return Promise.resolve(undefined)
    const result = queue.then(async () => {
      if (disposed || size >= 32 * 1024 * 1024) return
      const response = await api.preview(sessionId, path)
      if (disposed || !response.ok) return
      const asset = response.value
      if (kind === 'image' ? asset.kind !== 'image' || !rasterData(asset.content) : asset.kind !== 'text' || asset.page || asset.size > 256 * 1024) return
      if (asset.content.length + size > 32 * 1024 * 1024) return
      size += asset.content.length
      return asset.content
    }).catch(() => undefined)
    cache.set(path, result)
    queue = result.then(() => {})
    return result
  }
  return { read, dispose() { disposed = true; cache.clear() } }
}
export type PreviewResources = ReturnType<typeof previewResources>
