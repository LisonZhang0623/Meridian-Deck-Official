import type { ITheme } from '@xterm/xterm'

const light: ITheme = {
  background: '#fcfdff', foreground: '#141d2b', cursor: '#2558b8', cursorAccent: '#fcfdff',
  selectionBackground: '#bed7ff',
  black: '#1e2633', red: '#c62f40', green: '#137547', yellow: '#965b00',
  blue: '#1d53b8', magenta: '#8b188c', cyan: '#087285', white: '#4b5263',
  brightBlack: '#5b677b', brightRed: '#d9263c', brightGreen: '#15803d', brightYellow: '#a16207',
  brightBlue: '#2563eb', brightMagenta: '#a218a0', brightCyan: '#0e7490', brightWhite: '#243044',
}

const dark: ITheme = {
  background: '#10141c', foreground: '#edf2fb', cursor: '#5b9cf6', cursorAccent: '#10141c',
  selectionBackground: '#294b79',
  black: '#232a38', red: '#fb7185', green: '#4ade80', yellow: '#fbbf24',
  blue: '#60a5fa', magenta: '#e879f9', cyan: '#22d3ee', white: '#dbe5f4',
  brightBlack: '#76859e', brightRed: '#fda4af', brightGreen: '#86efac', brightYellow: '#fde047',
  brightBlue: '#93c5fd', brightMagenta: '#f5a8ff', brightCyan: '#67e8f9', brightWhite: '#ffffff',
}

export function terminalTheme(isDark: boolean): ITheme {
  return isDark ? dark : light
}
