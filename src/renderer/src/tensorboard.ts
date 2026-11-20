import type { Api } from '../../shared/types'
import { unwrap } from './workbench'

export async function openTensorBoard(api: Pick<Api, 'listForwards' | 'startForward' | 'stopForward' | 'openForward'>,
  sessionId: string, port: number, current: () => boolean) {
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('请输入 1 至 65535 的服务器端口')
  const forwards = unwrap(await api.listForwards())
  if (!current()) return
  const reuse = forwards.find(item => item.sessionId === sessionId && item.status === 'listening' &&
    (!item.mode || item.mode === 'local') && item.localHost === '127.0.0.1' && item.remoteHost === '127.0.0.1' && item.remotePort === port)
  const tunnel = reuse || unwrap(await api.startForward(sessionId, {
    name: 'TensorBoard', mode: 'local', localHost: '127.0.0.1', localPort: 0, remoteHost: '127.0.0.1', remotePort: port,
  }))
  if (!current()) {
    if (!reuse) unwrap(await api.stopForward(tunnel.id))
    return
  }
  unwrap(await api.openForward(tunnel.id))
}
