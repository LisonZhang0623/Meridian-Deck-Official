<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { X, FolderOpen, Eye, EyeOff } from '@lucide/vue'
import { state, newProfile, saveProfile, connect, unwrap, needsLoginCredentials, profileJumpHops } from '../workbench'
import type { Credentials } from '../../../shared/types'

const config = state.connectionDialog!
const form = reactive({ ...newProfile(), ...config.profile })
const tagText = ref((form.tags || []).join(', '))
const advancedSSH = !!window.sshTool.connectChain
const jumpProfiles = computed(() => state.profiles.filter(profile => {
  if (profile.id === form.id || !advancedSSH && profile.jumpProfileId) return false
  try { profileJumpHops({ ...form, jumpProfileId: profile.id }); return true }
  catch { return false }
}))
const password = ref(''), passphrase = ref(''), visible = ref(false), busy = ref(false), error = ref('')
const modal = ref<HTMLDialogElement>()
const formElement = ref<HTMLFormElement>()
const passwordInput = ref<HTMLInputElement>()
function close(): void { if (!busy.value) state.connectionDialog = null }
onMounted(() => { modal.value?.showModal() })
onBeforeUnmount(() => { password.value = ''; passphrase.value = '' })
async function chooseKey(): Promise<void> { try { const path = unwrap(await window.sshTool.selectKey()); if (path) form.keyPath = path } catch (e) { error.value = (e as Error).message } }
function revealInvalidField(event: Event): void {
  const section = (event.target as HTMLElement).closest('details')
  if (section) section.open = true
}
async function submit(shouldConnect: boolean): Promise<void> {
  if (busy.value || !formElement.value?.reportValidity()) return
  error.value = ''
  if (!form.name.trim() || !form.host.trim() || !form.username.trim()) { error.value = '请填写连接名称、主机地址和用户名'; return }
  if (shouldConnect && form.authType === 'password' && !password.value && !form.hasCredentials) { error.value = '请输入登录密码'; passwordInput.value?.focus(); return }
  const credentials: Credentials = form.authType === 'password' ? { password: password.value || undefined } : form.authType === 'key' ? { passphrase: passphrase.value || undefined } : {}
  busy.value = true
  try {
    const tags = tagText.value.split(/[,，;；]/).map(tag => tag.trim()).filter(Boolean)
    const profile = await saveProfile({ ...form, tags }, credentials)
    if (shouldConnect) {
      if (needsLoginCredentials(profile, credentials) && profile.authType === 'password' || profileJumpHops(profile).some(hop => needsLoginCredentials(hop))) state.credentialsDialog = { profile, targetCredentials: credentials }
      else await connect(profile, credentials)
    }
    password.value = ''; passphrase.value = ''; state.connectionDialog = null
  } catch (e) { error.value = (e as Error).message }
  finally { busy.value = false }
}
</script>

<template>
  <dialog ref="modal" class="connection-dialog" aria-labelledby="connection-title" @cancel.prevent="close">
    <form ref="formElement" @submit.prevent="submit(true)" @invalid.capture="revealInvalidField">
      <header class="dialog-header"><h2 id="connection-title">{{ config.profile ? '编辑 SSH 连接' : '新建 SSH 连接' }}</h2><button type="button" class="icon-button" aria-label="关闭" :disabled="busy" @click="close"><X :size="18" /></button></header>
      <div class="dialog-body">
        <label>连接名称<input v-model="form.name" autofocus required maxlength="100" placeholder="例如：开发服务器"></label>
        <div class="form-row host-row"><label>主机地址<input v-model="form.host" required spellcheck="false" placeholder="IP 地址或域名"></label><label>端口<input v-model.number="form.port" type="number" required min="1" max="65535"></label></div>
        <div class="form-row">
          <label>用户名<input v-model="form.username" required autocomplete="off" spellcheck="false" placeholder="用户名"></label>
          <label>身份验证方式<select v-model="form.authType"><option value="password">密码登录</option><option value="key">私钥登录</option><option v-if="advancedSSH" value="agent">本机 SSH Agent</option><option v-if="advancedSSH" value="interactive">交互式登录（含 MFA）</option></select></label>
        </div>
        <label v-if="form.authType === 'password'">密码<div class="input-action"><input ref="passwordInput" v-model="password" :type="visible ? 'text' : 'password'" autocomplete="off" :placeholder="form.hasCredentials ? '已保存凭据，留空继续使用' : '请输入密码'"><button type="button" class="icon-button" :aria-label="visible ? '隐藏密码' : '显示密码'" @click="visible = !visible"><EyeOff v-if="visible" :size="16"/><Eye v-else :size="16"/></button></div></label>
        <template v-else-if="form.authType === 'key'"><label>私钥文件<div class="input-action"><input v-model="form.keyPath" required placeholder="选择本地私钥"><button type="button" title="选择私钥" aria-label="选择私钥" @click="chooseKey"><FolderOpen :size="16"/></button></div></label><label>私钥口令（可选）<input v-model="passphrase" type="password" autocomplete="off" :placeholder="form.hasCredentials ? '留空继续使用已保存的口令' : '私钥未加密时可留空'"></label></template>
        <label v-if="form.authType === 'password' || form.authType === 'key'" class="check"><input v-model="form.rememberCredentials" type="checkbox">使用系统安全存储记住登录凭据</label>
        <label>启动目录<input v-model="form.initialPath" placeholder="默认使用账户主目录" spellcheck="false"></label>
        <div class="connection-options">
          <details>
            <summary>分组与标签</summary>
            <div class="advanced">
              <label>分组<input v-model="form.group" list="groups" maxlength="80" placeholder="开发环境"><datalist id="groups"><option>开发环境</option><option>测试环境</option><option>科研服务器</option></datalist></label>
              <label>服务器标签<input v-model="tagText" maxlength="420" placeholder="例如：训练, A100, 生产" aria-describedby="server-tags-help"><small id="server-tags-help" class="muted">用逗号或分号分隔，最多 12 个，每个最多 24 字。</small></label>
            </div>
          </details>
          <details>
            <summary>跳板机与高级设置 <small v-if="form.jumpProfileId || form.proxyAddress" class="muted">（已配置）</small></summary>
            <div class="advanced">
              <label>通过跳板机连接<select v-model="form.jumpProfileId"><option :value="undefined">直接连接</option><option v-for="jump in jumpProfiles" :key="jump.id" :value="jump.id">{{ jump.name }} · {{ jump.username }}@{{ jump.host }}</option></select><small class="muted">{{ advancedSSH ? '最多 5 跳，各跳分别核验指纹。' : '可选择已保存的直连服务器。' }}</small></label>
              <div class="form-row"><label>连接超时（秒）<input v-model.number="form.timeout" type="number" min="5" max="120" required></label></div>
              <label v-if="advancedSSH">SOCKS5 代理地址（可选）<input v-model="form.proxyAddress" maxlength="270" placeholder="127.0.0.1:1080" spellcheck="false"><small class="muted">无认证 SOCKS5，配置在直连服务器或跳板链的最外一跳。</small></label>
              <label v-if="advancedSSH && form.authType !== 'interactive'" class="check"><input v-model="form.keyboardInteractive" type="checkbox">允许服务器追加交互式验证（例如验证码）</label>
              <label class="check"><input v-model="form.keepalive" type="checkbox">保持连接，每 15 秒发送一次保活</label>
            </div>
          </details>
        </div>
      </div>
      <p v-if="error" class="error-box connection-error" role="alert">{{ error }}</p>
      <footer class="dialog-footer"><button type="button" class="cancel" :disabled="busy" @click="close">取消</button><button type="button" :disabled="busy" @click="submit(false)">仅保存</button><button class="primary" type="submit" :disabled="busy">{{ busy ? '正在处理…' : '保存并连接' }}</button></footer>
    </form>
  </dialog>
</template>

<style scoped>
.connection-dialog[open] { display: flex; flex-direction: column; overflow: hidden; }
.connection-dialog form { display: flex; flex-direction: column; min-height: 0; }
.connection-dialog .dialog-header { align-items: center; padding-block: 12px; }
.connection-dialog .dialog-header,
.connection-dialog .dialog-footer { flex: none; }
.connection-dialog .dialog-body { min-height: 0; overflow-y: auto; gap: 12px; padding-block: 14px; }
.connection-dialog .dialog-footer { padding-block: 10px; }
.connection-options details { border-top: 1px solid var(--border); }
.connection-options summary { padding: 9px 0; font-size: inherit; }
.connection-options summary small { font-size: .85em; }
.connection-options .advanced { margin: 0 0 12px; }
.connection-error { flex: none; margin: 0 22px 10px; max-height: 6em; overflow: auto; }
</style>
