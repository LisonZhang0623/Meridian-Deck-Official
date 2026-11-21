import { computed, ref, watch, type Ref } from 'vue'
import type { OSC7Status } from '../../shared/types'
import { active, reloadDocument, sendTerminalInput, state, unwrap, workspaceRequest, type Workspace } from './workbench'

function preferenceKey(session: Workspace): string {
  return 'meridian.osc7.dismissed.' + JSON.stringify([session.profileId, session.endpoint])
}

export function useOsc7Setup(settingsOpen: Ref<boolean>) {
  const info = ref<OSC7Status>()
  const checking = ref(false), writing = ref(''), error = ref(''), dismissed = ref(false)
  const retryCount = ref(0), prepared = ref(''), submitted = ref('')
  const supported = !!window.sshTool.checkOsc7 && !!window.sshTool.configureOsc7 && !!window.sshTool.prepareOsc7
  const connected = computed(() => active.value?.phase === 'connected' ? active.value : undefined)
  const reporting = computed(() => !!connected.value?.terminalDirectory)
  const eligible = computed(() => !!connected.value && !connected.value.tmuxName && !connected.value.sftpError)
  const installing = computed(() => !!connected.value && writing.value === connected.value.id)
  const canConfigure = computed(() => eligible.value && !reporting.value && !!info.value?.path && !info.value.configured)
  const canEnable = computed(() => eligible.value && !reporting.value && !!info.value?.configured)
  const atPrompt = computed(() => !!connected.value?.terminalAtPrompt && !connected.value.terminalAlternate && !connected.value.terminalInputPending)
  const enableCommand = computed(() => info.value?.shell === 'zsh'
    ? 'builtin source "${ZDOTDIR:-$HOME}/.meridian-osc7-v1.sh"'
    : 'builtin source "$HOME/.meridian-osc7-v1.sh"')
  const manualCommand = computed(() => connected.value && prepared.value === connected.value.id && !atPrompt.value && !reporting.value ? enableCommand.value : '')
  const offer = computed(() => (canConfigure.value || canEnable.value && submitted.value !== connected.value?.id) && !dismissed.value && !settingsOpen.value)
  const status = computed(() => {
    if (!connected.value) return '连接 SSH 服务器后可配置。'
    if (reporting.value) return '当前终端已在报告目录。'
    if (connected.value.tmuxName) return '请在普通 SSH 终端中配置登录 Shell。'
    if (connected.value.sftpError) return '配置需要可用的 SFTP 连接。'
    if (installing.value) return '正在保存远端配置…'
    if (checking.value) return '正在检查远端 Shell…'
    if (error.value) return error.value
    if (info.value?.configured) return submitted.value === connected.value.id ? '已发送启用命令，等待当前终端报告目录。' : '启动配置已保存。回到空白 Shell 提示符即可在当前终端启用。'
    if (info.value && !info.value.path) return '当前 Shell 暂不支持一键配置，已支持 Bash 和 Zsh。'
    return info.value?.path ? '将目录上报添加到 ' + info.value.path : ''
  })

  watch([() => connected.value?.id, eligible, reporting, settingsOpen, retryCount], (_, __, cleanup) => {
    let current = true
    info.value = undefined
    checking.value = false
    error.value = ''
    const session = connected.value
    if (!supported || !session || !eligible.value || reporting.value) return
    try { dismissed.value = localStorage.getItem(preferenceKey(session)) === '1' } catch { dismissed.value = false }
    if (dismissed.value && !settingsOpen.value) return
    checking.value = true
    const timer = window.setTimeout(async () => {
      try {
        const result = unwrap(await window.sshTool.checkOsc7!(session.id))
        if (current && !installing.value && !info.value?.configured) info.value = result
      } catch (cause) { if (current) error.value = (cause as Error).message }
      finally { if (current) checking.value = false }
    }, settingsOpen.value ? 0 : 2500)
    cleanup(() => { current = false; window.clearTimeout(timer) })
  }, { immediate: true })

  watch(() => connected.value?.id, () => { prepared.value = ''; submitted.value = '' })
  function dismiss(): void {
    if (!connected.value) return
    dismissed.value = true
    try { localStorage.setItem(preferenceKey(connected.value), '1') } catch { /* Keep the current choice in memory. */ }
  }
  async function configure(enable = true): Promise<void> {
    const session = connected.value, path = info.value?.path
    if (!session || !path || !(canConfigure.value || enable && canEnable.value) || installing.value) return
    const request = workspaceRequest(session), revision = session.terminalInputRevision
    const sendNow = enable && atPrompt.value
    const editors = state.sessions.filter(s => s.connectionKey === session.connectionKey)
      .flatMap(s => s.documents.filter(d => d.path === path).map(doc => ({ session: s, doc })))
    if (editors.some(({ doc }) => doc.busy || doc.content !== doc.saved)) {
      error.value = '请先保存或关闭该 Shell 配置文件中的修改。'
      return
    }
    writing.value = request.id
    error.value = ''
    try {
      const prepare = enable ? window.sshTool.prepareOsc7! : window.sshTool.configureOsc7!
      const updated = unwrap(await prepare(request.id, path))
      if (!request.current() || session.phase !== 'connected') return
      if (connected.value?.id === request.id) {
        info.value = updated
        if (enable) prepared.value = request.id
        if (sendNow && atPrompt.value && session.terminalInputRevision === revision && !session.terminalDirectory) {
          sendTerminalInput(session, ' ' + enableCommand.value + '\r')
          submitted.value = request.id
        }
      }
      for (const item of editors) {
        if (!item.doc.busy && item.doc.content === item.doc.saved) await reloadDocument(item.session, item.doc)
      }
    } catch (cause) { if (connected.value?.id === request.id) error.value = (cause as Error).message }
    finally { if (writing.value === request.id) writing.value = '' }
  }
  return { supported, status, checking, installing, error, info, offer, canConfigure, canEnable, atPrompt, manualCommand, configure, dismiss, retry: () => retryCount.value++ }
}
