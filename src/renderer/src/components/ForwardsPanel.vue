<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { Copy, Network, X, Play, Plus, Square, ExternalLink, Trash2 } from '@lucide/vue'
import { openTensorBoard } from '../tensorboard'
import { active, attempt, notify, unwrap } from '../workbench'
import type { ForwardConfig, PortForward } from '../../../shared/v2'
import '../network.css'

defineEmits<{ close: [] }>()
const api = window.sshTool, values = ref<PortForward[]>([]), busy = ref(false), error = ref('')
const formOpen = ref(false), tensorPort = ref(6006)
const form = reactive<ForwardConfig>({ name: '远程 Web 服务', mode: 'local', localHost: '127.0.0.1', localPort: 0, remoteHost: '127.0.0.1', remotePort: 6006 })
const remoteDynamic = reactive({ name: '反向 SOCKS5', port: 0 })
const remoteHttp = reactive({ name: '共享本机 HTTP 代理', proxyPort: 7897, listenPort: 0 })
const namedForm = computed(() => {
  if (form.mode === 'remote-dynamic') return remoteDynamic
  if (form.mode === 'remote-http') return remoteHttp
  return form
})
const current = computed(() => values.value.filter(item => item.sessionId === active.value?.id))
const listeningCount = computed(() => current.value.filter(item => item.status === 'listening').length)
const showForm = computed(() => formOpen.value || !current.value.length)
let refreshVersion = 0, mounted = true
async function refreshForwards(): Promise<void> {
  const version = ++refreshVersion
  try {
    const list = unwrap(await api.listForwards())
    if (version === refreshVersion) values.value = list
  } catch (cause) {
    if (version === refreshVersion && !error.value) error.value = (cause as Error).message
  }
}
// Treat events as invalidation notices so an older event cannot restore a removed row.
const unsubscribe = api.onEvent(event => { if (event.type === 'forwards') void refreshForwards() })
onMounted(refreshForwards)
onBeforeUnmount(() => {
  mounted = false
  refreshVersion++
  unsubscribe()
})
async function start(): Promise<void> {
  const session = active.value
  if (!session || session.phase !== 'connected' || busy.value) return
  busy.value = true; error.value = ''
  let config: ForwardConfig = { ...form }
  if (form.mode === 'remote-dynamic') {
    config = { name: remoteDynamic.name, mode: 'remote-dynamic', localPort: 0, remoteHost: '', remotePort: remoteDynamic.port }
  } else if (form.mode === 'remote-http') {
    config = { name: remoteHttp.name, mode: 'remote-http', localHost: '127.0.0.1', localPort: remoteHttp.proxyPort, remoteHost: '', remotePort: remoteHttp.listenPort }
  }
  try {
    unwrap(await api.startForward(session.id, config))
    formOpen.value = false
  }
  catch (cause) { error.value = (cause as Error).message }
  finally {
    await refreshForwards()
    busy.value = false
  }
}
async function launchTensorBoard() {
  const session = active.value
  if (!session || session.phase !== 'connected' || busy.value) return
  const id = session.id
  const current = () => mounted && active.value === session && session.id === id && session.phase === 'connected'
  busy.value = true
  error.value = ''
  try { await openTensorBoard(api, id, tensorPort.value, current) }
  catch (cause) { if (current()) error.value = (cause as Error).message }
  finally { await refreshForwards(); busy.value = false }
}
async function stop(id: string): Promise<void> {
  unwrap(await api.stopForward(id))
  await refreshForwards()
}
async function dismiss(id: string): Promise<void> {
  if (!api.dismissForward) return
  unwrap(await api.dismissForward(id))
  await refreshForwards()
}
async function copyProxy(item: PortForward, command = false): Promise<void> {
  if (item.mode !== 'remote-dynamic' || item.status !== 'listening' || !item.socksPassword) return
  const password = encodeURIComponent(item.socksPassword).replaceAll("'", '%27')
  const url = `socks5h://sshtool:${password}@127.0.0.1:${item.remotePort}`
  await navigator.clipboard.writeText(command ? `curl --noproxy '' --proxy '${url}' https://example.com/` : url)
  notify(command ? '已复制命令，请粘贴到服务器终端执行' : '已复制地址，请填入软件的 SOCKS5 代理设置')
}
async function copyHttpProxy(item: PortForward, launchCodex = false): Promise<void> {
  if (item.mode !== 'remote-http' || item.status !== 'listening') return
  const url = `http://127.0.0.1:${item.remotePort}`
  const command = [
    'env -u ALL_PROXY -u all_proxy -u NO_PROXY -u no_proxy -u WS_PROXY -u ws_proxy -u WSS_PROXY -u wss_proxy',
    `HTTP_PROXY='${url}' http_proxy='${url}' HTTPS_PROXY='${url}' https_proxy='${url}' codex`
  ].join(' ')
  await navigator.clipboard.writeText(launchCodex ? command : url)
  notify(launchCodex ? '已复制 Codex 启动命令，请粘贴到服务器终端执行' : '已复制服务器 HTTP 代理地址')
}
</script>

<template>
  <section id="sidebar-panel-forwards" class="network-panel" aria-label="端口转发">
    <div class="section-heading">
      <span><Network :size="15"/>端口转发</span>
      <div class="sidebar-heading-actions">
        <button v-if="current.length" type="button" class="icon-button network-new-toggle" :class="{ active: formOpen }" :aria-expanded="formOpen" @click="formOpen = !formOpen"><Plus :size="13"/>{{ formOpen ? '收起表单' : '新建' }}</button>
        <button class="icon-button" aria-label="收起端口转发" @click="$emit('close')"><X :size="15"/></button>
      </div>
    </div>
    <div class="side-context">
      <span :title="active?.name">{{ active?.name || '尚未选择服务器' }}</span>
      <span class="mono">{{ listeningCount }} 条监听中</span>
    </div>
    <div class="network-scroll">
      <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      <form class="network-form tensorboard-form" @submit.prevent="launchTensorBoard">
        <label>TensorBoard 端口<input v-model.number="tensorPort" type="number" min="1" max="65535" required></label>
        <button type="submit" :disabled="active?.phase !== 'connected' || busy"><ExternalLink :size="13"/>打开 TensorBoard</button>
      </form>
      <form v-if="showForm" class="network-form" @submit.prevent="start">
        <label v-if="api.connectChain">转发类型
          <select v-model="form.mode">
            <option value="local">本地 TCP 转发（本机 → 服务器）</option>
            <option value="remote">远程 TCP 转发（服务器 → 本机）</option>
            <option value="dynamic">动态 SOCKS5（本机 → 服务器）</option>
            <option value="remote-dynamic">反向 SOCKS5（使用本机网络）</option>
            <option value="remote-http">共享本机 HTTP 代理</option>
          </select>
        </label>
        <label>名称<input v-model="namedForm.name" required maxlength="100"></label>
        <label v-if="form.mode === 'remote-dynamic'">服务器监听端口<input v-model.number="remoteDynamic.port" type="number" min="0" max="65535" required></label>
        <div v-else-if="form.mode === 'remote-http'" class="network-fields">
          <label>本机 HTTP 代理端口<input v-model.number="remoteHttp.proxyPort" type="number" min="1" max="65535" required></label>
          <label>服务器监听端口<input v-model.number="remoteHttp.listenPort" type="number" min="0" max="65535" required></label>
        </div>
        <div v-else class="network-fields">
          <label>{{ form.mode === 'remote' ? '本地目标端口' : '本地监听端口' }}<input v-model.number="form.localPort" type="number" :min="form.mode === 'remote' ? 1 : 0" max="65535" required></label>
          <label v-if="form.mode !== 'dynamic'">{{ form.mode === 'remote' ? '远程监听端口' : '远程目标端口' }}<input v-model.number="form.remotePort" type="number" :min="form.mode === 'remote' ? 0 : 1" max="65535" required></label>
        </div>
        <label v-if="form.mode === 'remote'">本地目标主机<input v-model="form.localHost" required maxlength="255" spellcheck="false"></label>
        <label v-else-if="form.mode === 'local'">远程目标主机<input v-model="form.remoteHost" required maxlength="255" spellcheck="false"></label>
        <p v-if="form.mode === 'remote'" class="network-note">服务器 → 本机。请求服务器在 127.0.0.1 监听，端口填 0 自动分配。目标从本机访问。</p>
        <p v-else-if="form.mode === 'dynamic'" class="network-note">在本机 127.0.0.1 提供无认证 SOCKS5 TCP 代理，由 SSH 服务器访问请求的目标。监听端口填 0 自动分配。</p>
        <p v-else-if="form.mode === 'remote-dynamic'" class="network-note">请求 SSH 服务器监听 127.0.0.1，端口填 0 自动分配。服务器程序使用 SOCKS5 代理通过本机访问目标。需由 SSH 服务端允许远程转发，实际监听地址受服务端配置约束。</p>
        <p v-else-if="form.mode === 'remote-http'" class="network-note">服务器 → 本机代理。填写 Clash 的 HTTP 或混合端口。服务器监听端口填 0 自动分配，需由 SSH 服务端允许远程转发。</p>
        <p v-else class="network-note">本机 → 服务器。本机在 127.0.0.1 监听，端口填 0 自动分配。目标从当前 SSH 服务器访问。</p>
        <button class="primary" type="submit" :disabled="active?.phase !== 'connected' || busy"><Play :size="13"/>{{ busy ? '正在启动…' : '启动转发' }}</button>
      </form>
      <div v-if="!current.length" class="side-empty">当前会话还没有端口转发。</div>
      <article v-for="item in current" :key="item.id" class="network-card">
        <header>
          <strong>{{ item.name }}</strong>
          <span class="status-pill" :class="item.status === 'listening' ? 'ok' : item.status === 'failed' ? 'err' : 'neutral'">
            <span v-if="item.status === 'listening'" class="connection-dot" data-phase="connected" aria-hidden="true"></span>
            {{ item.status === 'listening' ? '监听中' : item.status === 'failed' ? '失败' : '已停止' }}
          </span>
        </header>
        <code v-if="item.mode === 'remote'">服务器监听 127.0.0.1:{{ item.remotePort }} → 本机目标 {{ item.localHost }}:{{ item.localPort }}</code>
        <code v-else-if="item.mode === 'dynamic'">SOCKS5 · 127.0.0.1:{{ item.localPort }}</code>
        <code v-else-if="item.mode === 'remote-dynamic'">反向 SOCKS5 · 服务器 127.0.0.1:{{ item.remotePort }}</code>
        <code v-else-if="item.mode === 'remote-http'">服务器代理 127.0.0.1:{{ item.remotePort }} → 本机 HTTP :{{ item.localPort }}</code>
        <code v-else>本机监听 127.0.0.1:{{ item.localPort }} → 远程目标 {{ item.remoteHost }}:{{ item.remotePort }}</code>
        <p v-if="item.mode === 'remote-dynamic' && item.status === 'listening'" class="network-note">在服务器终端测试时，复制整条 curl 命令。代理地址用于软件的 SOCKS5 设置，两者都包含本次转发的口令。</p>
        <p v-else-if="item.mode === 'remote-http' && item.status === 'listening'" class="network-note">在服务器终端运行复制的命令，为本次 Codex 进程设置代理。本机代理若启用了认证，服务器上的代理设置也需提供凭据。</p>
        <p v-if="item.error" class="danger">{{ item.error }}</p>
        <div class="network-actions">
          <button v-if="item.status === 'listening' && (!item.mode || item.mode === 'local')" class="primary" @click="attempt(async () => unwrap(await api.openForward(item.id)))"><ExternalLink :size="12"/>打开页面</button>
          <template v-if="item.mode === 'remote-dynamic' && item.status === 'listening' && item.socksPassword">
            <button @click="attempt(() => copyProxy(item, true))"><Copy :size="12"/>复制 curl 命令</button>
            <button @click="attempt(() => copyProxy(item))"><Copy :size="12"/>复制代理地址</button>
          </template>
          <template v-if="item.mode === 'remote-http' && item.status === 'listening'">
            <button @click="attempt(() => copyHttpProxy(item))"><Copy :size="12"/>复制代理</button>
            <button @click="attempt(() => copyHttpProxy(item, true))"><Copy :size="12"/>Codex 命令</button>
          </template>
          <button v-if="item.status === 'listening'" class="network-stop" @click="attempt(() => stop(item.id))"><Square :size="11"/>停止</button>
          <button v-else-if="api.dismissForward" @click="attempt(() => dismiss(item.id))"><Trash2 :size="12"/>移除记录</button>
        </div>
      </article>
      <p v-if="active?.phase !== 'connected'" class="network-note">请先连接服务器。</p>
    </div>
    <div class="network-footnote">收起面板继续转发；断开所属终端后停止</div>
  </section>
</template>
