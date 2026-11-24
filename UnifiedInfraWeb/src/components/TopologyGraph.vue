<template>
  <svg :viewBox="`0 0 ${W} ${H}`" class="topo-svg" role="img" aria-label="机柜拓扑图">
    <!-- 行标签 -->
    <text v-for="row in rows" :key="row.label" :x="16" :y="row.y + 4" class="topo-row-label">{{ row.label }}</text>
    <!-- 机柜框 -->
    <rect v-for="c in cabinetRects" :key="c.id" :x="c.x" :y="c.y" :width="c.w" :height="c.h" rx="10" class="topo-cabinet" />
    <text v-for="c in cabinetRects" :key="c.id + '-label'" :x="c.x + c.w - 8" :y="c.y + 16" text-anchor="end" class="topo-cabinet-label">{{ c.id }}</text>
    <!-- 边：供电（实线）/ 链路（虚线） -->
    <line v-for="(e, i) in resolvedPower" :key="'p' + i" :x1="e.x1" :y1="e.y1" :x2="e.x2" :y2="e.y2" class="topo-edge topo-edge-power" :class="{ 'topo-edge-hot': e.hot }" />
    <line v-for="(e, i) in resolvedLink" :key="'l' + i" :x1="e.x1" :y1="e.y1" :x2="e.x2" :y2="e.y2" class="topo-edge topo-edge-link" :class="{ 'topo-edge-hot': e.hot }" />
    <line v-for="(e, i) in resolvedContainment" :key="'c' + i" :x1="e.x1" :y1="e.y1" :x2="e.x2" :y2="e.y2" class="topo-edge topo-edge-contain" :class="{ 'topo-edge-hot': e.hot }" />
    <!-- 节点 -->
    <g v-for="n in nodes" :key="n.id" :transform="`translate(${n.x}, ${n.y})`" class="topo-node" :class="nodeClass(n.id)">
      <rect x="-32" y="-13" width="64" height="26" rx="5" />
      <text y="4" text-anchor="middle">{{ n.id }}</text>
    </g>
    <!-- 图例 -->
    <g :transform="`translate(${W - 250}, ${H - 16})`" class="topo-legend">
      <circle cx="6" cy="-4" r="5" class="topo-legend-root" /><text x="16" y="0">疑似根因</text>
      <circle cx="80" cy="-4" r="5" class="topo-legend-affected" /><text x="90" y="0">受影响</text>
      <line x1="150" y1="-4" x2="175" y2="-4" class="topo-edge topo-edge-link" /><text x="180" y="0">链路</text>
    </g>
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'

// fixture 拓扑可视化：分层布局（母线→PDU→服务器→TOR→Spine），端口/PSU 归并到宿主设备
// 诊断联动：root 实体标红、受影响实体标橙、两端均受影响的边加粗
const props = defineProps<{
  entities: Record<string, string>
  powerEdges: string[][]
  linkEdges: string[][]
  containmentEdges?: string[][]
  root?: string
  affected?: string[]
}>()

const W = 940
const rows = [
  { types: ['busbar'], label: '母线', y: 40 },
  { types: ['pdu'], label: 'PDU', y: 105 },
  { types: ['server'], label: '服务器', y: 170 },
  { types: ['switch-cab'], label: 'TOR', y: 235 },
  { types: ['switch-pod'], label: 'Spine', y: 300 },
  { types: ['pod'], label: 'POD', y: 355 },
]
const H = 415

// 端口/PSU → 宿主（命名约定 id:sub → id）
function ownerOf(id: string): string {
  return id.includes(':') ? id.slice(0, id.lastIndexOf(':')) : id
}

// 节点分类：TOR（机柜内）与 Spine（POD 级）同为 switch，按 id 前缀分
function rowTypeOf(id: string, type: string): string {
  if (type === 'switch') return id.startsWith('spine') ? 'switch-pod' : 'switch-cab'
  if (type === 'bgp_session') return 'switch-pod'  // 骨干 BGP 会话与 Spine 同层渲染
  if (type === 'busbar') return 'busbar'
  return type
}

const nodes = computed(() => {
  const out: Array<{ id: string; x: number; y: number }> = []
  for (const row of rows) {
    const members = Object.entries(props.entities)
      .map(([id, t]) => ({ id, rt: rowTypeOf(id, t) }))
      .filter(m => row.types.includes(m.rt))
      .map(m => m.id)
      .sort()
    const step = Math.min(78, (W - 140) / Math.max(members.length, 1))
    members.forEach((id, i) => {
      const total = (members.length - 1) * step
      out.push({ id, x: 90 + (W - 180 - total) / 2 + i * step, y: row.y })
    })
  }
  return out
})

const pos = computed(() => {
  const m: Record<string, { x: number; y: number }> = {}
  for (const n of nodes.value) m[n.id] = { x: n.x, y: n.y }
  return m
})

const hotSet = computed(() => {
  const s = new Set<string>()
  if (props.root) s.add(ownerOf(props.root))
  for (const a of props.affected ?? []) s.add(ownerOf(a))
  return s
})

// 边端点归并到已渲染节点（psu/port → 宿主）
function endpoint(id: string): { x: number; y: number } | null {
  if (pos.value[id]) return pos.value[id]
  const o = ownerOf(id)
  return pos.value[o] ?? null
}

function resolveEdges(raw: string[][]) {
  return raw.map(([a, b]) => {
    const pa = endpoint(a)
    const pb = endpoint(b)
    return pa && pb ? {
      x1: pa.x, y1: pa.y, x2: pb.x, y2: pb.y,
      hot: hotSet.value.has(ownerOf(a)) && hotSet.value.has(ownerOf(b)),
    } : null
  }).filter((e): e is { x1: number; y1: number; x2: number; y2: number; hot: boolean } => e !== null)
}

const resolvedPower = computed(() => resolveEdges(props.powerEdges))
const resolvedLink = computed(() => resolveEdges(props.linkEdges))
const resolvedContainment = computed(() => resolveEdges(props.containmentEdges ?? []))

// 机柜框：按包含关系聚合成员坐标
const cabinetRects = computed(() => {
  const cabs = Object.entries(props.entities).filter(([, t]) => t === 'cabinet').map(([id]) => id)
  return cabs.map(cab => {
    const members = nodes.value.filter(n => n.id.startsWith(cab === 'cab-A12' ? 'srv-0' : 'srv-1')
      || (cab === 'cab-A12' ? ['tor-A1', 'tor-A2', 'pdu-A1', 'pdu-A2'] : ['tor-B1', 'tor-B2', 'pdu-B1', 'pdu-B2']).some(p => n.id.startsWith(p)))
    if (!members.length) return null
    const xs = members.map(m => m.x)
    const ys = members.map(m => m.y)
    return {
      id: cab,
      x: Math.min(...xs) - 44, y: Math.min(...ys) - 24,
      w: Math.max(...xs) - Math.min(...xs) + 88, h: Math.max(...ys) - Math.min(...ys) + 48,
    }
  }).filter((r): r is { id: string; x: number; y: number; w: number; h: number } => r !== null)
})

function nodeClass(id: string): string {
  if (props.root && ownerOf(props.root) === id) return 'topo-node-root'
  if (hotSet.value.has(id)) return 'topo-node-affected'
  return ''
}
</script>

<style scoped>
.topo-svg { width: 100%; height: auto; background: #fafbfd; border: 1px solid #e4e9f0; border-radius: 10px; }
.topo-row-label { font-size: 11px; fill: #8a94a6; }
.topo-cabinet { fill: none; stroke: #d3dce6; stroke-dasharray: 6 4; }
.topo-cabinet-label { font-size: 11px; fill: #8a94a6; }
.topo-edge { stroke-width: 1; }
.topo-edge-power { stroke: #d3dce6; }
.topo-edge-link { stroke: #8fb7e8; stroke-dasharray: 4 3; }
.topo-edge-contain { stroke: #c8d0dc; stroke-dasharray: 2 5; stroke-width: 2; }
.topo-edge-hot { stroke: #f56c6c; stroke-width: 2.5; }
.topo-node rect { fill: #fff; stroke: #c8d0dc; }
.topo-node text { font-size: 11px; fill: #4a5568; font-family: monospace; }
.topo-node-affected rect { fill: #fdf6ec; stroke: #e6a23c; }
.topo-node-affected text { fill: #a0641a; }
.topo-node-root rect { fill: #fef0f0; stroke: #f56c6c; stroke-width: 2; }
.topo-node-root text { fill: #c45656; font-weight: 700; }
.topo-legend text { font-size: 11px; fill: #8a94a6; }
.topo-legend-root { fill: #fef0f0; stroke: #f56c6c; }
.topo-legend-affected { fill: #fdf6ec; stroke: #e6a23c; }
</style>
