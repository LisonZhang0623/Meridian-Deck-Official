<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { ShieldCheck } from '@lucide/vue'
import { state, unwrap } from '../workbench'

const pending = computed(() => state.authChallenges[0])
const modal = ref<HTMLDialogElement>()
const answers = ref<string[]>([]), busy = ref(false), error = ref('')

watch(() => pending.value?.challengeId, async id => {
  answers.value.fill('')
  answers.value = pending.value?.questions.map(() => '') ?? []
  error.value = ''
  await nextTick()
  if (!modal.value) return
  if (id && pending.value?.challengeId === id) {
    if (!modal.value.open) modal.value.showModal()
    modal.value.querySelector('input')?.focus()
  } else if (!pending.value) modal.value.close()
})

async function reply(accept: boolean): Promise<void> {
  const challenge = pending.value, send = window.sshTool.answerChallenge
  if (!challenge || !send || busy.value) return
  busy.value = true
  error.value = ''
  try {
    unwrap(await send(challenge.sessionId, challenge.challengeId, accept ? answers.value.slice() : null))
    state.authChallenges = state.authChallenges.filter(item => item.challengeId !== challenge.challengeId)
  } catch (cause) {
    if (pending.value?.challengeId === challenge.challengeId) error.value = (cause as Error).message
  } finally { busy.value = false }
}
</script>

<template>
  <dialog ref="modal" class="credentials-dialog" @cancel.prevent="reply(false)">
    <form v-if="pending" @submit.prevent="reply(true)">
      <header class="dialog-header"><div><h2>SSH 交互式身份验证</h2><p>{{ pending.label }} · {{ pending.host }}</p></div><ShieldCheck :size="24"/></header>
      <div class="dialog-body">
        <p v-if="pending.instruction" class="muted challenge-text">{{ pending.instruction }}</p>
        <label v-for="(question, index) in pending.questions" :key="index" class="challenge-text">{{ question || `验证问题 ${index + 1}` }}<input v-model="answers[index]" :type="pending.echo[index] ? 'text' : 'password'" maxlength="4096" autocomplete="off" spellcheck="false" :disabled="busy"></label>
        <p v-if="error" class="error-box" role="alert">{{ error }}</p>
      </div>
      <footer class="dialog-footer"><button type="button" :disabled="busy" @click="reply(false)">取消连接</button><button type="submit" class="primary" :disabled="busy">{{ busy ? '正在验证…' : '提交验证' }}</button></footer>
    </form>
  </dialog>
</template>

<style scoped>
.challenge-text { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
