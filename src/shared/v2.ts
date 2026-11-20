export interface CommandSnippet { id: string; name: string; group: string; profileId: string; command: string; description: string }
export interface LogOptions { path: string; lines: number }
export type LogEvent = { type: 'log'; sessionId: string; watchId: string; kind: 'data' | 'error' | 'end'; data: string }
export interface ForwardConfig { name: string; mode?: 'local' | 'remote' | 'dynamic' | 'remote-dynamic' | 'remote-http'; localHost?: string; localPort: number; remoteHost: string; remotePort: number }
export interface PortForward extends ForwardConfig { id: string; sessionId: string; status: 'listening' | 'stopped' | 'failed'; error?: string; socksPassword?: string }
export interface ResourceSnapshot {
  collectedAt: number; cpu: { total: number; idle: number }; memory: { total: number; available: number }
  disk: { total: number; used: number; available: number; mount: string }
  disks?: { total: number; used: number; available: number; mount: string }[]
  network: { name: string; received: number; sent: number }[]; uptime: number; load: number[]
}
