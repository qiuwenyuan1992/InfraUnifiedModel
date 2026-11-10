<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { SpatialSceneLayout } from '../services/spatialScene'
import { createSpatialThreeRenderer, networkRoleColor } from '../services/spatialThreeRenderer'

const props = defineProps<{ layout: SpatialSceneLayout; selection: string | null; mode: 'space' | 'network' }>()
const emit = defineEmits<{ select: [id: string] }>()
const container = ref<HTMLDivElement>()
const unavailable = ref(false)
let renderer: ReturnType<typeof createSpatialThreeRenderer> | undefined
const modeLabel = computed(() => props.mode === 'space' ? '机柜空间 · 真实 3D' : '网络角色分层 · 真实 3D')
const legend = computed(() => props.mode === 'network'
  ? [{ label: 'T2', color: networkRoleColor('T2') }, { label: 'T1', color: networkRoleColor('T1') }, { label: 'T0', color: networkRoleColor('T0') }, { label: '未知角色', color: networkRoleColor('unknown') }, { label: '设备', color: '#4396e8' }, { label: 'GPU', color: '#4ade80' }]
  : [{ label: '机柜', color: '#72baff' }, { label: '设备', color: '#4396e8' }, { label: '网络设备（按角色着色）', color: '#2dd4bf' }, { label: 'GPU', color: '#4ade80' }])
function update() {
  if (!renderer) return
  try {
    renderer.update(props.layout, props.mode)
    renderer.select(props.selection)
  } catch {
    renderer.dispose()
    renderer = undefined
    unavailable.value = true
  }
}
watch([() => props.layout, () => props.mode], update)
watch(() => props.selection, id => renderer?.select(id))
onMounted(() => {
  if (!container.value) return
  try {
    renderer = createSpatialThreeRenderer(container.value, id => emit('select', id))
    update()
  } catch {
    unavailable.value = true
  }
})
onBeforeUnmount(() => { renderer?.dispose(); renderer = undefined })
</script>

<template>
  <section class="spatial-three-shell" :aria-label="modeLabel">
    <div class="spatial-three-toolbar">
      <strong>{{ modeLabel }}</strong>
      <div class="spatial-three-controls">
        <button type="button" :disabled="unavailable" aria-label="放大 3D 画布" @click="renderer?.zoom(1.25)">放大</button>
        <button type="button" :disabled="unavailable" aria-label="缩小 3D 画布" @click="renderer?.zoom(0.8)">缩小</button>
        <button type="button" :disabled="unavailable" @click="renderer?.reset()">重置视角</button>
      </div>
    </div>
    <div ref="container" class="spatial-three-viewport" role="img" :aria-label="`${modeLabel}，拖拽旋转，右键拖拽平移，滚轮缩放，点击选择真实节点`" />
    <div v-if="unavailable" class="spatial-three-message" role="status">当前浏览器无法初始化 WebGL 3D 画布，请使用支持 WebGL 的浏览器并启用硬件加速。</div>
    <div v-else-if="layout.placements.length === 0" class="spatial-three-message" role="status">当前范围没有可展示的空间节点。</div>
    <div class="spatial-three-footer">
      <div class="spatial-three-legend" aria-label="3D 画布图例">
        <span v-for="item in legend" :key="item.label"><i :style="{ background: item.color }" />{{ item.label }}</span>
      </div>
      <span class="spatial-three-help">拖拽旋转 · 右键平移 · 滚轮缩放 · 点击选择</span>
    </div>
  </section>
</template>

<style scoped>
.spatial-three-shell { position: relative; min-height: 520px; overflow: hidden; border: 1px solid #28496b; border-radius: 12px; background: #0b1527; color: #d7e9ff; }
.spatial-three-toolbar { position: absolute; inset: 16px 16px auto; z-index: 2; display: flex; align-items: center; justify-content: space-between; gap: 12px; pointer-events: none; }
.spatial-three-toolbar strong { font-size: 14px; font-weight: 600; text-shadow: 0 1px 8px #0b1527; }
.spatial-three-controls { display: flex; gap: 6px; pointer-events: auto; }
.spatial-three-controls button { border: 1px solid #365779; border-radius: 6px; padding: 6px 10px; background: #14263f; color: #d7e9ff; font: inherit; font-size: 12px; cursor: pointer; }
.spatial-three-controls button:hover { background: #254568; }
.spatial-three-controls button:focus-visible { outline: 2px solid #76baff; outline-offset: 2px; }
.spatial-three-controls button:disabled { opacity: 0.45; cursor: default; }
.spatial-three-viewport { height: clamp(520px, 65vh, 800px); width: 100%; touch-action: none; }
.spatial-three-viewport :deep(canvas) { display: block; width: 100%; height: 100%; }
.spatial-three-message { position: absolute; inset: 35% 8% auto; text-align: center; color: #b7c9df; pointer-events: none; }
.spatial-three-footer { position: absolute; inset: auto 16px 14px; display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 12px; font-size: 11px; pointer-events: none; }
.spatial-three-legend { display: flex; flex-wrap: wrap; gap: 12px; }
.spatial-three-legend span { display: inline-flex; align-items: center; gap: 5px; }
.spatial-three-legend i { display: inline-block; width: 8px; height: 8px; border-radius: 2px; }
.spatial-three-help { color: #8da8c7; }
@media (max-width: 640px) { .spatial-three-toolbar { flex-wrap: wrap; } .spatial-three-help { display: none; } }
</style>
