import type { TopologyEdge, TopologyGraph, TopologyNode } from '../api/topologyTypes'

export type DeviceViewGroup = 'server' | 'gpu' | 'power' | 'interface' | 'network' | 'background'
export interface DeviceViewRect { x: number; y: number; width: number; height: number }
export interface DeviceViewNode extends DeviceViewRect { node: TopologyNode; group: DeviceViewGroup }
export interface DeviceViewEdge { edge: TopologyEdge; path: string; labelX: number; labelY: number; contained?: boolean }
export interface DeviceViewContainer extends DeviceViewRect { id: string; group: DeviceViewGroup; relationIds: string[]; depth: number; childIds: string[]; childCount: number; collapsed: boolean }
export interface DeviceViewLayout { nodes: DeviceViewNode[]; edges: DeviceViewEdge[]; container: DeviceViewRect; width: number; height: number; containers: DeviceViewContainer[]; warnings: string[] }

// 超过该数量的容器默认折叠为摘要框（组件层决定），借鉴 gpu_topology 的语义聚合。
export const deviceViewCollapseThreshold = 12

const compare = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0
const stableOrder = (a: TopologyNode, b: TopologyNode) => compare(a.stable_id, b.stable_id) || compare(a.id, b.id)
const cardWidth = 180
const cardHeight = 96
const gap = 20
const rowGap = 20
const childTop = cardHeight + gap * 2
const summaryWidth = 280
const summaryHeight = childTop + cardHeight + gap

// 网格换行容量：子节点不再单行铺开，容器宽度保持有界。
const rowCapacityOf = (owned: TopologyNode[]): number => {
  if (owned.length < 2) return 1
  if (owned.every(child => child.entity_type === 'gpu')) return 8
  if (owned.every(child => child.entity_type === 'interface')) return 4
  if (owned.every(child => child.entity_type === 'ups')) return 6
  return 3
}

export function buildDeviceView(graph: TopologyGraph, rootId: string, collapsed?: Iterable<string>): DeviceViewLayout {
  const root = graph.nodes.find(node => node.id === rootId)
  if (!root) return { nodes: [], edges: [], container: { x: 0, y: 0, width: 0, height: 0 }, width: 1200, height: 600, containers: [], warnings: [] }
  const originals = [...graph.nodes].sort(stableOrder)
  const originalById = new Map(originals.map(node => [node.id, node]))
  const relations = [...graph.edges].sort((a, b) => compare(a.id, b.id))
  const candidates = new Map<string, Map<string, string[]>>()
  const warnings: string[] = []
  for (const edge of relations) {
    const from = originalById.get(edge.from), to = originalById.get(edge.to)
    if (!from || !to || from.source_id !== to.source_id || edge.source_id !== from.source_id) continue
    let parent: TopologyNode | undefined, child: TopologyNode | undefined
    if (edge.edge_type === 'spatial_relation' && edge.relation_kind === 'located_in' &&
      ((from.entity_type === 'device' && to.entity_type === 'cabinet') || (from.entity_type === 'cabinet' && ['data_center', 'room'].includes(to.entity_type)) || (from.entity_type === 'room' && to.entity_type === 'data_center'))) {
      parent = to; child = from
    } else if (edge.edge_type === 'composition_relation' && from.entity_type === 'device' &&
      ((edge.relation_kind === 'contains_gpu' && to.entity_type === 'gpu') || (edge.relation_kind === 'owns_interface' && to.entity_type === 'interface'))) {
      parent = from; child = to
    } else if (edge.edge_type === 'power_relation' && edge.relation_kind === 'member_of' && from.entity_type === 'ups' && to.entity_type === 'ups_group') {
      parent = to; child = from
    }
    if (!parent || !child) continue
    // IDC和房间是并存的背景归属，不参与容器多父判定。
    if (['data_center', 'room'].includes(parent.entity_type)) continue
    const parents = candidates.get(child.id) || new Map<string, string[]>()
    parents.set(parent.id, [...(parents.get(parent.id) || []), edge.id])
    candidates.set(child.id, parents)
  }
  const parentOf = new Map<string, string>()
  for (const node of originals) {
    const parents = candidates.get(node.id)
    if (!parents) continue
    if (parents.size > 1) {
      warnings.push(`节点 ${node.id} 存在多个容器父节点：${[...parents.keys()].sort(compare).join('、')}，未进行分组。`)
    } else {
      parentOf.set(node.id, parents.keys().next().value!)
    }
  }
  // 即使以后扩展关系矩阵，也不能让异常数据触发递归环。
  const cyclic = new Set<string>()
  for (const node of originals) {
    const chain: string[] = [], seen = new Map<string, number>()
    let id: string | undefined = node.id
    while (id !== undefined && parentOf.has(id)) {
      const index = seen.get(id)
      if (index !== undefined) { chain.slice(index).forEach(item => cyclic.add(item)); break }
      seen.set(id, chain.length); chain.push(id); id = parentOf.get(id)
    }
  }
  if (cyclic.size) {
    warnings.push(`容器关系存在循环：${[...cyclic].sort(compare).join('、')}，未进行循环分组。`)
    cyclic.forEach(id => parentOf.delete(id))
  }
  const children = new Map<string, TopologyNode[]>()
  const relationIds = new Map<string, string[]>()
  const containedIds = new Set<string>()
  for (const node of originals) {
    const parent = parentOf.get(node.id)
    if (!parent) continue
    children.set(parent, [...(children.get(parent) || []), node])
    const ids = candidates.get(node.id)!.get(parent)!
    relationIds.set(parent, [...(relationIds.get(parent) || []), ...ids].sort(compare))
    ids.forEach(id => containedIds.add(id))
  }
  const groupOf = (node: TopologyNode): DeviceViewGroup => {
    if (['transformer', 'ups_group', 'ups', 'rpp'].includes(node.entity_type)) return 'power'
    if (node.entity_type === 'interface') return 'interface'
    if (node.entity_type === 'gpu') return parentOf.has(node.id) ? 'gpu' : 'background'
    if (node.entity_type === 'device') {
      if (node.id === rootId || children.get(node.id)?.some(child => child.entity_type === 'gpu')) return 'server'
      if (children.get(node.id)?.some(child => child.entity_type === 'interface')) return 'network'
    }
    return 'background'
  }
  const collapsedIds = collapsed ? new Set(collapsed) : new Set<string>()
  // 折叠容器的全部后代同样隐藏，布局按紧凑尺寸计算，画布不随隐藏子树膨胀。
  const collapsedSubtree = new Set<string>()
  if (collapsedIds.size) {
    const expand = (id: string) => {
      for (const child of children.get(id) || []) {
        if (collapsedSubtree.has(child.id)) continue
        collapsedSubtree.add(child.id)
        expand(child.id)
      }
    }
    collapsedIds.forEach(expand)
  }
  const sizes = new Map<string, { width: number; height: number }>()
  const measure = (node: TopologyNode): { width: number; height: number } => {
    const cached = sizes.get(node.id)
    if (cached) return cached
    const owned = children.get(node.id) || []
    if (!owned.length && (node.id !== rootId || ['data_center', 'room', 'pod'].includes(node.entity_type))) {
      const size = { width: cardWidth, height: cardHeight }; sizes.set(node.id, size); return size
    }
    if (collapsedIds.has(node.id) || (collapsedSubtree.has(node.id) && owned.length)) {
      // 折叠态：容器收缩为摘要框尺寸。
      const size = { width: summaryWidth, height: summaryHeight }; sizes.set(node.id, size); return size
    }
    if (collapsedSubtree.has(node.id)) {
      const size = { width: cardWidth, height: cardHeight }; sizes.set(node.id, size); return size
    }
    const capacity = rowCapacityOf(owned)
    let width = 0, height = childTop, rows = 0
    for (let start = 0; start < owned.length; start += capacity) {
      const row = owned.slice(start, start + capacity)
      width = Math.max(width, row.reduce((sum, child) => sum + measure(child).width, 0) + (row.length - 1) * gap)
      height += Math.max(40, ...row.map(child => measure(child).height))
      rows++
    }
    const size = { width: Math.max(280, width + gap * 2), height: height + Math.max(0, rows - 1) * rowGap + gap }
    sizes.set(node.id, size); return size
  }
  originals.forEach(measure)
  const placed = new Map<string, DeviceViewNode>()
  const containers: DeviceViewContainer[] = []
  const place = (node: TopologyNode, x: number, y: number, depth = 0) => {
    const size = sizes.get(node.id)!, owned = children.get(node.id) || [], group = groupOf(node)
    const isContainer = !['data_center', 'room', 'pod'].includes(node.entity_type) && (owned.length > 0 || node.id === rootId)
    placed.set(node.id, { node, group, x: x + (isContainer ? gap : 0), y: y + (isContainer ? gap : 0), width: isContainer ? size.width - gap * 2 : size.width, height: cardHeight })
    if (!isContainer) return
    containers.push({ id: node.id, group, x, y, ...size, depth, relationIds: relationIds.get(node.id) || [], childIds: owned.map(child => child.id), childCount: owned.length, collapsed: collapsedIds.has(node.id) })
    if (collapsedIds.has(node.id) || collapsedSubtree.has(node.id)) {
      // 折叠态：子树隐藏渲染，统一堆叠在摘要框内，画布保持紧凑。
      for (const child of owned) place(child, x + gap, y + childTop, depth + 1)
      return
    }
    const capacity = rowCapacityOf(owned)
    let cursorX = x + gap, cursorY = y + childTop, column = 0, rowHeight = 0
    for (const child of owned) {
      place(child, cursorX, cursorY, depth + 1)
      const childSize = sizes.get(child.id)!
      cursorX += childSize.width + gap
      rowHeight = Math.max(rowHeight, childSize.height)
      if (++column === capacity) { column = 0; cursorX = x + gap; cursorY += rowHeight + rowGap; rowHeight = 0 }
    }
  }
  let rootTree = rootId
  while (parentOf.has(rootTree)) rootTree = parentOf.get(rootTree)!
  const roots = originals.filter(node => !parentOf.has(node.id))
  const top = roots.filter(node => ['data_center', 'room', 'pod'].includes(node.entity_type))
  const topIds = new Set(top.map(node => node.id))
  const baseY = top.length ? 180 : 80
  const transformers = roots.filter(node => node.id !== rootTree && node.entity_type === 'transformer')
  const ups = roots.filter(node => node.id !== rootTree && ['ups_group', 'ups'].includes(node.entity_type))
  const rpps = roots.filter(node => node.id !== rootTree && node.entity_type === 'rpp')
  const rowWidth = (items: TopologyNode[], spacing: number) => items.length ? items.reduce((sum, node) => sum + measure(node).width, 0) + Math.max(0, items.length - 1) * 40 + spacing : 0
  const upsX = 30 + rowWidth(transformers, 60)
  const rppX = upsX + rowWidth(ups, 60)
  const centerX = rppX + rowWidth(rpps, 100)
  const centers = [...(topIds.has(rootTree) ? [] : [originalById.get(rootTree)!]), ...roots.filter(node => node.id !== rootTree && node.entity_type === 'cabinet')]
  const rightX = centerX + rowWidth(centers, 100)
  const network = roots.filter(node => node.id !== rootTree && (groupOf(node) === 'network' || groupOf(node) === 'server'))
  const row = (items: TopologyNode[], x: number, y = baseY) => {
    for (const node of items) { place(node, x, y); x += measure(node).width + 40 }
  }
  row(top, 30, 32)
  row(transformers, 30); row(ups, upsX); row(rpps, rppX); row(centers, centerX); row(network, rightX)
  row(roots.filter(node => !placed.has(node.id)), rightX + rowWidth(network, 100))
  const nodes = originals.map(node => placed.get(node.id)!)
  containers.sort((a, b) => a.depth - b.depth || stableOrder(originalById.get(a.id)!, originalById.get(b.id)!))
  const containerById = new Map(containers.map(item => [item.id, item]))
  const rootRect = containerById.get(rootId) || placed.get(rootId)!
  const container = { x: rootRect.x, y: rootRect.y, width: rootRect.width, height: rootRect.height }
  const width = Math.max(1200, ...nodes.map(node => node.x + node.width + 180), ...containers.map(item => item.x + item.width + 180))
  const height = Math.max(600, ...nodes.map(node => node.y + node.height + 80), ...containers.map(item => item.y + item.height + 80))
  const pairs = new Map<string, TopologyEdge[]>()
  for (const edge of relations) {
    const key = JSON.stringify([edge.from, edge.to].sort(compare))
    const siblings = pairs.get(key) || []
    siblings.push(edge); pairs.set(key, siblings)
  }
  const edges = relations.map(edge => {
    const siblings = pairs.get(JSON.stringify([edge.from, edge.to].sort(compare)))!
    const offset = (siblings.indexOf(edge) - (siblings.length - 1) / 2) * Math.min(36, 120 / Math.max(1, siblings.length - 1))
    return { ...routeEdge(edge, containerById.get(edge.from) || placed.get(edge.from), containerById.get(edge.to) || placed.get(edge.to), offset), contained: containedIds.has(edge.id) }
  })
  return { nodes, edges, container, width, height, containers, warnings }
}

function routeEdge(edge: TopologyEdge, from: DeviceViewRect | undefined, to: DeviceViewRect | undefined, offset: number): DeviceViewEdge {
  if (!from || !to) return { edge, path: '', labelX: 0, labelY: 0 }
  const a = { x: from.x + from.width / 2, y: from.y + from.height / 2 }
  const b = { x: to.x + to.width / 2, y: to.y + to.height / 2 }
  if (edge.from === edge.to) {
    const reach = 90 + offset, right = from.x + from.width
    return { edge, path: `M ${right} ${from.y + 20} C ${right + reach} ${from.y + 20} ${right + reach} ${from.y + from.height - 20} ${right} ${from.y + from.height - 20}`, labelX: right + reach * 0.75, labelY: a.y - 8 }
  }
  const dx = b.x - a.x, dy = b.y - a.y
  const distance = Math.hypot(dx, dy) || 1
  const direction = compare(edge.from, edge.to) <= 0 ? 1 : -1
  const control = { x: (a.x + b.x) / 2 - dy / distance * offset * direction, y: (a.y + b.y) / 2 + dx / distance * offset * direction }
  const anchor = (center: { x: number; y: number }, card: DeviceViewRect) => {
    const vx = control.x - center.x, vy = control.y - center.y
    const ratio = 1 / Math.max(Math.abs(vx) / (card.width / 2), Math.abs(vy) / (card.height / 2), Number.EPSILON)
    return { x: center.x + vx * ratio, y: center.y + vy * ratio }
  }
  const start = anchor(a, from), end = anchor(b, to)
  // 曲线控制点基于边界端点，避免宽容器的连线重新进入框内部。
  control.x = (start.x + end.x) / 2 - dy / distance * offset * direction
  control.y = (start.y + end.y) / 2 + dx / distance * offset * direction
  return { edge, path: `M ${start.x} ${start.y} Q ${control.x} ${control.y} ${end.x} ${end.y}`, labelX: (start.x + 2 * control.x + end.x) / 4, labelY: (start.y + 2 * control.y + end.y) / 4 - 8 }
}
