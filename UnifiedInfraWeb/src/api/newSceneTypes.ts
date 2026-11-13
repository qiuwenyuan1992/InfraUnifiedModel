import type { EdgeType, EntityType, PropertyValue, TopologyEdge, TopologyNode, TopologyRootType } from './topologyTypes'
import type { SpatialSceneData } from '../services/spatialScene'

export interface SceneNode {
  id: string
  source_id: string
  entity_type: EntityType
  stable_id: string
  label: string
  role: string
  parent_type_id: number | null
  synced_at: string | null
}
export interface SceneEdge {
  id: string
  source_id: string
  edge_type: EdgeType
  relation_kind: string
  from: string
  to: string
  rank: string
  power_path?: PropertyValue
  plane?: PropertyValue
}
export interface SceneQuery {
  source_id: string
  entity_type: TopologyRootType | 'room'
  stable_id: string
  limit?: number
  after?: string
}
export interface SceneResponse {
  root: SceneNode
  cabinets: { node: SceneNode; devices: SceneNode[]; next_after: string }[]
  unassigned: { node: SceneNode; status: 'missing' | 'ambiguous' | 'truncated' }[]
  component_owners: { parent_id: string; child_ids: string[] }[]
  network_levels: { role: string; level: number | null; nodes: SceneNode[] }[]
  nodes: SceneNode[]
  edges: SceneEdge[]
  next_after: string
  truncated: boolean
  warnings: string[]
}

export function sceneTopologyNode(node: SceneNode): TopologyNode {
  return { id: node.id, source_id: node.source_id, entity_type: node.entity_type, stable_id: node.stable_id,
    label: node.label, properties: { role: node.role, parent_type_id: node.parent_type_id }, created_at: null, synced_at: node.synced_at }
}
export function sceneTopologyEdge(edge: SceneEdge): TopologyEdge {
  return { id: edge.id, source_id: edge.source_id, edge_type: edge.edge_type, relation_kind: edge.relation_kind,
    from: edge.from, to: edge.to, rank: edge.rank, properties: {
      ...(edge.power_path !== undefined ? { power_path: edge.power_path } : {}),
      ...(edge.plane !== undefined ? { plane: edge.plane } : {}),
    }, created_at: null, synced_at: null }
}

export function normalizeSceneResponse(page: SceneResponse): { data: SpatialSceneData; cabinetNextAfter: Record<string, string> } {
  if (!Array.isArray(page.component_owners)) throw new Error('场景缺少组件归属契约。')
  const nodes = new Map<string, SceneNode>()
  const fields: (keyof SceneNode)[] = ['id', 'source_id', 'entity_type', 'stable_id', 'label', 'role', 'parent_type_id', 'synced_at']
  function sameNode(a: SceneNode, b: SceneNode) { return fields.every(field => a[field] === b[field]) }
  for (const node of [page.root, ...page.nodes]) {
    if (node.source_id !== page.root.source_id) throw new Error('场景节点来源不匹配。')
    const existing = nodes.get(node.id)
    if (existing && !sameNode(existing, node)) throw new Error('场景节点定义冲突。')
    nodes.set(node.id, node)
  }
  function reference(id: string, types: EntityType[]): SceneNode {
    const node = nodes.get(id)
    if (!node || !types.includes(node.entity_type)) throw new Error('场景层级引用不存在或节点类型不匹配。')
    return node
  }
  function groupedNode(node: SceneNode, types: EntityType[]) {
    const canonical = reference(node.id, types)
    if (!sameNode(canonical, node)) throw new Error('场景层级节点定义冲突。')
    return canonical
  }
  const cabinetAssignments = new Map<string, string>()
  const componentOwners = new Map<string, string>()
  const networkLevels = new Map<string, { role: string; level: number | null }>()
  const assignmentStatus = new Map<string, SceneResponse['unassigned'][number]['status']>()
  const cabinetNextAfter = new Map<string, string>()
  const warnings = new Set(page.warnings)
  let truncated = page.truncated
  function assign(map: Map<string, string>, child: string, parent: string) {
    if (map.has(child) && map.get(child) !== parent) throw new Error('场景归属冲突。')
    map.set(child, parent)
  }
  for (const cabinet of page.cabinets) {
    const parent = groupedNode(cabinet.node, ['cabinet'])
    for (const device of cabinet.devices) assign(cabinetAssignments, groupedNode(device, ['device']).id, parent.id)
    if (cabinet.next_after) {
      if (cabinetNextAfter.has(parent.id) && cabinetNextAfter.get(parent.id) !== cabinet.next_after) throw new Error('场景机柜分页游标冲突。')
      cabinetNextAfter.set(parent.id, cabinet.next_after)
      warnings.add(`${parent.label || parent.stable_id}：该机柜还有设备未加载`)
      truncated = true
    }
  }
  for (const group of page.component_owners) {
    reference(group.parent_id, ['device'])
    for (const child of group.child_ids) {
      reference(child, ['interface', 'gpu'])
      assign(componentOwners, child, group.parent_id)
    }
  }
  for (const group of page.network_levels) {
    if (group.level !== null && (!Number.isInteger(group.level) || group.level < 0 || group.level > 2)) throw new Error('场景网络层级无效。')
    for (const item of group.nodes) {
      const node = groupedNode(item, ['device'])
      const existing = networkLevels.get(node.id)
      if (existing && (existing.role !== group.role || existing.level !== group.level)) throw new Error('场景网络层级冲突。')
      networkLevels.set(node.id, { role: group.role, level: group.level })
    }
  }
  for (const item of page.unassigned) {
    const node = groupedNode(item.node, ['room', 'cabinet', 'device', 'interface', 'gpu'])
    if (!['missing', 'ambiguous', 'truncated'].includes(item.status) || cabinetAssignments.has(node.id) || componentOwners.has(node.id)
      || (assignmentStatus.has(node.id) && assignmentStatus.get(node.id) !== item.status)) throw new Error('场景未确认归属与已确认归属冲突。')
    assignmentStatus.set(node.id, item.status)
  }
  const edges = new Map<string, TopologyEdge>()
  for (const edge of page.edges) {
    if (edge.source_id !== page.root.source_id || !nodes.has(edge.from) || !nodes.has(edge.to)) throw new Error('场景关系来源或端点不匹配。')
    if (!edges.has(edge.id)) edges.set(edge.id, sceneTopologyEdge(edge))
  }
  if (page.next_after) { warnings.add('当前场景还有下一页未加载。'); truncated = true }
  return { data: { root: sceneTopologyNode(page.root), nodes: [...nodes.values()].map(sceneTopologyNode), edges: [...edges.values()],
    truncated, warnings: [...warnings], cabinetAssignments: Object.fromEntries(cabinetAssignments), componentOwners: Object.fromEntries(componentOwners),
    networkLevels: Object.fromEntries(networkLevels), assignmentStatus: Object.fromEntries(assignmentStatus) }, cabinetNextAfter: Object.fromEntries(cabinetNextAfter) }
}
