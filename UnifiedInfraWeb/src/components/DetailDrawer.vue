<script setup lang="ts">
import { computed } from 'vue'
import type { Round } from '../api/types'
import { formatTime, isActive, phaseNames, statuses, statusType } from '../services/display'

const props = defineProps<{ id: string; round: Round | null; loading: boolean; error: string; cancelLoading: boolean; sourceName: string }>()
const emit = defineEmits<{ close: []; refresh: []; cancel: [round: Round]; diagnostics: [id: string, isRound: boolean, sourceId: string] }>()
const percent = computed(() => props.round?.total_phases ? Math.round(props.round.completed_phases / props.round.total_phases * 100) : 0)
const phases = computed(() => [...(props.round?.phases || [])].sort((a, b) => a.position - b.position))
</script>

<template>
  <el-drawer :model-value="!!id" title="同步任务详情" size="min(1000px, 96vw)" @close="emit('close')">
    <div class="drawer-actions">
      <el-tag v-if="round" :type="statusType(round.status)">{{ statuses[round.status] || round.status }}</el-tag>
      <span class="muted">{{ round && isActive(round.status) ? '活动任务每 10 秒刷新' : '任务已停止自动刷新' }}</span>
      <el-button :loading="loading" @click="emit('refresh')">刷新详情</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="spaced" />
    <el-skeleton v-if="loading && !round" :rows="8" animated class="spaced" />
    <template v-if="round">
      <h2 class="drawer-heading">完整来源同步</h2>
      <div class="mono muted break">{{ round.id }}</div>
      <el-descriptions :column="2" border class="spaced">
        <el-descriptions-item label="数据来源">{{ sourceName }}</el-descriptions-item>
        <el-descriptions-item label="模式">{{ round.mode }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatTime(round.created_at) }}</el-descriptions-item>
        <el-descriptions-item label="共享快照">{{ formatTime(round.snapshot_at) }}</el-descriptions-item>
        <el-descriptions-item label="开始时间">{{ formatTime(round.started_at) }}</el-descriptions-item>
        <el-descriptions-item label="结束时间">{{ formatTime(round.finished_at) }}</el-descriptions-item>
      </el-descriptions>
      <el-alert v-if="round.error_code" :title="`任务错误码：${round.error_code}`" type="error" :closable="false" class="spaced" />
      <el-alert v-if="round.cancel_requested_at" :title="`取消请求时间：${formatTime(round.cancel_requested_at)}；不会回滚已写入数据。`"
        type="warning" :closable="false" class="spaced" />
      <section class="progress-section">
        <div class="section-heading"><h3>阶段进度</h3><strong>{{ round.completed_phases }} / {{ round.total_phases }} 成功</strong></div>
        <el-progress :percentage="percent" :status="round.status === 'succeeded' ? 'success' : undefined" :show-text="false" />
        <p class="muted">阶段完成比例，不是 CMDB 对象采集百分比。未完成阶段不展示尚未确定的计数。</p>
      </section>
      <el-table :data="phases" size="small" border>
        <el-table-column label="阶段" min-width="156">
          <template #default="{ row }"><strong>{{ row.position }}. {{ phaseNames[row.name] || row.name }}</strong><div class="mono muted">{{ row.name }}</div><div v-if="row.run_id" class="mono muted break" :title="`子任务 ID：${row.run_id}`">{{ row.run_id }}</div></template>
        </el-table-column>
        <el-table-column label="状态" width="96"><template #default="{ row }"><el-tag :type="statusType(row.status)" size="small">{{ statuses[row.status] || row.status }}</el-tag></template></el-table-column>
        <el-table-column label="节点 / 边 / 对象" min-width="124"><template #default="{ row }">{{ ['succeeded', 'failed', 'canceled'].includes(row.status) ? `${row.nodes} / ${row.edges} / ${row.objects}` : '—' }}</template></el-table-column>
        <el-table-column label="跳过 / 端点缺失" min-width="120"><template #default="{ row }">{{ ['succeeded', 'failed', 'canceled'].includes(row.status) ? `${row.skipped} / ${row.missing_endpoints}` : '—' }}</template></el-table-column>
        <el-table-column label="时间 / 错误" min-width="170"><template #default="{ row }"><div>{{ formatTime(row.started_at) }}</div><div class="muted">{{ formatTime(row.finished_at) }}</div><div class="error-code">{{ row.error_code }}</div></template></el-table-column>
        <el-table-column label="诊断" width="82"><template #default="{ row }"><el-button link type="primary" :disabled="!row.run_id" @click="emit('diagnostics', row.run_id, false, round.source_id)">查看</el-button></template></el-table-column>
      </el-table>
      <el-empty v-if="!phases.length" description="后端尚未返回阶段详情" :image-size="72" />
      <div class="drawer-actions spaced">
        <el-button type="primary" plain @click="emit('diagnostics', round.id, true, round.source_id)">查看整轮诊断</el-button>
        <el-button type="danger" plain :loading="cancelLoading" :disabled="!isActive(round.status) || !!round.cancel_requested_at" @click="emit('cancel', round)">{{ round.cancel_requested_at ? '已请求取消' : '取消任务' }}</el-button>
      </div>
    </template>
  </el-drawer>
</template>
