import { computed, onScopeDispose, ref, watch } from 'vue'
import { connectionIdentity } from '../../shared/connection'
import { active, connect, navigate, state, type Workspace } from './workbench'

export type SplitDirection = 'horizontal' | 'vertical'
export type SplitEdge = 'left' | 'right' | 'top' | 'bottom'
export interface ShownTerminalSplit { first: string; second: string; direction: SplitDirection }
export interface TerminalSplitDestination { anchorId: string; edge: SplitEdge }
export const MIN_SPLIT_RATIO = 20
export const MAX_SPLIT_RATIO = 80

export function terminalSplitEdge(bounds: Pick<DOMRect, 'left' | 'top' | 'width' | 'height'>, clientX: number, clientY: number): SplitEdge | undefined {
  if (bounds.width <= 0 || bounds.height <= 0) return
  const x = (clientX - bounds.left) / bounds.width
  const y = (clientY - bounds.top) / bounds.height
  if (x < 0 || x > 1 || y < 0 || y > 1) return
  const nearest = Math.min(x, 1 - x, y, 1 - y)
  if (nearest > .26) return
  if (nearest === x) return 'left'
  if (nearest === 1 - x) return 'right'
  return nearest === y ? 'top' : 'bottom'
}

export function useTerminalLayout(workspace: () => HTMLElement | undefined = () => undefined) {
  const followDirectory = ref(false)
  const split = ref<{ first: Workspace; second: Workspace; direction: SplitDirection } | null>(null)
  const splitChoice = ref<{ source: Workspace; direction: SplitDirection } | null>(null)
  const splitDragSource = ref<Workspace | null>(null)
  const splitDragTarget = ref<TerminalSplitDestination>()
  let pointer: { id: number; source: HTMLElement; session: Workspace; sessionId: string; x: number; y: number } | undefined
  let suppressTabClick = false
  let suppressTabDoubleClick = false
  const splitRatio = ref(50)
  const splitBusy = ref(false)
  const shown = computed<ShownTerminalSplit | null>(() => {
    const layout = split.value, session = active.value
    if (!layout || !session || session.activeDocument !== 'terminal' || (session !== layout.first && session !== layout.second)) return null
    if (!state.sessions.includes(layout.first) || !state.sessions.includes(layout.second)) return null
    return { first: layout.first.id, second: layout.second.id, direction: layout.direction }
  })
  const splitTargets = computed(() => state.sessions.filter(session => session !== splitChoice.value?.source && session.phase === 'connected'))
  function sourceProfile(session: Workspace) {
    if (session.phase !== 'connected') return
    return state.profiles.find(profile => profile.id === session.profileId && connectionIdentity(profile) === session.connectionKey)
  }
  const canCreateSplit = computed(() => !!splitChoice.value && !!sourceProfile(splitChoice.value.source))

  function openSplit(direction: SplitDirection): void {
    const session = active.value
    if (session && !splitBusy.value) splitChoice.value = { source: session, direction }
  }

  function closeSplitChoice(): void {
    if (!splitBusy.value) splitChoice.value = null
  }

  function applySplit(first: Workspace, second: Workspace, direction: SplitDirection, mirrorReversedRatio = false): void {
    const previous = split.value
    if (mirrorReversedRatio && previous?.first === second && previous.second === first && previous.direction === direction) splitRatio.value = 100 - splitRatio.value
    else if (previous?.first !== first || previous.second !== second) splitRatio.value = 50
    split.value = { first, second, direction }
  }

  async function confirmSplit(targetId: string): Promise<void> {
    const choice = splitChoice.value
    if (!choice || splitBusy.value) return
    const { source, direction } = choice
    if (!state.sessions.includes(source)) throw new Error('原终端已关闭，请重新选择分屏')
    splitBusy.value = true
    try {
      let target: Workspace | undefined
      if (targetId) {
        target = splitTargets.value.find(session => session.id === targetId)
        if (!target) throw new Error('所选终端已断开，请重新选择')
      } else {
        const profile = sourceProfile(source)
        if (!profile) throw new Error('请先连接当前服务器，再新建终端')
        target = await connect(profile, {}, undefined, source.id)
      }
      if (!target || !state.sessions.includes(source) || !state.sessions.includes(target)) throw new Error('无法打开分屏，请重新选择终端')
      source.activeDocument = 'terminal'
      applySplit(source, target, direction)
      splitChoice.value = null
    } finally {
      splitBusy.value = false
    }
  }

  function startSplitDrag(event: PointerEvent, session: Workspace): void {
    if (!event.isPrimary || event.button !== 0 || pointer) return
    suppressTabClick = false
    if (session.phase !== 'connected' || !state.sessions.includes(session) || splitBusy.value) return
    const source = event.currentTarget as HTMLElement
    pointer = { id: event.pointerId, source, session, sessionId: session.id, x: event.clientX, y: event.clientY }
    source.setPointerCapture(event.pointerId)
    source.ownerDocument.defaultView?.addEventListener('keydown', cancelOnEscape, true)
    source.ownerDocument.defaultView?.addEventListener('blur', endSplitDrag)
  }

  function endSplitDrag(): void {
    const previous = pointer
    pointer = undefined
    splitDragSource.value = null
    splitDragTarget.value = undefined
    if (!previous) return
    const { source, id } = previous
    source.ownerDocument.defaultView?.removeEventListener('keydown', cancelOnEscape, true)
    source.ownerDocument.defaultView?.removeEventListener('blur', endSplitDrag)
    if (source.hasPointerCapture(id)) source.releasePointerCapture(id)
  }

  function cancelOnEscape(event: KeyboardEvent): void {
    if (event.key !== 'Escape' || !splitDragSource.value) return
    event.preventDefault()
    event.stopPropagation()
    endSplitDrag()
  }

  function destinationAt(x: number, y: number): TerminalSplitDestination | undefined {
    if (!pointer) return
    const layout = shown.value
    const anchors = layout ? [layout.first, layout.second] : [active.value?.id]
    for (const pane of workspace()?.querySelectorAll<HTMLElement>('[data-terminal-session]') ?? []) {
      const anchorId = pane.dataset.terminalSession
      if (!anchorId || anchorId === pointer.sessionId || !anchors.includes(anchorId)) continue
      const anchor = state.sessions.find(session => session.id === anchorId)
      if (anchor?.phase !== 'connected') continue
      const edge = terminalSplitEdge(pane.getBoundingClientRect(), x, y)
      if (edge) return { anchorId, edge }
    }
  }

  function moveSplitDrag(event: PointerEvent): void {
    if (pointer?.id !== event.pointerId) return
    if (!(event.buttons & 1)) { endSplitDrag(); return }
    if (!activateSplitDrag(event)) return
    event.preventDefault()
    splitDragTarget.value = destinationAt(event.clientX, event.clientY)
  }

  function finishSplitDrag(event: PointerEvent): void {
    if (pointer?.id !== event.pointerId) return
    const target = activateSplitDrag(event) && destinationAt(event.clientX, event.clientY)
    if (target) dropSplit(target.anchorId, target.edge)
    else endSplitDrag()
  }

  function activateSplitDrag(event: PointerEvent): boolean {
    if (!pointer) return false
    if (splitDragSource.value) return true
    if (Math.hypot(event.clientX - pointer.x, event.clientY - pointer.y) < 6) return false
    const { session, sessionId } = pointer
    suppressTabClick = true
    suppressTabDoubleClick = true
    if (session.id !== sessionId || session.phase !== 'connected' || !state.sessions.includes(session)) {
      endSplitDrag()
      return false
    }
    splitDragSource.value = session
    return true
  }

  function cancelSplitDrag(event: PointerEvent): void { if (pointer?.id === event.pointerId) endSplitDrag() }

  function consumeSplitDragClick(event: MouseEvent): boolean {
    if (!event.detail) return false
    if (event.type === 'dblclick') return suppressTabDoubleClick
    if (suppressTabClick) return true
    if (event.detail === 1) suppressTabDoubleClick = false
    return false
  }

  function dropSplit(anchorId: string, edge: SplitEdge): void {
    const dragged = splitDragSource.value
    const anchor = state.sessions.find(session => session.id === anchorId)
    endSplitDrag()
    if (!dragged || !anchor || dragged === anchor || dragged.phase !== 'connected' || anchor.phase !== 'connected' || !state.sessions.includes(dragged)) return
    const visible = shown.value
    if (visible ? anchorId !== visible.first && anchorId !== visible.second : anchor !== active.value) return

    const before = edge === 'left' || edge === 'top'
    applySplit(before ? dragged : anchor, before ? anchor : dragged, edge === 'left' || edge === 'right' ? 'horizontal' : 'vertical', true)
    const selected = active.value === dragged ? dragged : anchor
    selected.activeDocument = 'terminal'
    state.activeId = selected.id
  }

  function setSplitRatio(percent: number): void {
    if (Number.isFinite(percent)) splitRatio.value = Math.min(MAX_SPLIT_RATIO, Math.max(MIN_SPLIT_RATIO, percent))
  }

  function inSplit(session: Workspace): boolean {
    return shown.value?.first === session.id || shown.value?.second === session.id
  }

  function focusPane(session: Workspace): void {
    if (!inSplit(session) || state.activeId === session.id) return
    session.activeDocument = 'terminal'
    state.activeId = session.id
  }

  watch(() => state.sessions, sessions => {
    const layout = split.value
    if (layout && (!sessions.includes(layout.first) || !sessions.includes(layout.second))) split.value = null
  })

  watch(() => {
    const dragged = splitDragSource.value
    return dragged && state.sessions.includes(dragged) && dragged.phase === 'connected' ? dragged.id : null
  }, (id, previousId) => {
    if (previousId && id !== previousId) endSplitDrag()
  }, { flush: 'sync' })
  onScopeDispose(endSplitDrag)

  watch(() => [followDirectory.value, active.value?.id, active.value?.phase, active.value?.terminalDirectory] as const, async ([follow, id, phase, directory], _, invalidate) => {
    const session = active.value
    if (!follow || !session || !id || phase !== 'connected' || session.sftpError || !directory) return
    let current = true
    invalidate(() => { current = false })
    try {
      await navigate(directory, () => current && active.value?.id === id)
    } catch { /* The terminal can report a directory unavailable through SFTP. */ }
  })

  return { followDirectory, split, shown, openSplit, inSplit, focusPane, splitChoice, splitTargets, canCreateSplit, splitBusy, closeSplitChoice, confirmSplit, splitRatio, setSplitRatio, splitDragSource, splitDragTarget, startSplitDrag, moveSplitDrag, finishSplitDrag, cancelSplitDrag, consumeSplitDragClick }
}
