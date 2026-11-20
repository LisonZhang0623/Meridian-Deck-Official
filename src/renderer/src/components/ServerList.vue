<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Clock3, Search, Plug, Settings, Trash2, Tags, X } from '@lucide/vue'
import type { Profile } from '../../../shared/types'
import { connectionIdentity } from '../../../shared/connection'
import { state, selectedProfile, requestConnection, removeProfile, attempt } from '../workbench'
import { filterProfiles, profileTags } from '../server-profiles'
import '../server-list.css'

const query = ref(''), selectedTag = ref(''), recent = ref(false)
const tags = computed(() => profileTags(state.profiles))
const filtered = computed(() => filterProfiles(state.profiles, query.value, selectedTag.value, recent.value))
const recentCount = computed(() => state.profiles.filter(profile => !!profile.lastConnectedAt).length)
const groups = computed(() => recent.value
  ? [{ name: '按最近连接排序', profiles: filtered.value }]
  : [...new Set(filtered.value.map(profile => profile.group || '未分组'))].map(name => ({ name, profiles: filtered.value.filter(profile => (profile.group || '未分组') === name) })))
watch(tags, available => { if (selectedTag.value && !available.includes(selectedTag.value)) selectedTag.value = '' })
function chooseProfile(profile: Profile): void {
  const key = connectionIdentity(profile)
  state.selectedProfileId = profile.id
  state.activeId = state.sessions.find(session => session.connectionKey === key)?.id || ''
}
function isConnected(profile: Profile): boolean {
  const key = connectionIdentity(profile)
  return state.sessions.some(session => session.connectionKey === key && session.phase === 'connected')
}
function timeLabel(timestamp: number): string {
  return new Date(timestamp).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
const emptyMessage = computed(() => {
  if (query.value.trim() || selectedTag.value) return '没有匹配的服务器，试试更换搜索内容或标签。'
  return recent.value ? '还没有成功连接记录。连接成功后会在这里显示。' : '还没有连接，点击 + 添加。'
})
</script>

<template>
  <div class="server-browser">
    <div class="server-view-switch segmented" aria-label="服务器列表范围">
      <button :aria-pressed="!recent" :class="{ active: !recent }" @click="recent = false">全部服务器<span>{{ state.profiles.length }}</span></button>
      <button :aria-pressed="recent" :class="{ active: recent }" @click="recent = true"><Clock3 :size="12"/>最近连接<span>{{ recentCount }}</span></button>
    </div>
    <div class="server-search"><Search :size="14"/><input v-model="query" placeholder="搜索名称、地址或标签" aria-label="搜索服务器"><button v-if="query" class="icon-button" aria-label="清空服务器搜索" @click="query = ''"><X :size="12"/></button></div>
    <div v-if="tags.length" class="server-tag-filters" aria-label="按服务器标签筛选">
      <Tags :size="12" class="server-tags-icon"/>
      <button :class="{ active: !selectedTag }" :aria-pressed="!selectedTag" @click="selectedTag = ''">全部</button>
      <button v-for="tag in tags" :key="tag" :title="tag" :class="{ active: selectedTag === tag }" :aria-pressed="selectedTag === tag" @click="selectedTag = selectedTag === tag ? '' : tag">{{ tag }}</button>
    </div>
    <div class="server-list">
      <template v-if="filtered.length">
        <div v-for="group in groups" :key="group.name">
          <div class="group-title">{{ group.name }}</div>
          <div v-for="profile in group.profiles" :key="profile.id" class="server-list-item hover-row" :class="{ selected: selectedProfile?.id === profile.id }">
            <div class="server-row">
              <button class="server-select" @click="chooseProfile(profile)" @dblclick="requestConnection(profile)">
                <span class="connection-dot" :data-phase="isConnected(profile) ? 'connected' : 'disconnected'" aria-hidden="true"></span>
                <span><strong>{{ profile.name }}</strong><small :title="`${profile.username}@${profile.host}:${profile.port}`">{{ profile.username }}@{{ profile.host }}</small></span>
              </button>
              <div class="server-row-actions hover-actions">
                <button class="icon-button" :title="`新建 ${profile.name} 的终端`" :aria-label="`新建 ${profile.name} 的终端`" @click="requestConnection(profile)"><Plug :size="13"/></button>
                <button class="icon-button" :title="`编辑 ${profile.name}`" :aria-label="`编辑 ${profile.name}`" @click="state.connectionDialog = { profile }"><Settings :size="13"/></button>
                <button class="icon-button" :title="`删除 ${profile.name}`" :aria-label="`删除 ${profile.name}`" @click="attempt(() => removeProfile(profile))"><Trash2 :size="13"/></button>
              </div>
            </div>
            <div v-if="profile.tags?.length" class="server-profile-tags"><span v-for="tag in profile.tags" :key="tag" :title="tag">{{ tag }}</span></div>
            <div v-if="profile.lastConnectedAt" class="server-last-connected" :title="new Date(profile.lastConnectedAt).toLocaleString('zh-CN')"><Clock3 :size="10"/>上次连接 {{ timeLabel(profile.lastConnectedAt) }}</div>
          </div>
        </div>
      </template>
      <div v-else class="side-empty">{{ emptyMessage }}</div>
    </div>
  </div>
</template>
