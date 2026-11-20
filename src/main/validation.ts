import { randomUUID } from 'node:crypto'
import type { Credentials, Profile, Settings } from '../shared/types'
import { DEFAULT_TERMINAL_FONT_SIZE, MIN_TERMINAL_FONT_SIZE, MAX_TERMINAL_FONT_SIZE, DEFAULT_EDITOR_FONT_SIZE, MIN_EDITOR_FONT_SIZE, MAX_EDITOR_FONT_SIZE, DEFAULT_UI_ZOOM, MIN_UI_ZOOM, MAX_UI_ZOOM, MIN_SIDEBAR_WIDTH, MAX_SIDEBAR_WIDTH, DEFAULT_GPU_REFRESH_SECONDS, MIN_GPU_REFRESH_SECONDS, MAX_GPU_REFRESH_SECONDS } from '../shared/settings'

export function text(value: unknown, name: string, max = 4096): string {
  if (typeof value !== 'string' || value.length > max || value.includes('\0')) throw new Error(`${name}格式不正确`)
  return value
}
export function remotePath(value: unknown): string {
  const path = text(value, '远程路径')
  if (!path || /[\r\n]/.test(path)) throw new Error('远程路径不能为空或包含换行')
  return path
}
export function entryName(value: unknown): string {
  const name = text(value, '文件名', 255).trim()
  if (!name || name === '.' || name === '..' || /[\\/\r\n]/.test(name)) throw new Error('名称不能为空，也不能包含路径分隔符')
  return name
}
export function id(value: unknown): string {
  if (typeof value !== 'string' || !/^[a-z0-9-]{1,80}$/i.test(value)) throw new Error('无效的标识符')
  return value
}
export function tags(value: unknown): string[] {
  if (value === undefined) return []
  if (!Array.isArray(value) || value.length > 200) throw new Error('服务器标签最多 12 个')
  const unique = new Set<string>()
  for (const item of value) {
    const tag = text(item, '服务器标签').trim()
    if (tag.length > 24) throw new Error('每个服务器标签最多 24 字')
    if (/[\r\n]/.test(tag)) throw new Error('服务器标签不能包含换行')
    if (tag) unique.add(tag)
  }
  if (unique.size > 12) throw new Error('服务器标签最多 12 个')
  return [...unique]
}
export function profile(input: Profile): Profile {
  if (!input || typeof input !== 'object') throw new Error('无效的连接配置')
  const host = text(input.host, '服务器地址', 255).trim()
  if (!host || /[\s/\\]/.test(host)) throw new Error('请输入有效的 IP 地址或域名')
  const name = text(input.name, '连接名称', 100).trim()
  const username = text(input.username, '用户名', 128).trim()
  if (!name || !username || /[\r\n]/.test(username)) throw new Error('连接名称和用户名不能为空')
  if (!Number.isInteger(input.port) || input.port < 1 || input.port > 65535) throw new Error('端口必须是 1–65535 的整数')
  if (!Number.isInteger(input.timeout) || input.timeout < 5 || input.timeout > 120) throw new Error('连接超时必须为 5–120 秒')
  if (!['password', 'key'].includes(input.authType)) throw new Error('未知的身份验证方式')
  const keyPath = text(input.keyPath || '', '私钥路径')
  if (input.authType === 'key' && !keyPath) throw new Error('请选择私钥文件')
  const initialPath = text(input.initialPath || '', '初始目录')
  if (/[\r\n]/.test(initialPath)) throw new Error('初始目录不能包含换行')
  if (input.lastConnectedAt !== undefined && (!Number.isSafeInteger(input.lastConnectedAt) || input.lastConnectedAt < 0)) throw new Error('最近连接时间格式不正确')
  return {
    id: input.id ? id(input.id) : randomUUID(), name, host, username, port: input.port,
    group: text(input.group || '', '分组', 80).trim(), authType: input.authType, keyPath, initialPath,
    timeout: input.timeout, keepalive: !!input.keepalive, rememberCredentials: !!input.rememberCredentials,
    tags: tags(input.tags), ...(input.lastConnectedAt === undefined ? {} : { lastConnectedAt: input.lastConnectedAt }),
    ...(input.jumpProfileId ? { jumpProfileId: id(input.jumpProfileId) } : {})
  }
}
export function credentials(value: Credentials): Credentials {
  if (!value || typeof value !== 'object') throw new Error('无效的凭据')
  return { password: value.password === undefined ? undefined : text(value.password, '密码', 16384), passphrase: value.passphrase === undefined ? undefined : text(value.passphrase, '私钥口令', 16384) }
}
export function settings(value: Settings): Settings {
  if (!value || !['left', 'right'].includes(value.sidebarSide) || !['light', 'dark', 'system'].includes(value.theme)) throw new Error('无效的界面设置')
  const terminalFontSize = value.terminalFontSize === undefined ? DEFAULT_TERMINAL_FONT_SIZE : value.terminalFontSize
  if (!Number.isInteger(terminalFontSize) || terminalFontSize < MIN_TERMINAL_FONT_SIZE || terminalFontSize > MAX_TERMINAL_FONT_SIZE) throw new Error('终端字号必须是 10–32 的整数')
  const editorFontSize = value.editorFontSize === undefined || value.editorFontSize === 0 ? DEFAULT_EDITOR_FONT_SIZE : value.editorFontSize
  if (!Number.isInteger(editorFontSize) || editorFontSize < MIN_EDITOR_FONT_SIZE || editorFontSize > MAX_EDITOR_FONT_SIZE) throw new Error('编辑器字号必须是 10–32 的整数')
  const uiZoom = value.uiZoom === undefined || value.uiZoom === 0 ? DEFAULT_UI_ZOOM : value.uiZoom
  if (!Number.isInteger(uiZoom) || uiZoom < MIN_UI_ZOOM || uiZoom > MAX_UI_ZOOM) throw new Error('工作台缩放必须是 80–150 的整数百分比')
  const sidebarWidth = value.sidebarWidth === undefined ? 0 : value.sidebarWidth
  if (!Number.isInteger(sidebarWidth) || sidebarWidth !== 0 && (sidebarWidth < MIN_SIDEBAR_WIDTH || sidebarWidth > MAX_SIDEBAR_WIDTH)) throw new Error('侧栏宽度必须是 236–640 的整数，或使用自动宽度')
  const sidebarPanel = value.sidebarPanel === undefined ? 'servers' : value.sidebarPanel
  if (!['servers', 'files', 'gpu', 'commands', 'logs', 'forwards', 'resources'].includes(sidebarPanel)) throw new Error('无效的侧栏面板')
  const gpuRefreshSeconds = value.gpuRefreshSeconds === undefined ? DEFAULT_GPU_REFRESH_SECONDS : value.gpuRefreshSeconds
  if (!Number.isInteger(gpuRefreshSeconds) || gpuRefreshSeconds < MIN_GPU_REFRESH_SECONDS || gpuRefreshSeconds > MAX_GPU_REFRESH_SECONDS) throw new Error('显卡刷新间隔必须是 1–3600 的整数秒')
  const startupAnimation = value.startupAnimation === undefined ? 'echo' : value.startupAnimation
  if (!['echo', 'orbit', 'random', 'off'].includes(startupAnimation)) throw new Error('无效的启动动画设置')
  const windowCloseAction = value.windowCloseAction === undefined ? 'ask' : value.windowCloseAction
  if (!['ask', 'tray', 'quit'].includes(windowCloseAction)) throw new Error('无效的关闭窗口设置')
  // Older versions used a combined sidebar. Introduce the new rail on the right once.
  const sidebarSide = value.sidebarPanel === undefined ? 'right' : value.sidebarSide
  return { sidebarSide, sidebarCollapsed: !!value.sidebarCollapsed, sidebarPanel, sidebarWidth, theme: value.theme, terminalFontSize, editorFontSize, uiZoom, gpuRefreshSeconds, startupAnimation, windowCloseAction, checkUpdatesOnStartup: value.checkUpdatesOnStartup === true }
}
export function shellQuote(value: string): string { return "'" + value.replace(/'/g, "'\\''") + "'" }
