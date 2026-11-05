<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { inventoryApi } from '../api/inventory'
import type { Diagnostic, DiagnosticQuery, Source } from '../api/types'
import { useCursorPage } from '../composables/useCursorPage'
import { formatTime } from '../services/display'

const props = defineProps<{ target: { id: string; isRound: boolean; sourceId: string }; sources: Source[] }>()
const emit = defineEmits<{ close: [] }>()
const sourceId = ref('')
const limit = ref(50)
const filters = computed(() => ({ source_id: sourceId.value, limit: limit.value }))
const { items, loading, error, history, nextCursor, load, next, previous } = useCursorPage<Diagnostic, DiagnosticQuery>(
  (query, signal) => inventoryApi.diagnostics(props.target.id, query, props.target.isRound, signal),
)
watch([() => props.target.id, () => props.target.isRound], () => {
  sourceId.value = ''
  void load(filters.value, true)
}, { immediate: true })
watch(filters, () => { void load(filters.value, true) })
</script>

<template>
  <el-drawer :model-value="true" :title="target.isRound ? '整轮同步诊断' : '阶段任务诊断'" size="min(1120px, 98vw)" append-to-body @close="emit('close')">
    <p class="mono muted break">{{ target.id }}</p>
    <el-alert title="诊断仅展示脱敏对象标识、字段路径、级别与错误码；不包含来源凭据或内部错误原文。" type="info" :closable="false" show-icon />
    <div class="filters spaced">
      <el-select v-model="sourceId" clearable filterable placeholder="全部来源" aria-label="诊断来源筛选">
        <el-option v-for="source in sources" :key="source.id" :label="source.name" :value="source.id" />
        <el-option v-if="!sources.some(source => source.id === target.sourceId)" :label="target.sourceId" :value="target.sourceId" />
      </el-select>
      <el-select v-model="limit" aria-label="诊断每页条数"><el-option v-for="size in [20, 50, 100, 200]" :key="size" :label="`每页 ${size} 条`" :value="size" /></el-select>
      <el-button :loading="loading" @click="load(filters)">刷新诊断</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="spaced" />
    <el-table v-loading="loading" :data="items" class="spaced" border>
      <el-table-column label="级别" width="90"><template #default="{ row }"><el-tag :type="row.severity === 'error' ? 'danger' : row.severity === 'warning' ? 'warning' : 'info'">{{ row.severity }}</el-tag></template></el-table-column>
      <el-table-column prop="code" label="错误码" min-width="170" />
      <el-table-column prop="resource" label="资源" min-width="140" />
      <el-table-column prop="object_ref" label="对象标识" min-width="180" show-overflow-tooltip />
      <el-table-column prop="field_path" label="字段路径" min-width="150" show-overflow-tooltip />
      <el-table-column prop="run_id" label="子任务 ID" min-width="160" show-overflow-tooltip />
      <el-table-column label="时间" min-width="160"><template #default="{ row }">{{ formatTime(row.created_at) }}</template></el-table-column>
      <template #empty><el-empty :description="error ? '诊断加载失败，请重试' : loading ? '正在读取诊断' : '当前条件下暂无诊断'" :image-size="70" /></template>
    </el-table>
    <div class="pagination"><span class="muted">第 {{ history.length + 1 }} 页 · 本页 {{ items.length }} 条</span><el-button :disabled="!history.length || loading" @click="previous(filters)">上一页</el-button><el-button :disabled="!nextCursor || loading" @click="next(filters)">下一页</el-button></div>
  </el-drawer>
</template>
