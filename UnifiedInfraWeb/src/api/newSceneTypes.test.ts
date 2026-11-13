import { expect, it } from 'vitest'
import { normalizeSceneResponse, sceneTopologyEdge, sceneTopologyNode, type SceneNode, type SceneResponse } from './newSceneTypes'

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

const compact = (id: string, entity_type: SceneNode['entity_type'] = 'device'): SceneNode => ({ id, entity_type, source_id: 's', stable_id: id, label: id, role: '', parent_type_id: null, synced_at: null })
function response(): SceneResponse {
  const root = compact('dc', 'data_center'), rack = compact('rack', 'cabinet'), device = compact('device'), gpu = compact('gpu', 'gpu'), port = compact('port', 'interface')
  return { root, nodes: [root, rack, device, gpu, port], cabinets: [{ node: rack, devices: [device], next_after: '' }], component_owners: [{ parent_id: device.id, child_ids: [gpu.id, port.id] }],
    network_levels: [{ role: 'RAW', level: 0, nodes: [device] }], unassigned: [], edges: [], next_after: '', truncated: false, warnings: [] }
}
it('retains room UUID context in a DC scene without changing cabinet authority or inventing power', () => {
  const page = response(), room = compact('room-uuid', 'room')
  page.nodes.push(room)
  page.edges.push({ id: 'room-dc', source_id: 's', edge_type: 'spatial_relation', relation_kind: 'located_in', from: room.id, to: page.root.id, rank: '1' },
    { id: 'rack-room', source_id: 's', edge_type: 'spatial_relation', relation_kind: 'located_in', from: 'rack', to: room.id, rank: '1' })
  const data = normalizeSceneResponse(page).data
  expect(data.nodes.find(node => node.id === room.id)).toMatchObject({ entity_type: 'room', stable_id: 'room-uuid' })
  expect(data.cabinetAssignments).toEqual({ device: 'rack' })
  expect(data.componentOwners).toEqual({ gpu: 'device', port: 'device' })
  expect(data.edges.map(edge => edge.id)).toEqual(['room-dc', 'rack-room'])
  expect(data.edges.some(edge => edge.edge_type === 'power_relation')).toBe(false)
})
it('normalizes authoritative hierarchy independently of edges and raw properties without mutating the DTO', () => {
  const page = response(), original = JSON.stringify(page)
  const { data, cabinetNextAfter } = normalizeSceneResponse(page)
  expect(data).toMatchObject({ cabinetAssignments: { device: 'rack' }, componentOwners: { gpu: 'device', port: 'device' }, networkLevels: { device: { role: 'RAW', level: 0 } }, assignmentStatus: {} })
  expect(data.nodes.find(item => item.id === 'device')?.properties.role).toBe('')
  expect(data.edges).toEqual([])
  expect(cabinetNextAfter).toEqual({})
  expect(JSON.stringify(page)).toBe(original)
})
it('always supplies empty authoritative maps and rejects a missing component owner contract', () => {
  const page = response()
  page.cabinets = []; page.component_owners = []; page.network_levels = []
  expect(normalizeSceneResponse(page).data).toMatchObject({ cabinetAssignments: {}, componentOwners: {}, networkLevels: {} })
  const missing: Partial<SceneResponse> = { ...page }
  delete missing.component_owners
  expect(() => normalizeSceneResponse(missing as SceneResponse)).toThrow(/组件归属契约/)
})
it('accepts unresolved room ownership while keeping its status and original context', () => {
  const page = response(), room = compact('room-uuid', 'room')
  page.nodes.push(room)
  page.unassigned.push({ node: room, status: 'ambiguous' })
  const data = normalizeSceneResponse(page).data
  expect(data.assignmentStatus?.[room.id]).toBe('ambiguous')
  expect(data.nodes.find(node => node.id === room.id)?.entity_type).toBe('room')
  expect(data.cabinetAssignments).toEqual({ device: 'rack' })
})
it('accepts unresolved data-center ownership of a cabinet without rejecting its device group', () => {
  const page = response()
  page.unassigned.push({ node: page.nodes[1]!, status: 'truncated' })
  const data = normalizeSceneResponse(page).data
  expect(data.assignmentStatus?.rack).toBe('truncated')
  expect(data.cabinetAssignments).toEqual({ device: 'rack' })
})
it('rejects conflicting parent assignments rather than choosing the first or last', () => {
  const page = response(), other = compact('other')
  page.nodes.push(other)
  page.component_owners.push({ parent_id: other.id, child_ids: ['port'] })
  expect(() => normalizeSceneResponse(page)).toThrow(/归属/)
  const racks = response(), secondRack = compact('second', 'cabinet')
  racks.nodes.push(secondRack)
  racks.cabinets.push({ node: secondRack, devices: [racks.nodes[2]!], next_after: '' })
  expect(() => normalizeSceneResponse(racks)).toThrow(/归属/)
})
it('rejects assigned nodes also declared ambiguous or truncated by backend unassigned', () => {
  for (const status of ['missing', 'ambiguous', 'truncated'] as const) {
    const page = response()
    page.unassigned.push({ node: page.nodes[3]!, status })
    expect(() => normalizeSceneResponse(page)).toThrow(/归属/)
    const cabinetPage = response()
    cabinetPage.unassigned.push({ node: cabinetPage.nodes[2]!, status })
    expect(() => normalizeSceneResponse(cabinetPage)).toThrow(/归属/)
  }
})
it('rejects unknown references, wrong parent or child types, foreign sources and conflicting node identities', () => {
  const mutations: ((page: SceneResponse) => void)[] = [
    page => { page.component_owners[0]!.parent_id = 'absent' },
    page => { page.component_owners[0]!.child_ids = ['absent'] },
    page => { page.component_owners[0]!.parent_id = 'rack' },
    page => { page.component_owners[0]!.child_ids = ['dc'] },
    page => { page.cabinets[0]!.devices = [page.nodes[3]!] },
    page => { page.cabinets[0]!.node = page.nodes[2]! },
    page => { page.nodes[3] = { ...page.nodes[3]!, source_id: 'foreign' } },
    page => { page.nodes.push({ ...page.nodes[2]!, stable_id: 'different' }) },
    page => { page.network_levels[0]!.nodes = [page.nodes[3]!] },
    page => { page.network_levels[0]!.nodes = [{ ...page.nodes[2]!, role: 'contradiction' }] },
  ]
  for (const mutate of mutations) { const page = response(); mutate(page); expect(() => normalizeSceneResponse(page)).toThrow() }
})
it('rejects contradictory or invalid numeric levels without guessing from role', () => {
  const page = response()
  page.network_levels.push({ role: 'OTHER', level: 2, nodes: [page.nodes[2]!] })
  expect(() => normalizeSceneResponse(page)).toThrow(/层级/)
  for (const level of [-1, 3, 0.5, NaN]) {
    const invalid = response(); invalid.network_levels[0]!.level = level
    expect(() => normalizeSceneResponse(invalid)).toThrow(/层级/)
  }
  const unknown = response(); unknown.network_levels[0] = { role: 'BAD', level: null, nodes: [unknown.nodes[2]!] }
  expect(normalizeSceneResponse(unknown).data.networkLevels).toEqual({ device: { role: 'BAD', level: null } })
})
