<template>
  <div class="rungraph">
    <div ref="container" class="rungraph-svg" />
    <div v-if="branch" class="rungraph-branches">
      <div class="rungraph-branch">
        <el-tag size="small" type="primary">正向分支（1 条告警）</el-tag>
        <span class="rungraph-branch-nodes mono">{{ branch.judge.join(' → ') }}</span>
      </div>
      <div class="rungraph-branch">
        <el-tag size="small" type="danger">风暴分支（N 条告警）</el-tag>
        <span class="rungraph-branch-nodes mono">{{ branch.storm.join(' → ') }}</span>
      </div>
      <div class="muted rungraph-note">同一张合图：入口按告警条数条件路由，conclude 与飞轮回写两分支共享。</div>
    </div>
    <el-empty v-if="error" :description="error" :image-size="48" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import mermaid from 'mermaid'

// LangGraph 合图可视化：后端 draw_mermaid 输出整图 + 两分支节点序
const branch = ref<{ judge: string[]; storm: string[] } | null>(null)
const container = ref<HTMLElement | null>(null)
const error = ref('')
let renderId = 0

mermaid.initialize({ startOnLoad: false, theme: 'default', flowchart: { curve: 'basis' } })

onMounted(async () => {
  try {
    const resp = await fetch('/api/diagnosis/graph?which=judge')
    if (!resp.ok) throw new Error(`运行图加载失败（HTTP ${resp.status}）`)
    const data = await resp.json()
    if (!data.mermaid) throw new Error('mermaid 生成失败（后端降级为节点清单）')
    renderId += 1
    const { svg } = await mermaid.render(`rungraph-${renderId}`, data.mermaid)
    if (container.value) container.value.innerHTML = svg
    error.value = ''
    // 合图两分支节点序（storm 再取一次只为分支字段；图本体同一张）
    const resp2 = await fetch('/api/diagnosis/graph?which=storm')
    if (resp2.ok) {
      const d2 = await resp2.json()
      branch.value = { judge: data.branch ?? [], storm: d2.branch ?? [] }
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
})
</script>

<style scoped>
.rungraph { display: flex; flex-direction: column; gap: 8px; }
.rungraph-svg { border: 1px solid #e4e9f0; border-radius: 8px; background: #fafbfd; overflow-x: auto; }
.rungraph-svg :deep(svg) { max-width: 100%; height: auto; }
.rungraph-branches { display: flex; flex-direction: column; gap: 4px; }
.rungraph-branch { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.rungraph-branch-nodes { font-size: 11px; color: #4a5568; }
.rungraph-note { font-size: 11px; }
</style>
