<script setup lang="ts">
import { computed } from 'vue'
import type { ShownTerminalSplit, SplitEdge, TerminalSplitDestination } from '../terminal-layout'
import { state, type Workspace } from '../workbench'

const props = defineProps<{ source: Workspace; active?: Workspace; shown: ShownTerminalSplit | null; target?: TerminalSplitDestination }>()
const targets = computed(() => {
  const layout = props.shown
  const panes = layout ? [
    { session: state.sessions.find(session => session.id === layout.first), position: 'first' },
    { session: state.sessions.find(session => session.id === layout.second), position: 'second' }
  ] : [{ session: props.active, position: 'whole' }]
  return panes.flatMap(({ session, position }) => session?.phase === 'connected' && session !== props.source ? [{ id: session.id, position }] : [])
})
const edgeLabels: Record<SplitEdge, string> = { left: '放到左侧', right: '放到右侧', top: '放到上方', bottom: '放到下方' }
</script>

<template>
  <div v-if="targets.length" class="terminal-split-drop" aria-hidden="true">
    <div v-for="{ id, position } in targets" :key="id" class="terminal-split-target" :class="position">
      <span v-if="!target" class="terminal-split-hint">拖到边缘分屏</span>
    </div>
    <div v-if="target" class="terminal-split-preview" :class="target.edge">{{ edgeLabels[target.edge] }}</div>
  </div>
</template>

<style scoped>
.terminal-split-drop { position: absolute; inset: 0; display: grid; grid-template: inherit; pointer-events: none; z-index: 6; }
.terminal-split-target { display: grid; place-items: center; outline: 1px dashed var(--blue); outline-offset: -4px; }
.terminal-split-target.first { grid-area: first; }
.terminal-split-target.second { grid-area: second; }
.terminal-split-hint { pointer-events: none; padding: 5px 8px; color: var(--blue); background: var(--background); border: 1px solid var(--border); border-radius: 5px; }
.terminal-split-preview { position: absolute; inset: 4px; display: grid; place-items: center; pointer-events: none; border: 2px solid var(--blue); border-radius: 5px; color: var(--blue); background: color-mix(in srgb, var(--highlight) 87%, transparent); }
.terminal-split-preview.left { right: 50%; }
.terminal-split-preview.right { left: 50%; }
.terminal-split-preview.top { bottom: 50%; }
.terminal-split-preview.bottom { top: 50%; }
</style>
