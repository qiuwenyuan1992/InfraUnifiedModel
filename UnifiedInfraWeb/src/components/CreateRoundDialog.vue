<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { inventoryApi } from '../api/inventory'
import type { Round, Source } from '../api/types'
import { errorText } from '../services/display'

const props = defineProps<{ modelValue: boolean; sources: Source[] }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; created: [round: Round] }>()
const sourceId = ref('')
const key = ref('')
const loading = ref(false)
const error = ref('')
const sourceEnabled = computed(() => props.sources.some(source => source.id === sourceId.value && source.enabled))
watch(sourceEnabled, enabled => { if (!enabled && !loading.value) sourceId.value = '' })
watch(() => props.modelValue, open => { if (open && !sourceEnabled.value) sourceId.value = '' })
watch(sourceId, () => { key.value = ''; error.value = '' })
async function submit() {
  if (!sourceEnabled.value || loading.value) return
  if (!key.value) key.value = crypto.randomUUID()
  loading.value = true
  error.value = ''
  try {
    const round = await inventoryApi.create(sourceId.value, key.value)
    key.value = ''
    emit('update:modelValue', false)
    emit('created', round)
    ElMessage.success('完整同步任务已入队')
  } catch (cause) { error.value = errorText(cause) }
  finally { loading.value = false }
}
</script>

<template>
  <el-dialog :model-value="props.modelValue" title="创建完整同步任务" width="min(560px, 94vw)"
    :close-on-click-modal="false" :close-on-press-escape="!loading" :show-close="!loading"
    @update:model-value="emit('update:modelValue', $event)">
    <p class="dialog-intro">选择已启用的数据来源，手动入队一轮完整同步，阶段总数以任务 API 返回值为准。</p>
    <el-form label-position="top" @submit.prevent="submit">
      <el-form-item label="数据来源" required>
        <el-select v-model="sourceId" placeholder="请选择已启用来源" filterable :disabled="loading" style="width: 100%">
          <el-option v-for="source in sources" :key="source.id" :value="source.id"
            :label="source.name" :disabled="!source.enabled" />
        </el-select>
      </el-form-item>
      <el-form-item label="同步模式"><el-tag>完整同步 · full</el-tag></el-form-item>
      <el-alert title="完整同步仅覆盖该来源当前配置范围，不代表所有数据中心。提交后由独立 Worker 执行，不自动清理历史图数据。"
        type="info" :closable="false" show-icon />
      <el-alert v-if="error" class="spaced" :title="error" type="error" :closable="false" show-icon />
      <p v-if="error" class="muted">网络失败时直接重试会沿用本次幂等键，避免重复入队；切换来源视为新请求。</p>
    </el-form>
    <template #footer>
      <el-button :disabled="loading" @click="emit('update:modelValue', false)">暂不创建</el-button>
      <el-button type="primary" :loading="loading" :disabled="!sourceEnabled" @click="submit">{{ error ? '重试提交' : '确认创建' }}</el-button>
    </template>
  </el-dialog>
</template>
