<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import type { EdgeType, TopologyEdge, TopologyGraph, TopologyNode, TopologySelection } from '../api/topologyTypes'
import { buildDeviceView, deviceViewCollapseThreshold, type DeviceViewContainer, type DeviceViewNode } from '../services/deviceView'
import { edgeColors, edgeLabels, entityLabels, planeLabel, relationLabels } from '../services/topology'

const props = defineProps<{ graph: TopologyGraph; rootId: string; selection: TopologySelection | null }>()
const emit = defineEmits<{ select: [selection: TopologySelection] }>()
const svg = ref<SVGSVGElement>()
const markerId = `device-arrow-${useId()}`
// 语义聚合：超过阈值的容器默认折叠为摘要框（借鉴 gpu_topology），点击展开网格。
const collapsedIds = ref<ReadonlySet<string>>(new Set())
// 容器父子结构只与图数据相关、与折叠状态无关：单独一次布局派生，供折叠状态与隐藏子树复用。
const containment = computed(() => {
  const full = buildDeviceView(props.graph, props.rootId)
  const childMap = new Map<string, string[]>()
  const parentMap = new Map<string, string>()
  for (const container of full.containers) {
    childMap.set(container.id, container.childIds)
    for (const child of container.childIds) parentMap.set(child, container.id)
  }
  return { childMap, parentMap, containers: full.containers }
})
const layout = computed(() => buildDeviceView(props.graph, props.rootId, collapsedIds.value))
const rootAncestors = computed(() => {
  const ancestors = new Set<string>()
  let current = containment.value.parentMap.get(props.rootId)
  while (current !== undefined && !ancestors.has(current)) { ancestors.add(current); current = containment.value.parentMap.get(current) }
  return ancestors
})
const defaultCollapsed = computed(() => {
  // 子节点超过阈值的容器默认折叠；根设备及其祖先容器保持展开。
  const ancestors = rootAncestors.value
  const collapsed = new Set<string>()
  for (const container of containment.value.containers) {
    if (container.childCount > deviceViewCollapseThreshold && container.id !== props.rootId && !ancestors.has(container.id)) collapsed.add(container.id)
  }
  return collapsed
})
watch(() => [props.rootId, props.graph], () => { collapsedIds.value = defaultCollapsed.value }, { immediate: true })
const hiddenSubtree = computed(() => {
  // 仅折叠容器的后代子树；折叠容器自身由摘要框替代头部卡片。
  const hidden = new Set<string>()
  for (const id of collapsedIds.value) {
    const stack = [...(containment.value.childMap.get(id) || [])]
    while (stack.length) {
      const current = stack.pop()!
      if (hidden.has(current)) continue
      hidden.add(current)
      stack.push(...(containment.value.childMap.get(current) || []))
    }
  }
  return hidden
})
const hiddenIds = computed(() => {
  const hidden = new Set<string>(hiddenSubtree.value)
  for (const id of collapsedIds.value) hidden.add(id)
  return hidden
})
const collapsedContainers = computed(() => layout.value.containers.filter(item => collapsedIds.value.has(item.id) && !hiddenSubtree.value.has(item.id)))
const collapsibleContainers = computed(() => layout.value.containers.filter(item => item.childCount > deviceViewCollapseThreshold && !collapsedIds.value.has(item.id) && !hiddenIds.value.has(item.id) && item.id !== props.rootId && !rootAncestors.value.has(item.id)))
function toggleCollapse(id: string) {
  if (collapsedIds.value.has(id)) {
    collapsedIds.value = new Set([...collapsedIds.value].filter(item => item !== id))
    return
  }
  // 根设备与其祖先容器不允许折叠，根必须始终可见。
  if (id === props.rootId || rootAncestors.value.has(id)) return
  const container = layout.value.containers.find(item => item.id === id)
  if (!container || container.childCount <= deviceViewCollapseThreshold) return
  collapsedIds.value = new Set([...collapsedIds.value, id])
}
function collapsedTitle(item: DeviceViewContainer) {
  const owner = layout.value.nodes.find(node => node.node.id === item.id)?.node
  return owner ? `${entityLabels[owner.entity_type]} ${shorten(owner.label || owner.stable_id, 16)}` : '聚合容器'
}
function collapsedSubtitle(item: DeviceViewContainer) {
  const first = layout.value.nodes.find(node => node.node.id === item.childIds[0])?.node
  return `${first ? entityLabels[first.entity_type] : '成员'} ×${item.childCount} · 点击展开`
}
const filters = ref<Record<EdgeType, boolean>>({ power_relation: true, network_relation: true, spatial_relation: true, composition_relation: true })
const domains: EdgeType[] = ['power_relation', 'network_relation', 'spatial_relation', 'composition_relation']
const gpuCount = computed(() => layout.value.nodes.filter(item => item.group === 'gpu').length)
const networkCount = computed(() => layout.value.edges.filter(item => ['server_uplink', 'gpu_uplink'].includes(item.edge.relation_kind) && [props.rootId, ...layout.value.nodes.filter(node => node.group === 'gpu').map(node => node.node.id)].includes(item.edge.from)).length)
function nodeVisible(item: DeviceViewNode) {
  if (item.node.id === props.rootId) return true
  // 折叠容器及其后代隐藏，由摘要框替代。
  if (hiddenIds.value.has(item.node.id)) return false
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
  // 折叠容器不画普通框，由摘要框替代；隐藏子树的容器同样不画。
  return layout.value.containers.filter(item => ids.has(item.id) && !collapsedIds.value.has(item.id) && !hiddenIds.value.has(item.id)).sort((a, b) => a.depth - b.depth)
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
// 邻接索引一次构建，避免选中时对每节点全量扫边（O(N×E) → O(N+E)）。
const adjacency = computed(() => {
  const neighbors = new Map<string, Set<string>>()
  for (const edge of props.graph.edges) {
    for (const [self, peer] of [[edge.from, edge.to], [edge.to, edge.from]] as const) {
      const set = neighbors.get(self) || new Set<string>()
      set.add(peer); neighbors.set(self, set)
    }
  }
  return neighbors
})
function edgeHighlighted(edge: TopologyEdge) { return selectionIds.value.has(edge.from) || selectionIds.value.has(edge.to) }
function nodeHighlighted(id: string) {
  if (selectionIds.value.has(id)) return true
  const neighbors = adjacency.value.get(id)
  if (!neighbors) return false
  for (const peer of neighbors) if (selectionIds.value.has(peer)) return true
  return false
}
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
        <!-- 文字反缩放：缩放画布时字号按 1/k 反向补偿，标签在任意缩放级别保持屏幕可读（借鉴 gpu_topology）。 -->
        <g class="device-content" :style="{ fontSize: `${1 / zoomLevel}px` }">
        <template v-if="layout.nodes.length">
          <g class="device-containers">
            <rect v-for="item in visibleContainers" :key="item.id" :data-container-id="item.id" :x="item.x" :y="item.y" :width="item.width" :height="item.height" rx="14"
              :class="['containment-container', item.group, { selected: selectionIds.has(item.id) }]" />
          </g>
          <g class="device-collapsed">
            <g v-for="item in collapsedContainers" :key="item.id" class="collapsed-box" :data-collapsed-container-id="item.id" :data-group="item.group" :transform="`translate(${item.x} ${item.y})`"
              role="button" tabindex="0" :aria-label="`${collapsedTitle(item)}，${collapsedSubtitle(item)}`"
              @click.stop="toggleCollapse(item.id)" @keydown.enter.prevent="toggleCollapse(item.id)" @keydown.space.prevent="toggleCollapse(item.id)">
              <title>{{ collapsedTitle(item) }} · {{ collapsedSubtitle(item) }}</title>
              <rect :width="item.width" :height="item.height" rx="10" />
              <text class="collapsed-title" x="14" y="34">{{ collapsedTitle(item) }}</text>
              <text class="collapsed-count" x="14" y="60">{{ collapsedSubtitle(item) }}</text>
              <text class="collapsed-hint" x="14" y="84">展开后按网格排列，可再次收起</text>
            </g>
          </g>
          <g v-for="item in collapsibleContainers" :key="`toggle-${item.id}`" class="collapse-toggle" :data-collapse-toggle-id="item.id" role="button" tabindex="0"
            aria-label="收起该容器为摘要框" @click.stop="toggleCollapse(item.id)" @keydown.enter.prevent="toggleCollapse(item.id)">
            <rect :x="item.x + item.width - 32" :y="item.y + 10" width="20" height="20" rx="5" />
            <text :x="item.x + item.width - 22" :y="item.y + 25" text-anchor="middle">−</text>
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
        </g>
      </svg>
    </div>
    <div v-if="layout.warnings.length" class="containment-warnings" role="status"><span v-for="warning in layout.warnings" :key="warning">{{ warning }}</span></div>
    <details v-if="containmentRelations.length" class="containment-details">
      <summary>归属与标签关系 {{ containmentRelations.length }} 条（容器或背景元数据表达，不画连线）</summary>
      <button v-for="item in containmentRelations" :key="item.edge.id" :data-contained-edge-id="item.edge.id" @click="selectKeyboard({ kind: 'edge', id: item.edge.id })">
        {{ edgeLabel(item.edge) }} · {{ graph.nodes.find(node => node.id === item.edge.from)?.stable_id }} → {{ graph.nodes.find(node => node.id === item.edge.to)?.stable_id }}
      </button>
    </details>
    <div class="device-legend"><span>左侧电力、中间机柜与设备、右侧网络对端；IDC/房间在顶部展示</span><span>GPU、端口与UPS成员按网格换行；超过12个自动折叠为摘要框，点击框展开、点击 − 收起</span><span>仅网络和供电依赖绘制箭头，A/B分别保留</span><span>POD按成员或标签展示，不推导空间层级</span></div>
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
.device-stage { overflow: hidden; height: clamp(440px, 85vh, 1080px); background: #14243b; }
.device-svg { width: 100%; height: 100%; display: block; touch-action: none; user-select: none; cursor: grab; }
.device-svg:active { cursor: grabbing; }
.containment-container { fill: #1c304a; stroke: #7791ae; stroke-width: 2; pointer-events: none; }
.containment-container.server { fill: #243f5c; stroke: #75a6e2; }
.containment-container.network { fill: #1a3d49; stroke: #6bb3ba; }
.containment-container.power { fill: #3b3427; stroke: #d2ab62; }
.containment-container.selected { stroke-width: 4; stroke: #60a5ff; }
.collapsed-box { cursor: pointer; }
.collapsed-box rect { fill: #eef6ff; stroke: #60a5ff; stroke-width: 2; stroke-dasharray: 8 4; }
.collapsed-box:hover rect, .collapsed-box:focus-visible rect { fill: #e2efff; stroke-width: 3; }
.collapsed-box .collapsed-title { fill: #213955; font-size: 15px; font-weight: 600; }
.collapsed-box .collapsed-count { fill: #3479df; font-size: 13px; font-weight: 600; }
.collapsed-box .collapsed-hint { fill: #5b7290; font-size: 11px; }
.collapse-toggle { cursor: pointer; }
.collapse-toggle rect { fill: #1c304a; stroke: #60a5ff; stroke-width: 1.5; }
.collapse-toggle text { fill: #cfe2ff; font-size: 14px; font-weight: 700; pointer-events: none; }
.collapse-toggle:hover rect { fill: #2c4a6e; }
.containment-details { padding: 8px 12px; border-top: 1px solid #d8e2ef; color: #354963; font-size: 12px; }
.containment-details summary { cursor: pointer; }
.containment-details button { display: block; margin: 6px 0; background: #fff; border: 1px solid #d8e2ef; border-radius: 5px; padding: 5px 9px; color: #354963; cursor: pointer; text-align: left; }
.containment-warnings { display: flex; flex-direction: column; padding: 8px 12px; color: #946318; background: #fff5df; font-size: 12px; }
.empty-label { fill: #bacce3; font-size: 16px; pointer-events: none; }
.device-node { cursor: pointer; }
.device-node rect { fill: #f2f6fd; stroke: #91b2dc; stroke-width: 2; }
.device-node.server rect { fill: #deebfc; stroke: #79aaf0; }
.device-node.gpu rect { fill: #e7f5f2; stroke: #72baae; }
.device-node.power rect { fill: #fff4dc; stroke: #d2ab62; }
.device-node.interface rect, .device-node.network rect { fill: #e5f4f8; stroke: #7bb5c3; }
.device-node.background rect { fill: #e9edf5; stroke: #99a8bf; }
/* 反缩放：字号用 em 相对单位，跟随 .device-content 的 font-size 补偿缩放。 */
.device-content text { font-size: inherit; }
.node-type { fill: #60738c; font-size: 1.1em; }
.node-name { fill: #213955; font-size: 1.4em; font-weight: 600; }
.node-identity { fill: #334e6b; font-size: 1.2em; }
.node-model { fill: #62768d; font-size: 1.1em; }
.idc-metadata .node-type, .idc-metadata .node-identity { fill: #bacce3; }
.idc-metadata .node-name { fill: #e7effb; }
.idc-metadata.highlighted text, .idc-metadata.selected text, .idc-metadata:focus text { fill: #60a5ff; }
.edge-line { fill: none; stroke-width: 2; }
.edge-hit { fill: none; stroke: transparent; stroke-width: 16; cursor: pointer; }
.edge-label { fill: #e7effb; font-size: 1.1em; paint-order: stroke; stroke: #14243b; stroke-width: 5px; stroke-linejoin: round; cursor: pointer; }
.highlighted .edge-line { stroke-width: 4; }
.device-node.highlighted rect, .device-node.selected rect { stroke: #60a5ff; stroke-width: 4; }
.device-node:focus rect { stroke: #60a5ff; stroke-width: 4; }
.device-edges g:focus .edge-line { stroke-width: 4; }
.muted { opacity: 0.45; }
.device-legend { display: flex; flex-wrap: wrap; gap: 6px 18px; color: #61758f; padding: 9px 12px; font-size: 11px; line-height: 1.5; }
@media (max-width: 640px) { .device-toolbar { align-items: flex-start; gap: 8px; } .device-stage { height: 440px; } }
</style>
