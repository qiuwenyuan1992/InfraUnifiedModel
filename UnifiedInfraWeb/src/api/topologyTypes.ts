export type EntityType = 'device' | 'gpu' | 'interface' | 'cabinet' | 'room' | 'data_center' | 'pod' | 'transformer' | 'ups_group' | 'ups' | 'rpp'
export type EdgeType = 'spatial_relation' | 'composition_relation' | 'network_relation' | 'power_relation'
export type Direction = 'in' | 'out' | 'both'
export type PropertyValue = string | number | boolean | null | PropertyValue[] | { [key: string]: PropertyValue }
export interface TopologyIdentity { source_id: string; entity_type: EntityType; stable_id: string }
interface TopologyRecord {
  id: string
  source_id: string
  properties: Record<string, PropertyValue>
  created_at: string | null
  synced_at: string | null
}
export interface TopologyNode extends TopologyRecord, TopologyIdentity { label: string }
export interface TopologyEdge extends TopologyRecord {
  edge_type: EdgeType
  relation_kind: string
  from: string
  to: string
  rank: string
}
export interface Neighborhood {
  root: TopologyNode
  nodes: TopologyNode[]
  edges: TopologyEdge[]
  truncated: boolean
  limit: number
}
export type TopologyRootType = 'data_center' | 'pod'
export interface TopologyDirectoryPage { items: TopologyNode[]; next_after: string | null }
export interface TopologyPageQuery { limit: number; after?: string }
export interface TopologyRootQuery extends TopologyPageQuery { source_id: string; entity_type: TopologyRootType }
export interface TopologyChildQuery extends TopologyIdentity, TopologyPageQuery {}
export interface DeviceContext extends Neighborhood { warnings: string[] }
export interface DeviceContextQuery { source_id: string; stable_id: string }
export interface RoomPowerContext extends Neighborhood { warnings: string[]; power_missing: string[] }
export interface NeighborQuery extends TopologyIdentity { direction: Direction; edge_type?: EdgeType | '' }
export interface TopologyGraph { nodes: TopologyNode[]; edges: TopologyEdge[] }
export type TopologySelection = { kind: 'node'; id: string } | { kind: 'edge'; id: string }
