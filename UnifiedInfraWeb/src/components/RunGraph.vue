<template>
  <div class="rungraph">
    <div class="rungraph-tabs">
      <el-radio-group v-model="which" size="small">
        <el-radio-button value="judge">判定序（正向）</el-radio-button>
        <el-radio-button value="storm">风暴反推（反向）</el-radio-button>
      </el-radio-group>
    </div>
    <div ref="container" class="rungraph-svg" />
    <el-empty v-if="error" :description="error" :image-size="48" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import mermaid from 'mermaid'

// LangGraph 运行图：后端 draw_mermaid 输出 + 前端渲染（用户要看图的形状）
const which = ref<'judge' | 'storm'>('judge')
const container = ref<HTMLElement | null>(null)
const error = ref('')
let renderId = 0

mermaid.initialize({ startOnLoad: false, theme: 'default', flowchart: { curve: 'basis' } })

async function load() {
  try {
    const resp = await fetch(`/api/diagnosis/graph?which=${which.value}`)
    if (!resp.ok) throw new Error(`运行图加载失败（HTTP ${resp.status}）`)
    const data = await resp.json()
    if (!data.mermaid) throw new Error('mermaid 生成失败（后端降级为节点清单）')
    renderId += 1
    const { svg } = await mermaid.render(`rungraph-${renderId}`, data.mermaid)
    if (container.value) container.value.innerHTML = svg
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

onMounted(load)
watch(which, load)
</script>

<style scoped>
.rungraph { display: flex; flex-direction: column; gap: 8px; }
.rungraph-tabs { display: flex; }
.rungraph-svg { border: 1px solid #e4e9f0; border-radius: 8px; background: #fafbfd; overflow-x: auto; }
.rungraph-svg :deep(svg) { max-width: 100%; height: auto; }
</style>
