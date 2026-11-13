import { effectScope } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Neighborhood, NeighborQuery, TopologyEdge, TopologyNode } from '../api/topologyTypes'
import type { SceneQuery, SceneResponse } from '../api/newSceneTypes'
import { buildSpatialScene } from '../services/spatialScene'
import { useSpatialScene } from './useSpatialScene'

const node = (id: string, entity_type: TopologyNode['entity_type'] = 'device'): TopologyNode => ({ id, entity_type, source_id: 's', stable_id: id, label: id, properties: {}, created_at: null, synced_at: null })
const compact = (id: string, entity_type: TopologyNode['entity_type'] = 'device') => ({ id, entity_type, source_id: 's', stable_id: id, label: id, role: '', parent_type_id: null, synced_at: '2026-11-09T00:00:00Z' })
const response = (root = compact('dc', 'data_center')) => ({ root, cabinets: [], unassigned: [], component_owners: [], network_levels: [], nodes: [root], edges: [], next_after: '', truncated: false, warnings: [] })
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const scopes: ReturnType<typeof effectScope>[] = []
function setup(scene: (query: SceneQuery, signal?: AbortSignal) => Promise<SceneResponse> = vi.fn(async () => response())) {
  const root = node('dc', 'data_center')
  const api = { roots: vi.fn(async () => ({ items: [root], next_after: 'directory-cursor' })), scene,
    neighbors: vi.fn() }
  const scope = effectScope(); scopes.push(scope)
  return { state: scope.run(() => useSpatialScene(api))!, api, root, scope }
}
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); vi.unstubAllGlobals() })

describe('compact spatial scenes', () => {
  it('lists 20 roots and selects a root with exactly one scene request, no initial N+1 reads', async () => {
    const root = node('dc', 'data_center')
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 0, message: '', data: { items: [root], next_after: null } })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ code: 0, message: '', data: response() })))
    vi.stubGlobal('fetch', fetcher)
    const scope = effectScope(); scopes.push(scope)
    const state = scope.run(() => useSpatialScene())!
    await state.loadRoots('s', 'data_center')
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher).toHaveBeenLastCalledWith('/v1/inventory/topology/roots?source_id=s&entity_type=data_center&limit=20', expect.objectContaining({ method: 'GET', signal: expect.any(AbortSignal) }))
    fetcher.mockClear()
    await state.selectRoot(root)
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher).toHaveBeenCalledWith('/v1/inventory/topology/scene?source_id=s&entity_type=data_center&stable_id=dc&limit=5', expect.objectContaining({ method: 'GET', signal: expect.any(AbortSignal) }))
    expect(state.data.value.nodes.map(item => item.id)).toEqual(['dc'])
    expect(state.data.value).toMatchObject({ cabinetAssignments: {}, componentOwners: {}, networkLevels: {} })
    state.mode.value = 'network'
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('preserves hierarchy for devices beyond 16, actual edge IDs, roles and cabinet pagination', async () => {
    const root = compact('dc', 'data_center'), rack = compact('rack', 'cabinet')
    const devices = Array.from({ length: 20 }, (_, i) => ({ ...compact(`d${i}`), role: i === 19 ? 'UNRECOGNIZED' : '', parent_type_id: i === 19 ? 54 : null }))
    const page = { ...response(root), cabinets: [{ node: rack, devices, next_after: 'private-device-cursor' }], nodes: [root, rack, ...devices],
      edges: devices.map(device => ({ id: `real-${device.id}`, source_id: 's', edge_type: 'spatial_relation' as const, relation_kind: 'located_in', from: device.id, to: rack.id, rank: device.id })),
      network_levels: [{ role: 'UNRECOGNIZED', level: null, nodes: [devices[19]!] }] }
    const { state, root: selected, api } = setup(vi.fn(async () => page))
    await state.loadRoots('s', 'data_center'); await state.selectRoot(selected)
    const layout = buildSpatialScene(state.data.value, 'space')
    for (const device of devices) expect(layout.placements.find(item => item.node.id === device.id)?.parentId).toBe('rack')
    expect(state.data.value.nodes.find(item => item.id === 'd19')?.properties).toEqual({ role: 'UNRECOGNIZED', parent_type_id: 54 })
    expect(state.data.value.edges.map(item => item.id)).toEqual(devices.map(item => `real-${item.id}`))
    expect(state.cabinetNextAfter.value).toEqual({ rack: 'private-device-cursor' })
    expect(state.data.value.warnings.join(' ')).toContain('该机柜还有设备未加载')
    expect(state.data.value.warnings.join(' ')).not.toContain('private-device-cursor')
    expect(api.scene).toHaveBeenCalledTimes(1)
  })
  it('does not turn partially read ownership into confirmed cabinet membership', async () => {
    const d = compact('device'), rack = compact('rack', 'cabinet')
    const page: SceneResponse = { ...response(), nodes: [compact('dc', 'data_center'), d, rack], unassigned: [{ node: d, status: 'truncated' }], edges: [{ id: 'partial-edge', source_id: 's', edge_type: 'spatial_relation', relation_kind: 'located_in', from: d.id, to: rack.id, rank: '1' }], truncated: true }
    const { state, root } = setup(vi.fn(async () => page))
    await state.loadRoots('s', 'data_center'); await state.selectRoot(root)
    const layout = buildSpatialScene(state.data.value, 'space')
    expect(layout.placements.find(item => item.node.id === d.id)?.parentId).toBeUndefined()
    expect(layout.warnings.join(' ')).toContain('不代表没有机柜关系')
    expect(state.data.value.edges).toHaveLength(1)
  })
  it('keeps scene pagination separate and replaces rather than merges pages', async () => {
    const page = { ...response(), nodes: [compact('dc', 'data_center'), compact('first')], next_after: 'opaque-next' }
    const scene = vi.fn(async (query: SceneQuery) => query.after === 'opaque-third'
      ? { ...response(), nodes: [compact('dc', 'data_center'), compact('third')] }
      : query.after === 'opaque-next'
        ? { ...response(), nodes: [compact('dc', 'data_center'), compact('second')], next_after: 'opaque-third' }
        : page)
    const { state, root } = setup(scene)
    await state.loadRoots('s', 'data_center'); await state.selectRoot(root); await state.nextScenePage()
    expect(scene.mock.calls[1]?.[0]).toMatchObject({ after: 'opaque-next', limit: 5 })
    expect(state.sceneHistory.value).toEqual([undefined])
    expect(state.rootsNextAfter.value).toBe('directory-cursor')
    expect(state.data.value.nodes.map(item => item.id)).toEqual(['dc', 'second'])
    await state.nextScenePage()
    expect(state.sceneHistory.value).toEqual([undefined, 'opaque-next'])
    expect(state.data.value.nodes.map(item => item.id)).toEqual(['dc', 'third'])
    await state.previousScenePage()
    expect(scene.mock.calls[3]?.[0]).toMatchObject({ after: 'opaque-next', limit: 5 })
    expect(state.data.value.nodes.map(item => item.id)).toEqual(['dc', 'second'])
    await state.previousScenePage()
    expect(scene.mock.calls[4]?.[0]).not.toHaveProperty('after')
    expect(state.data.value.nodes.map(item => item.id)).toEqual(['dc', 'first'])
    expect(state.sceneHistory.value).toEqual([])
  })
  it('rejects foreign nodes and invalid root identity without retaining partial data', async () => {
    const foreign = { ...compact('foreign'), source_id: 'other' }
    const page = { ...response(), cabinets: [{ node: compact('rack', 'cabinet'), devices: [compact('device')], next_after: '' }],
      nodes: [compact('dc', 'data_center'), foreign], edges: [{ id: 'foreign-edge', source_id: 'other', edge_type: 'spatial_relation' as const, relation_kind: 'located_in', from: 'device', to: 'rack', rank: '' }] }
    const { state, root } = setup(vi.fn(async () => page))
    await state.loadRoots('s', 'data_center'); await state.selectRoot(root)
    expect(state.error.value).not.toBe('')
    expect(state.data.value.edges).toEqual([])
    expect(state.data.value.nodes).toEqual([])
    const invalid = setup(vi.fn(async () => response({ ...compact('dc', 'data_center'), source_id: 'other' })))
    await invalid.state.loadRoots('s', 'data_center'); await invalid.state.selectRoot(invalid.root)
    expect(invalid.state.error.value).not.toBe('')
    expect(invalid.state.data.value.nodes).toEqual([])
  })
  it('aborts stale requests and resets pagination for source/type changes and disposal', async () => {
    const pending = deferred<ReturnType<typeof response>>()
    const scene = vi.fn((_query: unknown, _signal?: AbortSignal) => pending.promise)
    const { state, root, scope } = setup(scene)
    await state.loadRoots('s', 'data_center')
    const loading = state.selectRoot(root)
    state.rootType.value = 'pod'
    expect(scene.mock.calls[0]?.[1]?.aborted).toBe(true)
    pending.resolve(response()); await loading
    expect(state.data.value.nodes).toEqual([])
    expect(state.sceneNextAfter.value).toBeNull(); expect(state.sceneHistory.value).toEqual([])
    scope.stop()
    expect(state.loading.value).toBe(false)
  })
  it('handles current failures without retaining prior page data', async () => {
    const scene = vi.fn().mockResolvedValueOnce({ ...response(), next_after: 'next', nodes: [compact('dc', 'data_center'), compact('old')] }).mockRejectedValueOnce(new Error('unavailable'))
    const { state, root } = setup(scene)
    await state.loadRoots('s', 'data_center'); await state.selectRoot(root); await state.nextScenePage()
    expect(state.data.value.nodes).toEqual([])
    expect(state.error.value).not.toBe(''); expect(state.loading.value).toBe(false)
  })
  it('keeps explicit network expansion bounded, source validated and based on real port edges', async () => {
    const device = { ...compact('switch'), parent_type_id: 54, role: 'T1' }
    const { state, root, api } = setup(vi.fn(async () => ({ ...response(), nodes: [compact('dc', 'data_center'), device], network_levels: [{ role: 'T1', level: 1, nodes: [device] }] })))
    const local = Array.from({ length: 10 }, (_, i) => node(`p${i}`))
    const remote = Array.from({ length: 10 }, (_, i) => node(`r${i}`))
    local.forEach(port => { port.entity_type = 'interface' }); remote.forEach(port => { port.entity_type = 'interface' })
    const edge = (id: string, from: string, to: string, relation_kind: string, edge_type: TopologyEdge['edge_type']): TopologyEdge =>
      ({ id, source_id: 's', from, to, relation_kind, edge_type, rank: id, properties: {}, created_at: null, synced_at: null })
    api.neighbors.mockImplementation(async (query: NeighborQuery): Promise<Neighborhood> => {
      const index = Number(query.stable_id.slice(1))
      if (query.entity_type === 'device') return { root: node('switch'), nodes: local, edges: local.map(port => edge(`own-${port.id}`, 'switch', port.id, 'owns_interface', 'composition_relation')), truncated: false, limit: 100 }
      if (query.edge_type === 'network_relation') return { root: local[index]!, nodes: [remote[index]!], edges: [edge(`physical-${index}`, remote[index]!.id, local[index]!.id, 'links_to', 'network_relation')], truncated: false, limit: 100 }
      return { root: remote[index]!, nodes: [node('owner')], edges: [edge(`remote-owner-${index}`, 'owner', remote[index]!.id, 'owns_interface', 'composition_relation')], truncated: true, limit: 100 }
    })
    await state.loadRoots('s', 'data_center'); await state.selectRoot(root)
    expect(api.neighbors).not.toHaveBeenCalled()
    await state.expandNetwork(state.data.value.nodes.find(item => item.id === 'switch')!)
    expect(api.neighbors).toHaveBeenCalledTimes(8)
    expect(state.data.value.edges.filter(item => item.edge_type === 'network_relation')).toHaveLength(7)
    expect(state.data.value.edges.some(item => item.from === 'switch' && item.to === 'owner')).toBe(false)
    expect(state.data.value.assignmentStatus?.r0).toBe('unknown')
    expect(state.data.value.assignmentStatus?.p0).toBe('unknown')
    expect(state.data.value.componentOwners).toEqual({})
    expect(state.data.value.cabinetAssignments).toEqual({})
    expect(state.data.value.networkLevels).toEqual({ switch: { role: 'T1', level: 1 } })
    expect(state.data.value.nodes.some(item => item.id === 'owner')).toBe(false)
    expect(buildSpatialScene(state.data.value, 'network').placements.find(item => item.node.id === 'r0')?.parentId).toBeUndefined()
    expect(state.data.value.warnings.join(' ')).toContain('手动展开端点层级未确认，请重载')
    expect(state.data.value.truncated).toBe(true)
  })
})
