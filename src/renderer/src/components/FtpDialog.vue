<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { ArrowUp, Download, Folder, RefreshCw, Upload, X } from '@lucide/vue'
import type { FTPConfig, RemoteEntry } from '../../../shared/types'
import { parentPath, sizeLabel, unwrap } from '../workbench'

const emit = defineEmits<{ close: [] }>()
const api = window.sshTool
const dialog = ref<HTMLDialogElement>()
const config = reactive<FTPConfig>({ host: '', port: 21, username: '', password: '', tls: true })
const directory = ref(''), entries = ref<RemoteEntry[]>([]), input = ref('')
const busy = ref(false), error = ref(''), status = ref('')
let mounted = true

onMounted(() => dialog.value?.showModal())
onBeforeUnmount(() => {
  mounted = false
  config.password = ''
  void api.ftpDisconnect?.()
})

async function run(work: () => Promise<void>): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = ''; status.value = ''
  try { await work() }
  catch (failure) { if (mounted) error.value = (failure as Error).message }
  finally { if (mounted) busy.value = false }
}

function show(result: { path: string; entries: RemoteEntry[] }): void {
  if (!mounted) return
  directory.value = input.value = result.path
  entries.value = result.entries
}

async function connect(): Promise<void> {
  const options = { ...config }
  config.password = ''
  show(unwrap(await api.ftpConnect!(options)))
}

async function list(path: string): Promise<void> { show(unwrap(await api.ftpList!(path))) }

async function upload(): Promise<void> {
  const count = unwrap(await api.ftpUpload!(directory.value))
  if (count) { await list(directory.value); status.value = `已上传 ${count} 个文件` }
}

async function download(entry: RemoteEntry): Promise<void> {
  if (unwrap(await api.ftpDownload!(entry.path))) status.value = `已下载 ${entry.name}`
}
</script>

<template>
  <dialog ref="dialog" class="ftp-dialog" @cancel.prevent="emit('close')">
    <header class="dialog-header"><div><h2>FTP 文件传输</h2><p>{{ directory ? config.host : '连接传统 FTP 或显式 FTPS 服务器' }}</p></div><button type="button" class="icon-button" aria-label="关闭" @click="emit('close')"><X :size="17"/></button></header>
    <form v-if="!directory" @submit.prevent="run(connect)">
      <div class="dialog-body">
        <div class="form-row host-row"><label>服务器<input v-model.trim="config.host" required maxlength="255" autocomplete="off"></label><label>端口<input v-model.number="config.port" type="number" min="1" max="65535" required></label></div>
        <label>用户名<input v-model.trim="config.username" required maxlength="128" autocomplete="off"></label>
        <label>密码<input v-model="config.password" type="password" maxlength="4096" autocomplete="new-password"></label>
        <label class="check"><input v-model="config.tls" type="checkbox">使用显式 FTPS（TLS）</label>
        <p v-if="!config.tls" class="error-box">明文 FTP 会在网络中传输未加密的密码和文件。</p>
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      </div>
      <footer class="dialog-footer"><button type="button" @click="emit('close')">取消</button><button type="submit" class="primary" :disabled="busy">{{ busy ? '连接中…' : '连接' }}</button></footer>
    </form>
    <template v-else>
      <div class="dialog-body">
        <form class="ftp-actions" @submit.prevent="run(() => list(input))">
          <button type="button" aria-label="上级目录" :disabled="busy || directory === '/'" @click="run(() => list(parentPath(directory)))"><ArrowUp :size="15"/></button>
          <input v-model.trim="input" aria-label="FTP 远程目录" maxlength="4096" :disabled="busy">
          <button type="button" aria-label="刷新目录" :disabled="busy" @click="run(() => list(directory))"><RefreshCw :size="15"/></button>
          <button type="button" :disabled="busy" @click="run(upload)"><Upload :size="15"/>上传</button>
        </form>
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
        <p v-if="busy || status" class="muted" role="status">{{ busy ? '处理中…' : status }}</p>
        <ul class="ftp-list">
          <li v-for="entry in entries" :key="entry.path">
            <button v-if="entry.type === 'directory'" type="button" class="file-row" :disabled="busy" @click="run(() => list(entry.path))"><Folder :size="15"/><span>{{ entry.name }}</span></button>
            <span v-else>{{ entry.name }} <small v-if="entry.type === 'file'" class="muted">{{ sizeLabel(entry.size) }}</small></span>
            <button v-if="entry.type === 'file'" type="button" class="icon-button" :aria-label="`下载 ${entry.name}`" :disabled="busy" @click="run(() => download(entry))"><Download :size="15"/></button>
          </li>
        </ul>
        <p v-if="!entries.length" class="muted">此目录没有可显示的文件。</p>
      </div>
      <footer class="dialog-footer"><button type="button" @click="emit('close')">断开并关闭</button></footer>
    </template>
  </dialog>
</template>

<style scoped>
.ftp-dialog { width: 640px; }
.ftp-actions { display: flex; align-items: center; gap: 6px; }
.ftp-actions input { flex: 1; }
.ftp-list { max-height: 48vh; overflow: auto; list-style: none; padding: 0; margin: 0; }
.ftp-list li { display: flex; gap: 8px; justify-content: space-between; align-items: center; min-height: 32px; padding: 0 5px; border-bottom: 1px solid var(--border); }
.ftp-list li > span { overflow-wrap: anywhere; }
</style>
