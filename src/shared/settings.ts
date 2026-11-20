import type { Settings, StartupAnimationVariant } from './types'

export function pickStartupAnimation(mode: string | null): StartupAnimationVariant | undefined {
  if (mode === 'random') return Math.random() < 0.5 ? 'echo' : 'orbit'
  if (mode === 'echo' || mode === 'orbit') return mode
}

export const MIN_TERMINAL_FONT_SIZE = 10
export const MAX_TERMINAL_FONT_SIZE = 32
export const DEFAULT_TERMINAL_FONT_SIZE = 13
export const MIN_EDITOR_FONT_SIZE = 10
export const MAX_EDITOR_FONT_SIZE = 32
export const DEFAULT_EDITOR_FONT_SIZE = 13
export const MIN_UI_ZOOM = 80
export const MAX_UI_ZOOM = 150
export const DEFAULT_UI_ZOOM = 100
export const UI_ZOOM_STEP = 10
export const MIN_SIDEBAR_WIDTH = 236
export const MAX_SIDEBAR_WIDTH = 640
export const MIN_GPU_REFRESH_SECONDS = 1
export const MAX_GPU_REFRESH_SECONDS = 3600
export const DEFAULT_GPU_REFRESH_SECONDS = 2
export const DEFAULT_SETTINGS: Settings = {
  sidebarSide: 'right', sidebarCollapsed: true, sidebarPanel: 'servers', sidebarWidth: 0, theme: 'system',
  terminalFontSize: DEFAULT_TERMINAL_FONT_SIZE, editorFontSize: DEFAULT_EDITOR_FONT_SIZE, uiZoom: DEFAULT_UI_ZOOM,
  startupAnimation: 'echo', gpuRefreshSeconds: DEFAULT_GPU_REFRESH_SECONDS, windowCloseAction: 'ask', checkUpdatesOnStartup: false,
}
