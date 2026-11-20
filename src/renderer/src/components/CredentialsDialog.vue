<script setup lang="ts">
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { X } from '@lucide/vue'
import { state, connect, needsLoginCredentials, profileJumpHops } from '../workbench'
import type { Credentials, Profile } from '../../../shared/types'

const config = state.credentialsDialog!
const modal = ref<HTMLDialogElement>(), busy = ref(false), error = ref('')
const hops = profileJumpHops(config.profile).reverse()
const needed = [config.profile, ...hops].filter(profile => needsLoginCredentials(profile, profile.id === config.profile.id ? config.targetCredentials : {}))
const secrets = reactive<Record<string, string>>(Object.fromEntries(needed.map(profile => [profile.id, ''])))
function provided(profile: Profile): Credentials {
  if (!Object.hasOwn(secrets, profile.id)) return {}
  const value = secrets[profile.id]
  if (!value) return {}
  return profile.authType === 'password' ? { password: value } : { passphrase: value }
}
function close(): void { if (!busy.value) state.credentialsDialog = null }
onMounted(() => { modal.value?.showModal(); modal.value?.querySelector('input')?.focus() })
onBeforeUnmount(() => { for (const id of Object.keys(secrets)) delete secrets[id]; config.targetCredentials = undefined })
async function submit(): Promise<void> {
  if (busy.value) return
  busy.value = true; error.value = ''
  try {
    const hopCredentials = Object.fromEntries(hops.map(hop => [hop.id, provided(hop)]))
    await connect(config.profile, { ...config.targetCredentials, ...provided(config.profile) }, config.reconnectId, undefined, undefined, undefined, hopCredentials)
    state.credentialsDialog = null
  } catch (e) { error.value = (e as Error).message }
  finally { busy.value = false }
}
</script>

<template>
  <dialog ref="modal" class="credentials-dialog" @cancel.prevent="close">
    <form @submit.prevent="submit">
      <header class="dialog-header"><div><h2>登录 {{ config.profile.name }}</h2><p>{{ config.profile.username }}@{{ config.profile.host }}:{{ config.profile.port }}</p></div><button class="icon-button" type="button" aria-label="关闭" :disabled="busy" @click="close"><X :size="18"/></button></header>
      <div class="dialog-body"><label v-for="profile in needed" :key="profile.id">{{ profile.id === config.profile.id ? '目标服务器' : '跳板机' }} {{ profile.name }} · {{ profile.authType === 'password' ? '密码' : '私钥口令' }}<input v-model="secrets[profile.id]" type="password" autocomplete="off" maxlength="16384" :required="profile.authType === 'password'" :placeholder="profile.authType === 'password' ? '请输入密码' : '私钥未加密时可留空'"></label><p v-if="error" class="error-box" role="alert">{{ error }}</p></div>
      <footer class="dialog-footer"><button type="button" :disabled="busy" @click="close">取消</button><button type="submit" class="primary" :disabled="busy">{{ busy ? '正在连接…' : '连接' }}</button></footer>
    </form>
  </dialog>
</template>
