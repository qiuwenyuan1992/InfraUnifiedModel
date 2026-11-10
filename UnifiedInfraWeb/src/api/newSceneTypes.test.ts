import { expect, it } from 'vitest'
import { sceneTopologyEdge, sceneTopologyNode } from './newSceneTypes'

it('converts compact nodes without guessing roles or exposing full properties', () => {
  const node = { id: 'n', source_id: 's', entity_type: 'device' as const, stable_id: 'sn', label: 'switch T2',
    role: 'unknown-role', parent_type_id: 54, synced_at: 'actual-time', properties: { secret: 'must-not-copy', role: 'T2' }, created_at: 'must-not-copy' }
  expect(sceneTopologyNode(node)).toEqual({ id: 'n', source_id: 's', entity_type: 'device', stable_id: 'sn', label: 'switch T2',
    properties: { role: 'unknown-role', parent_type_id: 54 }, created_at: null, synced_at: 'actual-time' })
})
it('converts real edges with only optional power_path and plane attributes', () => {
  const edge = { id: 'real', source_id: 's', edge_type: 'power_relation' as const, relation_kind: 'power_upstream',
    from: 'a', to: 'b', rank: 'rank', power_path: 'A', plane: 'physical', properties: { secret: 'must-not-copy' } }
  expect(sceneTopologyEdge(edge)).toEqual({ id: 'real', source_id: 's', edge_type: 'power_relation', relation_kind: 'power_upstream',
    from: 'a', to: 'b', rank: 'rank', properties: { power_path: 'A', plane: 'physical' }, created_at: null, synced_at: null })
  expect(sceneTopologyEdge({ ...edge, power_path: undefined, plane: undefined }).properties).toEqual({})
})
