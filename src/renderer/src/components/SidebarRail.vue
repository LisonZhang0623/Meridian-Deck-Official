<script setup lang="ts">
import { nextTick } from 'vue'
import { Server, FolderTree, Cpu, SquareTerminal, FileClock, Cable, Activity, Sun, Moon, Settings } from '@lucide/vue'
import { state, setLayout, attempt } from '../workbench'
import type { SidebarPanel } from '../../../shared/types'

const props = defineProps<{ dark?: boolean; hasUpdate?: boolean }>()
const emit = defineEmits<{ openSettings: [] }>()

const corePanels = [
  { id: 'servers' as const, label: '服务器', icon: Server },
  { id: 'files' as const, label: '文件', icon: FolderTree },
  { id: 'commands' as const, label: '命令', icon: SquareTerminal }
]

const opsPanels = [
  { id: 'gpu' as const, label: '显卡', icon: Cpu },
  { id: 'resources' as const, label: '资源', icon: Activity },
  { id: 'logs' as const, label: '日志', icon: FileClock },
  { id: 'forwards' as const, label: '转发', icon: Cable }
]

function expanded(panel: SidebarPanel): boolean {
  return !state.settings.sidebarCollapsed && state.settings.sidebarPanel === panel
}

async function toggle(panel: SidebarPanel, event: MouseEvent): Promise<void> {
  const collapse = expanded(panel)
  void attempt(() => setLayout({ sidebarPanel: panel, sidebarCollapsed: collapse }))
  if (!collapse && event.detail === 0) {
    await nextTick()
    const target = document.getElementById('sidebar-panel-' + panel)
    if (target) { target.tabIndex = -1; target.focus() }
  }
}

function toggleTheme(): void {
  const next = props.dark ? 'light' : 'dark'
  void attempt(() => setLayout({ theme: next }))
}
</script>

<template>
  <nav class="sidebar-rail" aria-label="侧栏面板">
    <div class="sidebar-rail-group">
      <button v-for="panel in corePanels" :id="'sidebar-toggle-' + panel.id" :key="panel.id"
        class="sidebar-rail-button" type="button" :class="{ active: expanded(panel.id) }" :aria-label="panel.label"
        :aria-expanded="expanded(panel.id)" :aria-controls="'sidebar-panel-' + panel.id"
        :title="(expanded(panel.id) ? '收起' : '展开') + panel.label" @click="toggle(panel.id, $event)">
        <component :is="panel.icon" :size="17" aria-hidden="true" />
      </button>
    </div>

    <div class="sidebar-rail-divider" role="separator"></div>

    <div class="sidebar-rail-group">
      <button v-for="panel in opsPanels" :id="'sidebar-toggle-' + panel.id" :key="panel.id"
        class="sidebar-rail-button" type="button" :class="{ active: expanded(panel.id) }" :aria-label="panel.label"
        :aria-expanded="expanded(panel.id)" :aria-controls="'sidebar-panel-' + panel.id"
        :title="(expanded(panel.id) ? '收起' : '展开') + panel.label" @click="toggle(panel.id, $event)">
        <component :is="panel.icon" :size="17" aria-hidden="true" />
      </button>
    </div>

    <div class="sidebar-rail-footer">
      <button class="sidebar-rail-button" type="button" :aria-label="dark ? '切换为浅色主题' : '切换为深色主题'"
        :title="dark ? '切换为浅色主题' : '切换为深色主题'" @click="toggleTheme">
        <Sun v-if="dark" :size="16" aria-hidden="true" />
        <Moon v-else :size="16" aria-hidden="true" />
      </button>
      <button class="sidebar-rail-button" type="button" aria-label="首选项设置" title="首选项设置" @click="emit('openSettings')">
        <Settings :size="16" aria-hidden="true" />
        <span v-if="hasUpdate" class="rail-update-dot" aria-hidden="true"></span>
      </button>
    </div>
  </nav>
</template>
