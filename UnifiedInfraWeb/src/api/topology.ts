import { inventoryRequest } from './inventory'
import type { DeviceContext, DeviceContextQuery, Neighborhood, NeighborQuery, TopologyChildQuery, TopologyDirectoryPage, TopologyIdentity, TopologyNode, TopologyRootQuery } from './topologyTypes'

function query(identity: Partial<TopologyIdentity>, options: Record<string, string | number | undefined> = {}) {
  const params = new URLSearchParams()
  const values = { source_id: identity.source_id, entity_type: identity.entity_type, stable_id: identity.stable_id, ...options }
  for (const [key, value] of Object.entries(values)) {
    if (value !== undefined && value !== '') params.set(key, String(value))
  }
  return params.toString()
}
export const topologyDirectoryApi = {
  roots: (identity: TopologyRootQuery, signal?: AbortSignal) =>
    inventoryRequest<TopologyDirectoryPage>(`/topology/roots?${query(identity, { limit: identity.limit, after: identity.after })}`, { method: 'GET', signal }),
  children: (identity: TopologyChildQuery, signal?: AbortSignal) =>
    inventoryRequest<TopologyDirectoryPage>(`/topology/children?${query(identity, { limit: identity.limit, after: identity.after })}`, { method: 'GET', signal }),
  deviceContext: (identity: DeviceContextQuery, signal?: AbortSignal) =>
    inventoryRequest<DeviceContext>(`/topology/device-context?${query(identity)}`, { method: 'GET', signal }),
}
export const topologyApi = {
  node: (identity: TopologyIdentity, signal?: AbortSignal) =>
    inventoryRequest<TopologyNode>(`/topology/node?${query(identity)}`, { method: 'GET', signal }),
  neighbors: (identity: NeighborQuery, signal?: AbortSignal) =>
    inventoryRequest<Neighborhood>(`/topology/neighbors?${query(identity, { limit: 100, direction: identity.direction, edge_type: identity.edge_type })}`, { method: 'GET', signal }),
}
