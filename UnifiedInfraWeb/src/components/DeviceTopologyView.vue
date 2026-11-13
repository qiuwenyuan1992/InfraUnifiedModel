<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import type { EdgeType, TopologyEdge, TopologyGraph, TopologyNode, TopologySelection } from '../api/topologyTypes'
import { buildDeviceView, type DeviceViewNode } from '../services/deviceView'
import { edgeColors, edgeLabels, entityLabels, planeLabel, relationLabels } from '../services/topology'

const props = defineProps<{ graph: TopologyGraph; rootId: string; selection: TopologySelection | null }>()
const emit = defineEmits<{ select: [selection: TopologySelection] }>()
const svg = ref<SVGSVGElement>()
const markerId = `device-arrow-${useId()}`
const layout = computed(() => buildDeviceView(props.graph, props.rootId))
const filters = ref<Record<EdgeType, boolean>>({ power_relation: true, network_relation: true, spatial_relation: true, composition_relation: true })
const domains: EdgeType[] = ['power_relation', 'network_relation', 'spatial_relation', 'composition_relation']
const gpuCount = computed(() => layout.value.nodes.filter(item => item.group === 'gpu').length)
const networkCount = computed(() => layout.value.edges.filter(item => ['server_uplink', 'gpu_uplink'].includes(item.edge.relation_kind) && [props.rootId, ...layout.value.nodes.filter(node => node.group === 'gpu').map(node => node.node.id)].includes(item.edge.from)).length)
function nodeVisible(item: DeviceViewNode) {
  if (item.node.id === props.rootId) return true
  if (item.node.entity_type === 'cabinet') return filters.value.spatial_relation || filters.value.power_relation
  if (item.group === 'gpu') return filters.value.composition_relation
  if (item.group === 'power') return filters.value.power_relation
  if (item.group === 'interface' || item.group === 'network') return filters.value.network_relation
  if (['data_center', 'room', 'pod'].includes(item.node.entity_type)) return filters.value.spatial_relation
  return true
}
const visibleNodes = computed(() => {
  const ids = new Set(layout.value.nodes.filter(nodeVisible).map(item => item.node.id))
  // 保留可见子节点的所有空间/组成容器，不把子节点变成悬空硬件。
  let changed = true
  while (changed) {
    changed = false
    for (const item of layout.value.edges.filter(item => item.contained)) {
      const child = item.edge.relation_kind === 'contains_gpu' || item.edge.relation_kind === 'owns_interface' ? item.edge.to : item.edge.from
      const parent = child === item.edge.from ? item.edge.to : item.edge.from
      if (ids.has(child) && !ids.has(parent)) {ids.add(parent); changed = true}
    }
  }
  return layout.value.nodes.filter(item => ids.has(item.node.id))
})
const visibleContainers = computed(() => {
  const ids = new Set(visibleNodes.value.map(item => item.node.id))
  return layout.value.containers.filter(item => ids.has(item.id)).sort((a, b) => a.depth - b.depth)
})
const containmentRelations = computed(() => {
  const ids = new Set(visibleNodes.value.map(item => item.node.id))
  return layout.value.edges.filter(item => (item.contained || item.edge.edge_type === 'spatial_relation' || item.edge.edge_type === 'composition_relation') && filters.value[item.edge.edge_type] && ids.has(item.edge.from) && ids.has(item.edge.to))
})
const visibleEdges = computed(() => {
  const ids = new Set(visibleNodes.value.map(item => item.node.id))
  return layout.value.edges.filter(item => !item.contained && (item.edge.edge_type === 'network_relation' || item.edge.edge_type === 'power_relation') && filters.value[item.edge.edge_type] && ids.has(item.edge.from) && ids.has(item.edge.to))
})
const selectionIds = computed(() => {
  if (!props.selection) return new Set<string>()
  if (props.selection.kind === 'node') return new Set([props.selection.id])
  const edge = props.graph.edges.find(edge => edge.id === props.selection!.id)
  return new Set(edge ? [edge.from, edge.to] : [])
})
function edgeHighlighted(edge: TopologyEdge) { return selectionIds.value.has(edge.from) || selectionIds.value.has(edge.to) }
function nodeHighlighted(id: string) { return selectionIds.value.has(id) || props.graph.edges.some(edge => edgeHighlighted(edge) && (edge.from === id || edge.to === id)) }
function edgeLabel(edge: TopologyEdge) {
  const label = edge.edge_type === 'power_relation' && edge.relation_kind === 'member_of' ? 'UPS 成员' : relationLabels[edge.relation_kind] || edge.relation_kind
  const path = edge.properties.power_path, plane = edge.properties.plane
  return [label, path !== undefined && path !== null ? `路径 ${path}` : '', plane !== undefined && plane !== null ? planeLabel(plane) : ''].filter(Boolean).join(' · ')
}
function model(node: TopologyNode) {
  const value = node.properties.model || node.properties.model_name || node.properties.device_model || node.properties.parts_model
  return typeof value === 'string' || typeof value === 'number' ? String(value) : '型号未提供'
}
function shorten(text: string, length = 23) { return text.length > length ? `${text.slice(0, length - 1)}…` : text }

const zoomLevel = ref(1)
const camera = ref({ x: 0, y: 0 })
const viewBox = computed(() => `${camera.value.x} ${camera.value.y} ${layout.value.width / zoomLevel.value} ${layout.value.height / zoomLevel.value}`)
const bounded = (value: number, extent: number) => Math.max(-extent, Math.min(extent, value))
function zoom(factor: number) {
  if (!Number.isFinite(factor) || factor <= 0) return
  const old = zoomLevel.value, next = Math.max(0.2, Math.min(4, old * factor))
  camera.value = { x: bounded(camera.value.x + layout.value.width / old / 2 - layout.value.width / next / 2, layout.value.width), y: bounded(camera.value.y + layout.value.height / old / 2 - layout.value.height / next / 2, layout.value.height) }
  zoomLevel.value = next
}
function onWheel(event: WheelEvent) { event.preventDefault(); if (Number.isFinite(event.deltaY)) zoom(event.deltaY < 0 ? 1.12 : 1 / 1.12) }
let pan: { pointerId: number; x: number; y: number; cameraX: number; cameraY: number; scale: number } | undefined
let dragged = false
function select(selection: TopologySelection) { if (!dragged) emit('select', selection) }
function selectKeyboard(selection: TopologySelection) { dragged = false; select(selection) }
function startPan(event: PointerEvent) {
  if (event.button !== 0 || pan) return
  dragged = false
  if ((event.target as Element | null)?.closest?.('[data-selectable]') || !svg.value) return
  const rect = svg.value.getBoundingClientRect()
  if (!rect.width || !rect.height) return
  event.preventDefault()
  pan = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, cameraX: camera.value.x, cameraY: camera.value.y, scale: Math.max(layout.value.width / rect.width, layout.value.height / rect.height) / zoomLevel.value }
  svg.value.setPointerCapture(event.pointerId)
}
function movePan(event: PointerEvent) {
  if (!pan || event.pointerId !== pan.pointerId) return
  const dx = event.clientX - pan.x, dy = event.clientY - pan.y
  if (Math.hypot(dx, dy) < 4 && !dragged) return
  dragged = true
  camera.value = { x: bounded(pan.cameraX - dx * pan.scale, layout.value.width), y: bounded(pan.cameraY - dy * pan.scale, layout.value.height) }
}
function endPan(event?: PointerEvent) {
  if (!pan || event && event.pointerId !== pan.pointerId) return
  const id = pan.pointerId
  pan = undefined
  if (svg.value?.hasPointerCapture(id)) svg.value.releasePointerCapture(id)
}
function fit() { endPan(); dragged = false; zoomLevel.value = 1; camera.value = { x: 0, y: 0 } }
watch(() => props.rootId, fit)
onBeforeUnmount(() => endPan())
</script>

<template>
  <section class="device-view" aria-label="设备拓扑视图">
    <div class="device-toolbar">
      <div class="device-filters" aria-label="显示关系领域">
        <label v-for="domain in domains" :key="domain"><input v-model="filters[domain]" type="checkbox" /><i :style="{ background: edgeColors[domain] }" />{{ edgeLabels[domain] }}</label>
      </div>
      <div class="device-controls"><button aria-label="放大设备画布" @click="zoom(1.25)">放大</button><button aria-label="缩小设备画布" @click="zoom(0.8)">缩小</button><button @click="fit">适应画布</button><span>{{ Math.round(zoomLevel * 100) }}%</span></div>
    </div>
    <div class="device-stage">
      <svg ref="svg" class="device-svg" :viewBox="viewBox" role="group" aria-label="真实设备拓扑，可在空白处拖拽、滚轮缩放，选择节点或关系查看详情"
        @wheel.prevent="onWheel" @pointerdown="startPan" @pointermove="movePan" @pointerup="endPan" @pointercancel="endPan" @lostpointercapture="endPan">
        <defs>
          <marker :id="markerId" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="context-stroke" /></marker>
          <clipPath v-for="(item, index) in visibleNodes" :id="`${markerId}-card-${index}`" :key="item.node.id"><rect x="8" y="8" :width="item.width - 16" :height="item.height - 16" /></clipPath>
        </defs>
        <template v-if="layout.nodes.length">
          <g class="device-containers">
            <rect v-for="item in visibleContainers" :key="item.id" :data-container-id="item.id" :x="item.x" :y="item.y" :width="item.width" :height="item.height" rx="14"
              :class="['containment-container', item.group, { selected: selectionIds.has(item.id) }]" />
          </g>
          <g class="device-edges">
            <g v-for="item in visibleEdges" :key="item.edge.id" data-selectable="edge" :data-edge-id="item.edge.id" role="button" tabindex="0" :aria-label="`${edgeLabel(item.edge)}：${item.edge.from} → ${item.edge.to}`"
              :class="{ highlighted: edgeHighlighted(item.edge), muted: selection && !edgeHighlighted(item.edge) }"
              @click.stop="select({ kind: 'edge', id: item.edge.id })" @keydown.enter.prevent="selectKeyboard({ kind: 'edge', id: item.edge.id })" @keydown.space.prevent="selectKeyboard({ kind: 'edge', id: item.edge.id })">
              <title>{{ edgeLabel(item.edge) }} · {{ item.edge.id }} · {{ item.edge.from }} → {{ item.edge.to }}</title>
              <path class="edge-hit" :d="item.path" />
              <path class="edge-line" :d="item.path" :stroke="edgeColors[item.edge.edge_type]" :stroke-dasharray="item.edge.relation_kind === 'has_standby' ? '7 5' : undefined" :marker-end="`url(#${markerId})`" />
              <text class="edge-label" :x="item.labelX" :y="item.labelY" text-anchor="middle">{{ edgeLabel(item.edge) }}</text>
            </g>
          </g>
          <g v-for="(item, index) in visibleNodes" :key="item.node.id" data-selectable="node" :data-node-id="item.node.id" :data-group="item.group" :transform="`translate(${item.x} ${item.y})`" role="button" tabindex="0"
            :aria-label="`${entityLabels[item.node.entity_type]} ${item.node.label} ${item.node.stable_id} ${model(item.node)}`" class="device-node" :class="[item.group, { 'idc-metadata': ['data_center', 'room'].includes(item.node.entity_type), highlighted: nodeHighlighted(item.node.id), selected: selection?.kind === 'node' && selection.id === item.node.id, muted: selection && !nodeHighlighted(item.node.id) }]"
            @click.stop="select({ kind: 'node', id: item.node.id })" @keydown.enter.prevent="selectKeyboard({ kind: 'node', id: item.node.id })" @keydown.space.prevent="selectKeyboard({ kind: 'node', id: item.node.id })">
            <title>{{ entityLabels[item.node.entity_type] }} · {{ item.node.label }} · {{ item.node.stable_id }} · {{ model(item.node) }}</title>
            <rect v-if="!['data_center', 'room'].includes(item.node.entity_type)" :width="item.width" :height="item.height" rx="10" />
            <g :clip-path="`url(#${markerId}-card-${index})`">
              <text class="node-type" x="12" y="20">{{ entityLabels[item.node.entity_type] }}{{ item.group === 'server' ? ' / SERVER' : '' }}</text>
              <text class="node-name" x="12" y="42">{{ shorten(item.node.label || item.node.stable_id, item.group === 'server' ? 52 : 22) }}</text>
              <text class="node-identity" x="12" y="63">{{ item.node.entity_type === 'device' || item.node.entity_type === 'gpu' ? 'SN' : 'ID' }} {{ shorten(item.node.stable_id, item.group === 'server' ? 50 : 20) }}</text>
              <text v-if="item.node.entity_type === 'gpu' || item.node.entity_type === 'device'" class="node-model" x="12" y="83">{{ shorten(model(item.node), item.group === 'server' ? 52 : 23) }}</text>
            </g>
          </g>
          <text v-if="!gpuCount" class="empty-label" :x="layout.container.x + 24" :y="layout.container.y + 122">GPU 0 · 未发现实际包含关系</text>
          <text v-if="!networkCount" class="empty-label" :x="layout.container.x + 24" :y="layout.container.y + 144">未发现真实网络上联关系</text>
        </template>
        <text v-else class="empty-label" x="60" y="80">根设备不在当前图中，暂无设备视图</text>
      </svg>
    </div>
    <div v-if="layout.warnings.length" class="containment-warnings" role="status"><span v-for="warning in layout.warnings" :key="warning">{{ warning }}</span></div>
    <details v-if="containmentRelations.length" class="containment-details">
      <summary>归属与标签关系 {{ containmentRelations.length }} 条（容器或背景元数据表达，不画连线）</summary>
      <button v-for="item in containmentRelations" :key="item.edge.id" :data-contained-edge-id="item.edge.id" @click="selectKeyboard({ kind: 'edge', id: item.edge.id })">
        {{ edgeLabel(item.edge) }} · {{ graph.nodes.find(node => node.id === item.edge.from)?.stable_id }} → {{ graph.nodes.find(node => node.id === item.edge.to)?.stable_id }}
      </button>
    </details>
    <div class="device-legend"><span>IDC以顶部文字展示，不画框；机柜 / 设备按实际归属嵌套，GPU、端口和UPS成员横向排列</span><span>仅网络和供电依赖绘制箭头，A/B分别保留</span><span>POD按成员或标签展示，不推导空间层级</span></div>
  </section>
</template>

<style scoped>
.device-view { min-width: 0; width: 100%; border: 1px solid #d8e2ef; border-radius: 10px; overflow: hidden; background: #f6f9fe; }
.device-toolbar, .device-controls, .device-filters { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; }
.device-toolbar { justify-content: space-between; padding: 10px 12px; color: #354963; font-size: 12px; }
.device-filters label { display: flex; align-items: center; gap: 5px; cursor: pointer; }
.device-filters input { accent-color: #3479df; margin: 0; }
.device-filters i { width: 10px; height: 10px; border-radius: 50%; }
.device-controls { gap: 6px; }
.device-controls button { border: 1px solid #cddbec; border-radius: 5px; background: white; color: #354963; padding: 4px 9px; cursor: pointer; }
.device-controls button:hover { border-color: #3479df; color: #3479df; }
.device-stage { overflow: hidden; height: clamp(400px, 65vh, 720px); background: #14243b; }
.device-svg { width: 100%; height: 100%; display: block; touch-action: none; user-select: none; cursor: grab; }
.device-svg:active { cursor: grabbing; }
.containment-container { fill: #1c304a; stroke: #7791ae; stroke-width: 2; pointer-events: none; }
.containment-container.server { fill: #243f5c; stroke: #75a6e2; }
.containment-container.network { fill: #1a3d49; stroke: #6bb3ba; }
.containment-container.power { fill: #3b3427; stroke: #d2ab62; }
.containment-container.selected { stroke-width: 4; stroke: #60a5ff; }
.containment-details { padding: 8px 12px; border-top: 1px solid #d8e2ef; color: #354963; font-size: 12px; }
.containment-details summary { cursor: pointer; }
.containment-details button { display: block; margin: 6px 0; background: #fff; border: 1px solid #d8e2ef; border-radius: 5px; padding: 5px 9px; color: #354963; cursor: pointer; text-align: left; }
.containment-warnings { display: flex; flex-direction: column; padding: 8px 12px; color: #946318; background: #fff5df; font-size: 12px; }
.section-label { fill: #c6daf3; font-size: 16px; font-weight: 600; pointer-events: none; }
.empty-label { fill: #bacce3; font-size: 16px; pointer-events: none; }
.device-node { cursor: pointer; }
.device-node rect { fill: #f2f6fd; stroke: #91b2dc; stroke-width: 2; }
.device-node.server rect { fill: #deebfc; stroke: #79aaf0; }
.device-node.gpu rect { fill: #e7f5f2; stroke: #72baae; }
.device-node.power rect { fill: #fff4dc; stroke: #d2ab62; }
.device-node.interface rect, .device-node.network rect { fill: #e5f4f8; stroke: #7bb5c3; }
.device-node.background rect { fill: #e9edf5; stroke: #99a8bf; }
.node-type { fill: #60738c; font-size: 11px; }
.node-name { fill: #213955; font-size: 14px; font-weight: 600; }
.node-identity { fill: #334e6b; font-size: 12px; }
.node-model { fill: #62768d; font-size: 11px; }
.idc-metadata .node-type, .idc-metadata .node-identity { fill: #bacce3; }
.idc-metadata .node-name { fill: #e7effb; }
.idc-metadata.highlighted text, .idc-metadata.selected text, .idc-metadata:focus text { fill: #60a5ff; }
.edge-line { fill: none; stroke-width: 2; }
.edge-hit { fill: none; stroke: transparent; stroke-width: 16; cursor: pointer; }
.edge-label { fill: #e7effb; font-size: 11px; paint-order: stroke; stroke: #14243b; stroke-width: 5px; stroke-linejoin: round; cursor: pointer; }
.highlighted .edge-line { stroke-width: 4; }
.device-node.highlighted rect, .device-node.selected rect { stroke: #60a5ff; stroke-width: 4; }
.device-node:focus rect { stroke: #60a5ff; stroke-width: 4; }
.device-edges g:focus .edge-line { stroke-width: 4; }
.muted { opacity: 0.45; }
.device-legend { display: flex; flex-wrap: wrap; gap: 6px 18px; color: #61758f; padding: 9px 12px; font-size: 11px; line-height: 1.5; }
@media (max-width: 640px) { .device-toolbar { align-items: flex-start; gap: 8px; } .device-stage { height: 440px; } }
</style>
