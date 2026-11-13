import type { TopologyEdge, TopologyNode } from '../api/topologyTypes'

export type SpatialSceneMode = 'space' | 'network'
export interface SpatialSceneData {
  nodes: TopologyNode[]
  edges: TopologyEdge[]
  root: TopologyNode | null
  truncated: boolean
  warnings: string[]
  assignmentStatus?: Record<string, 'missing' | 'ambiguous' | 'truncated' | 'unknown'>
  cabinetAssignments?: Record<string, string>
  componentOwners?: Record<string, string>
  networkLevels?: Record<string, { role: string; level: number | null }>
}
export interface SpatialPlacement {
  node: TopologyNode
  x: number
  y: number
  z: number
  kind: 'cabinet' | 'device' | 'network' | 'gpu' | 'background'
  parentId?: string
  role: string
  tier?: number | null
}
export interface SpatialSceneLayout {
  placements: SpatialPlacement[]
  edges: TopologyEdge[]
  warnings: string[]
  width: number
  depth: number
  height: number
}
export function spatialDeviceRole(node: TopologyNode): string {
  return typeof node.properties.role === 'string' ? node.properties.role.trim() : ''
}
export function isSpatialNetworkDevice(node: TopologyNode, networkLevels: SpatialSceneData['networkLevels']): boolean {
  return node.entity_type === 'device' && Object.hasOwn(networkLevels || {}, node.id)
}

export function buildSpatialScene(data: SpatialSceneData, mode: SpatialSceneMode): SpatialSceneLayout {
  const nodes = new Map(data.nodes.map(node => [node.id, node]))
  if (data.root) nodes.set(data.root.id, data.root)
  const warnings = [...data.warnings]
  const placements: SpatialPlacement[] = []
  const byId = new Map<string, SpatialPlacement>()
  const cabinetOf = new Map(Object.entries(data.cabinetAssignments || {}))
  const ownerOf = new Map(Object.entries(data.componentOwners || {}))
  const networkLevels = new Map(Object.entries(data.networkLevels || {}))
  const actualEdges = data.edges.filter(edge => nodes.has(edge.from) && nodes.has(edge.to) && edge.source_id === nodes.get(edge.from)!.source_id && edge.source_id === nodes.get(edge.to)!.source_id)
  function add(placement: SpatialPlacement) {
    placements.push(placement)
    byId.set(placement.node.id, placement)
  }
  const cabinets = [...nodes.values()].filter(node => node.entity_type === 'cabinet').sort((a, b) => a.id.localeCompare(b.id))
  const columns = Math.max(1, Math.ceil(Math.sqrt(cabinets.length)))
  cabinets.forEach((node, index) => add({ node, kind: 'cabinet', role: '', x: index % columns * 10, y: 0, z: Math.floor(index / columns) * 10 }))
  const deviceIndexes = new Map<string, number>()
  let orphanIndex = 0
  let unknownCount = 0
  const devices = [...nodes.values()].filter(node => node.entity_type === 'device').sort((a, b) => a.id.localeCompare(b.id))
  const roleIndexes = new Map<string, number>()
  const networkColumns = Math.max(1, Math.ceil(Math.sqrt(devices.filter(node => networkLevels.has(node.id)).length)))
  for (const node of devices) {
    const parentId = cabinetOf.get(node.id)
    const cabinet = parentId ? byId.get(parentId) : undefined
    const index = cabinet ? deviceIndexes.get(cabinet.node.id) || 0 : orphanIndex++
    if (cabinet) deviceIndexes.set(cabinet.node.id, index + 1)
    const classification = networkLevels.get(node.id)
    const role = classification ? classification.role : spatialDeviceRole(node)
    const network = !!classification
    const tier = classification?.level ?? null
    if (network && tier === null) unknownCount++
    const layered = mode === 'network' && network
    const layer = JSON.stringify([role, tier])
    const layerIndex = roleIndexes.get(layer) || 0
    if (layered) roleIndexes.set(layer, layerIndex + 1)
    const y = layered ? tier === null ? 4 : 8 + tier * 8 : cabinet ? 1 + index * 1.1 : 1
    // 网络层只使用后端级别，角色仅作为原始分组标签。
    add({ node, kind: network ? 'network' : 'device', role, ...(network ? { tier } : {}), ...(cabinet && !layered ? { parentId: cabinet.node.id } : {}),
      x: layered ? layerIndex % networkColumns * 8 : cabinet ? cabinet.x : columns * 10 + 10 + index % 5 * 8,
      y, z: layered ? Math.floor(layerIndex / networkColumns) * 6 : cabinet ? cabinet.z : Math.floor(index / 5) * 6 })
  }
  const childIndexes = new Map<string, number>()
  const linkedPortIds = new Set(actualEdges.filter(edge => edge.edge_type === 'network_relation' && edge.relation_kind === 'links_to')
    .flatMap(edge => [edge.from, edge.to]))
  let unresolvedPorts = 0
  for (const node of nodes.values()) {
    if (node.entity_type !== 'gpu' && (mode !== 'network' || node.entity_type !== 'interface')) continue
    const parentId = ownerOf.get(node.id)
    const owner = parentId ? byId.get(parentId) : undefined
    if (!owner) {
      if (node.entity_type === 'interface' && linkedPortIds.has(node.id)) {
        const index = unresolvedPorts++
        add({ node, kind: 'network', role: '', x: columns * 10 + 30 + index % 5 * 2, y: 4, z: Math.floor(index / 5) * 2 })
      }
      continue
    }
    const index = childIndexes.get(owner.node.id) || 0
    childIndexes.set(owner.node.id, index + 1)
    add({ node, kind: node.entity_type === 'gpu' ? 'gpu' : 'network', role: '', parentId: owner.node.id,
      x: owner.x + 1.5 + index % 4 * 0.55, y: owner.y + (node.entity_type === 'gpu' ? 0.45 : 0), z: owner.z + Math.floor(index / 4) * 0.55 })
  }
  const backgrounds = [...nodes.values()].filter(node => node.id === data.root?.id || node.entity_type === 'room').sort((a, b) => a.id.localeCompare(b.id))
  backgrounds.forEach((node, index) => add({ node, kind: 'background', role: '', x: -7 - index * 8, y: 0, z: -7 }))
  if (orphanIndex) {
    const unresolved = devices.filter(node => !cabinetOf.has(node.id))
    if (unresolved.some(node => data.assignmentStatus?.[node.id] === 'truncated')) warnings.push('部分设备归属尚未完整读取，放在待核验区域；不代表没有机柜关系。')
    if (unresolved.some(node => data.assignmentStatus?.[node.id] === 'ambiguous')) warnings.push('部分设备有多个机柜归属，暂不分组。')
    if (unresolved.some(node => !data.assignmentStatus?.[node.id] || data.assignmentStatus[node.id] === 'unknown')) warnings.push('部分设备机柜归属未核验，独立展示，不推断其归属。')
    if (unresolved.some(node => data.assignmentStatus?.[node.id] === 'missing')) warnings.push('无机柜关系：后端已确认未读取到有效机柜关系。')
  }
  if (unknownCount) warnings.push('未分类网络设备：保留原始 role，不按名称推断 T0/T1/T2。')
  if (unresolvedPorts) warnings.push('端口设备归属未核验：单独展示真实互联端点，不推断所属设备。')
  warnings.push('示意布局：设备间距与层级高度不是实际 U 位、尺寸或测量坐标。')
  if (data.truncated) warnings.push('部分数据：分页或读取预算已截断，仅展示已加载节点和关系。')
  const edges = actualEdges.filter(edge => byId.has(edge.from) && byId.has(edge.to)
    && (mode === 'space' || edge.edge_type === 'network_relation'
      || (edge.edge_type === 'composition_relation' && ['owns_interface', 'contains_gpu'].includes(edge.relation_kind))))
  return { placements, edges, warnings: [...new Set(warnings)],
    width: Math.max(20, ...placements.map(item => item.x + 8)),
    depth: Math.max(20, ...placements.map(item => item.z + 8)),
    height: Math.max(10, ...placements.map(item => item.y + 5)) }
}
