<script setup lang="ts">
import { Upload, Download, X, RotateCw, Check, Pause, Play, Trash2 } from '@lucide/vue'
import type { Transfer } from '../../../shared/types'
import { active, state, attempt, unwrap, sizeLabel, retryTransfer, dismissCompletedTransfers } from '../workbench'

const api = window.sshTool
const labels = { queued: '排队中', running: '传输中', completed: '已完成', failed: '失败', cancelled: '已取消', paused: '已暂停' }
const purposes = { drag: ' · 拖出准备', directory: ' · 文件夹', sync: ' · 目录同步', nas: ' · NAS' }

function connectedSession(transfer: Transfer): string {
  const same = state.sessions.find(session => session.id === transfer.sessionId && session.phase === 'connected')
  return same?.id || (active.value?.phase === 'connected' ? active.value.id : '')
}

async function resume(transfer: Transfer): Promise<void> {
  if (!api.resumeTransfer) return
  const session = connectedSession(transfer)
  if (!session) throw new Error('请先重新连接原服务器，再继续传输')
  unwrap(await api.resumeTransfer(transfer.id, session))
}

async function discard(transfer: Transfer): Promise<void> {
  if (!api.discardTransferPartial) return
  unwrap(await api.discardTransferPartial(transfer.id, connectedSession(transfer)))
}
</script>

<template>
  <section v-if="state.transferOpen" class="transfers-panel" aria-label="传输任务">
    <div class="transfers-header">
      <span>传输任务 <span class="count">{{ state.transfers.length }}</span></span>
      <div class="transfers-header-actions">
        <button v-if="state.transfers.some(transfer => transfer.status === 'completed')" type="button" class="icon-button transfer-clear" @click="dismissCompletedTransfers()"><Trash2 :size="13"/>清除已完成</button>
        <button type="button" class="icon-button" aria-label="收起传输任务" title="收起传输任务" @click="state.transferOpen = false"><X :size="14"/></button>
      </div>
    </div>
    <div class="transfers-list">
      <div v-if="!state.transfers.length" class="transfer-empty">上传或下载文件后，可在这里查看进度。</div>
      <div v-for="transfer in state.transfers" :key="transfer.id" class="transfer-row">
        <component :is="transfer.direction === 'upload' ? Upload : Download" :size="15" class="muted"/>
        <div class="transfer-detail">
          <div class="transfer-name">
            <strong>{{ transfer.name }}{{ transfer.purpose ? purposes[transfer.purpose] : '' }}</strong>
            <span class="status-pill" :class="transfer.status === 'completed' ? 'ok' : transfer.status === 'failed' ? 'err' : transfer.status === 'running' ? 'info' : 'neutral'">{{ labels[transfer.status] }}</span>
          </div>
          <div class="progress-track"><span :style="{ width: (transfer.size ? Math.min(100, transfer.transferred / transfer.size * 100) : transfer.status === 'completed' ? 100 : 0) + '%' }"></span></div>
          <small v-if="transfer.error" class="danger">{{ transfer.error }}</small>
          <small v-else>
            {{ sizeLabel(transfer.transferred) }} / {{ sizeLabel(transfer.size) }}
            <template v-if="transfer.status === 'running'"> · {{ sizeLabel(transfer.speed) }}/s</template>
            <template v-else-if="transfer.resumable"> · 可续传</template>
          </small>
        </div>
        <div class="transfer-actions">
          <button v-if="transfer.status === 'running' && transfer.purpose !== 'drag' && api.pauseTransfer" class="icon-button" aria-label="暂停传输" title="暂停并保留断点" :disabled="!transfer.transferred" @click="attempt(async () => unwrap(await api.pauseTransfer!(transfer.id)))"><Pause :size="15"/></button>
          <button v-if="transfer.status === 'running' || transfer.status === 'queued'" class="icon-button" aria-label="取消传输" title="取消传输" @click="attempt(async () => unwrap(await api.cancelTransfer(transfer.id)))"><X :size="15"/></button>
          <template v-else-if="transfer.resumable && api.resumeTransfer">
            <button class="icon-button" aria-label="从断点继续" title="从断点继续" @click="attempt(() => resume(transfer))"><Play :size="15"/></button>
            <button v-if="api.discardTransferPartial" class="icon-button" aria-label="丢弃断点" title="丢弃断点" @click="attempt(() => discard(transfer))"><Trash2 :size="15"/></button>
          </template>
          <button v-else-if="transfer.status === 'failed' || transfer.status === 'cancelled'" class="icon-button" aria-label="重试" title="重试" @click="attempt(() => retryTransfer(transfer))"><RotateCw :size="15"/></button>
          <template v-else-if="transfer.status === 'completed'">
            <Check :size="15" class="success" aria-hidden="true"/>
            <button class="icon-button" :aria-label="`移除传输记录 ${transfer.name}`" title="移除传输记录" @click="dismissCompletedTransfers(transfer.id)"><Trash2 :size="15"/></button>
          </template>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.transfer-actions { display: flex; align-items: center; gap: 2px; }
.transfer-clear { padding: 2px 7px; font-size: 11px; min-height: 24px; }
</style>
