import type { EdgeType, EntityType, PropertyValue, TopologyEdge, TopologyNode, TopologyRootType } from './topologyTypes'

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
  entity_type: TopologyRootType
  stable_id: string
  limit?: number
  after?: string
}
export interface SceneResponse {
  root: SceneNode
  cabinets: { node: SceneNode; devices: SceneNode[]; next_after: string }[]
  unassigned: { node: SceneNode; status: 'missing' | 'ambiguous' | 'truncated' }[]
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
