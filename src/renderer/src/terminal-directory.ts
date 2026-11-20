export function terminalDirectory(sequence: string): string | undefined {
  if (!sequence.startsWith('file://') || sequence.length > 16384) return
  try {
    const url = new URL(sequence)
    if (url.protocol !== 'file:' || url.search || url.hash) return
    const path = decodeURIComponent(url.pathname)
    if (!path.startsWith('/') || path.length > 4096 || /[\x00-\x1f\x7f]/.test(path)) return
    return path
  } catch { return }
}
