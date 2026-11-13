import { describe, expect, it } from 'vitest'
import * as THREE from 'three'
import { buildSpatialObjects, disposeSpatialObjects } from './spatialThreeRenderer'
import type { TopologyEdge, TopologyNode } from '../api/topologyTypes'
import { buildSpatialScene, type SpatialSceneData } from './spatialScene'

function node(id: string, entity_type: TopologyNode['entity_type'], properties: TopologyNode['properties'] = {}): TopologyNode {
  return { id, entity_type, properties, source_id: 's', stable_id: id, label: id, created_at: null, synced_at: null }
}
function edge(id: string, from: string, to: string, relation_kind: string, edge_type: TopologyEdge['edge_type'] = 'spatial_relation'): TopologyEdge {
  return { id, from, to, relation_kind, edge_type, source_id: 's', rank: id, properties: {}, created_at: null, synced_at: null }
}
function scene(nodes: TopologyNode[], edges: TopologyEdge[] = [], authority: Pick<SpatialSceneData, 'cabinetAssignments' | 'componentOwners' | 'networkLevels' | 'assignmentStatus'> = {}): SpatialSceneData {
  return { nodes, edges, root: null, truncated: false, warnings: [], cabinetAssignments: {}, componentOwners: {}, networkLevels: {}, ...authority }
}
describe('spatial scene evidence-only layout', () => {
  it('retains rooms as background context and preserves real spatial edges without fake power', () => {
    const dc = node('dc', 'data_center'), room = node('room-uuid', 'room'), rack = node('rack', 'cabinet'), device = node('device', 'device')
    const edges = [edge('room-dc', room.id, dc.id, 'located_in'), edge('rack-room', rack.id, room.id, 'located_in'), edge('device-rack', device.id, rack.id, 'located_in')]
    const layout = buildSpatialScene({ ...scene([dc, room, rack, device], edges, { cabinetAssignments: { device: 'rack' } }), root: dc }, 'space')
    expect(layout.placements.find(item => item.node.id === room.id)?.kind).toBe('background')
    expect(layout.placements.find(item => item.node.id === device.id)?.parentId).toBe(rack.id)
    expect(layout.edges.map(item => item.id).sort()).toEqual(edges.map(item => item.id).sort())
    expect(layout.edges.some(item => item.edge_type === 'power_relation')).toBe(false)
  })
  it('does not arbitrarily pick a cabinet when a device has conflicting spatial parents', () => {
    const d = node('device', 'device'), a = node('a', 'cabinet'), b = node('b', 'cabinet')
    const layout = buildSpatialScene(scene([d, a, b], [edge('a-edge', d.id, a.id, 'located_in'), edge('b-edge', d.id, b.id, 'located_in')], { assignmentStatus: { device: 'ambiguous' } }), 'space')
    expect(layout.placements.find(item => item.node.id === d.id)?.parentId).toBeUndefined()
    expect(layout.warnings.join(' ')).toContain('多个')
  })
  it('uses backend numeric tiers and preserves unknown roles without classifying from properties', () => {
    const values = [node('t2', 'device', { role: 'T2', parent_type_id: 54 }), node('t1', 'device', { role: 'T1', parent_type_id: '54' }),
      node('t0', 'device', { role: 'T0', parent_type_id: 54 }), node('Core-T2', 'device', { role: 'custom', parent_type_id: 54 }),
      node('T2-in-name', 'device', { parent_type_id: 54 }), node('server', 'device', { role: 'T2', parent_type_id: 1 })]
    const layout = buildSpatialScene(scene(values, [], { networkLevels: {
      t2: { role: 'T2', level: 0 }, t1: { role: 'T1', level: 1 }, t0: { role: 'T0', level: 2 },
      'Core-T2': { role: 'custom', level: null }, 'T2-in-name': { role: '', level: null },
    } }), 'network')
    const byId = new Map(layout.placements.map(item => [item.node.id, item]))
    expect(byId.get('t2')).toMatchObject({ y: 8, tier: 0, role: 'T2' })
    expect(byId.get('t1')?.y).toBe(16)
    expect(byId.get('t0')).toMatchObject({ y: 24, tier: 2, role: 'T0' })
    expect(byId.get('Core-T2')).toMatchObject({ y: 4, role: 'custom', kind: 'network' })
    expect(byId.get('T2-in-name')).toMatchObject({ y: 4, role: '' })
    expect(byId.get('server')?.kind).toBe('device')
    expect(layout.warnings.join(' ')).toContain('未分类')
  })
  it('aligns network tiers independently of cabinet positions and never renders a switch as GPU', () => {
    const rackA = node('rack-a', 'cabinet'), rackB = node('rack-b', 'cabinet')
    const low = node('210235A53L5264L100BY', 'device', { role: 'T0', parent_type_id: 54 })
    const high = node('high', 'device', { role: 'T2', parent_type_id: 54 })
    const layout = buildSpatialScene(scene([rackA, rackB, low, high], [edge('low-rack', low.id, rackA.id, 'located_in'), edge('high-rack', high.id, rackB.id, 'located_in')], {
      cabinetAssignments: { [low.id]: rackA.id, [high.id]: rackB.id }, networkLevels: { [low.id]: { role: 'T0', level: 0 }, [high.id]: { role: 'T2', level: 2 } },
    }), 'network')
    const a = layout.placements.find(p => p.node.id === low.id)!, b = layout.placements.find(p => p.node.id === high.id)!
    expect(a.kind).toBe('network'); expect(a.kind).not.toBe('gpu')
    expect(a.parentId).toBeUndefined(); expect(b.parentId).toBeUndefined()
    expect(a.x).toBe(b.x); expect(a.z).toBe(b.z); expect(a.y).toBe(8); expect(b.y).toBe(24)
  })
  it('uses backend cabinet assignments and puts unassigned devices in the orphan area', () => {
    const cabinet = node('rack', 'cabinet')
    const device = node('device', 'device', { cabinet_id: 'rack', u_position: 42 })
    const orphan = node('orphan', 'device', { cabinet_id: 'rack' })
    const layout = buildSpatialScene(scene([cabinet, device, orphan], [edge('real', device.id, cabinet.id, 'located_in')], { cabinetAssignments: { device: 'rack' } }), 'space')
    const linked = layout.placements.find(item => item.node.id === device.id)!
    const unlinked = layout.placements.find(item => item.node.id === orphan.id)!
    expect(linked.parentId).toBe(cabinet.id)
    expect(linked.y).toBe(1)
    expect(unlinked.parentId).toBeUndefined()
    expect(unlinked.y).toBe(1)
    expect(unlinked.x).toBeGreaterThan(linked.x + 10)
    expect(layout.warnings.join(' ')).toContain('未核验')
    expect(layout.warnings.join(' ')).not.toContain('无机柜关系')
    expect(layout.warnings.join(' ')).toContain('不是实际 U 位')
  })
  it('shows GPU hardware only when backend component ownership exists, not gpu counts', () => {
    const device = node('server', 'device', { gpu_count: 8 })
    const gpu = node('gpu', 'gpu')
    const unowned = node('other', 'gpu')
    const layout = buildSpatialScene(scene([device, gpu, unowned], [edge('gpu-edge', device.id, gpu.id, 'contains_gpu', 'composition_relation')], { componentOwners: { gpu: 'server' } }), 'space')
    expect(layout.placements.filter(item => item.kind === 'gpu').map(item => item.node.id)).toEqual(['gpu'])
    expect(layout.placements.find(item => item.node.id === gpu.id)?.parentId).toBe(device.id)
    expect(buildSpatialScene(scene([device]), 'space').placements).toHaveLength(1)
  })
  it('places actual GPU bounds above the opaque owner without moving owned interfaces', () => {
    const nodes = [node('rack', 'cabinet'), node('server', 'device'), node('gpu', 'gpu'), node('port', 'interface')]
    const edges = [edge('location', 'server', 'rack', 'located_in'),
      edge('contains', 'server', 'gpu', 'contains_gpu', 'composition_relation'),
      edge('owns', 'server', 'port', 'owns_interface', 'composition_relation')]
    for (const mode of ['space', 'network'] as const) {
      const layout = buildSpatialScene(scene(nodes, edges, { cabinetAssignments: { server: 'rack' }, componentOwners: { gpu: 'server', port: 'server' } }), mode)
      const objects = buildSpatialObjects(layout, mode)
      objects.root.updateMatrixWorld(true)
      const ownerMesh = objects.nodes.get('server')!.children.find(child => child instanceof THREE.Mesh)!
      const gpuMesh = objects.nodes.get('gpu')!.children.find(child => child instanceof THREE.Mesh)!
      const ownerBounds = new THREE.Box3().setFromObject(ownerMesh)
      const gpuBounds = new THREE.Box3().setFromObject(gpuMesh)
      expect(gpuBounds.min.y).toBeGreaterThan(ownerBounds.max.y)
      expect(gpuBounds.intersectsBox(ownerBounds)).toBe(false)
      expect(layout.warnings.join(' ')).toContain('示意布局')
      if (mode === 'network') {
        expect(layout.placements.find(item => item.node.id === 'port')!.y)
          .toBe(layout.placements.find(item => item.node.id === 'server')!.y)
      }
      disposeSpatialObjects(objects.root)
    }
  })
  it('preserves parallel network IDs, keeps ownership bridges, and never fabricates cabinet links or mutates input', () => {
    const nodes = [node('rack', 'cabinet'), node('switch', 'device', { role: 'T0', parent_type_id: 54 }), node('server', 'device')]
    const edges = [edge('location', 'switch', 'rack', 'located_in'), edge('net-a', 'server', 'switch', 'server_uplink', 'network_relation'),
      edge('net-b', 'server', 'switch', 'server_uplink', 'network_relation')]
    const input = scene(nodes, edges, { cabinetAssignments: { switch: 'rack' }, networkLevels: { switch: { role: 'T0', level: 0 } } })
    input.truncated = true
    input.warnings = ['真实告警']
    const original = JSON.stringify(input)
    nodes.forEach(Object.freeze)
    edges.forEach(Object.freeze)
    Object.freeze(nodes)
    Object.freeze(edges)
    const layout = buildSpatialScene(input, 'network')
    expect(layout.edges.map(item => item.id)).toEqual(['net-a', 'net-b'])
    expect(layout.edges[0]).toBe(edges[1])
    expect(layout.warnings).toContain('真实告警')
    expect(layout.warnings.join(' ')).toContain('部分数据')
    expect(JSON.stringify(input)).toBe(original)
  })
  it('preserves real links when a remote port owner is unavailable without guessing the owner', () => {
    const input = scene([node('p1', 'interface'), node('p2', 'interface')], [edge('real-link', 'p1', 'p2', 'links_to', 'network_relation')])
    const layout = buildSpatialScene(input, 'network')
    expect(layout.edges.map(item => item.id)).toEqual(['real-link'])
    expect(layout.placements).toHaveLength(2)
    expect(layout.placements.every(item => !item.parentId)).toBe(true)
    expect(layout.warnings.join(' ')).toContain('端口设备归属未核验')
  })
  it('places linked interfaces under backend owners so original physical edges have renderable endpoints', () => {
    const nodes = [node('a', 'device', { parent_type_id: 54 }), node('b', 'device', { parent_type_id: 54 }), node('p1', 'interface'), node('p2', 'interface')]
    const edges = [edge('o1', 'a', 'p1', 'owns_interface', 'composition_relation'), edge('o2', 'b', 'p2', 'owns_interface', 'composition_relation'),
      edge('link', 'p1', 'p2', 'links_to', 'network_relation')]
    const layout = buildSpatialScene(scene(nodes, edges, { componentOwners: { p1: 'a', p2: 'b' }, networkLevels: { a: { role: '', level: null }, b: { role: '', level: null } } }), 'network')
    expect(layout.edges.map(item => item.id)).toEqual(['o1', 'o2', 'link'])
    expect(layout.placements.find(item => item.node.id === 'p1')?.parentId).toBe('a')
  })
  it('never derives membership or network classification when authority maps are absent or empty', () => {
    const nodes = [node('rack', 'cabinet'), node('switch', 'device', { role: 'T2', parent_type_id: 54 }), node('gpu', 'gpu')]
    const edges = [edge('rack-edge', 'switch', 'rack', 'located_in'), edge('gpu-edge', 'switch', 'gpu', 'contains_gpu', 'composition_relation')]
    for (const maps of [{}, { cabinetAssignments: undefined, componentOwners: undefined, networkLevels: undefined }]) {
      const layout = buildSpatialScene(scene(nodes, edges, maps), 'network')
      expect(layout.placements.find(item => item.node.id === 'switch')).toMatchObject({ kind: 'device' })
      expect(layout.placements.find(item => item.node.id === 'switch')?.parentId).toBeUndefined()
      expect(layout.placements.some(item => item.node.id === 'gpu')).toBe(false)
    }
  })
  it('keeps authoritative parents and raw role-level groups despite competing edges and properties', () => {
    const nodes = [node('a', 'cabinet'), node('b', 'cabinet'), node('one', 'device', { role: 'T2', parent_type_id: 1 }), node('two', 'device'), node('gpu', 'gpu')]
    const edges = [edge('location', 'one', 'b', 'located_in'), edge('owner', 'two', 'gpu', 'contains_gpu', 'composition_relation')]
    const authority = { cabinetAssignments: { one: 'a' }, componentOwners: { gpu: 'one' }, networkLevels: { one: { role: 'RAW', level: 1 }, two: { role: 'OTHER', level: 1 } } }
    for (const ordered of [edges, [...edges].reverse()]) {
      const spatial = buildSpatialScene(scene(nodes, ordered, authority), 'space')
      expect(spatial.placements.find(item => item.node.id === 'one')?.parentId).toBe('a')
      expect(spatial.placements.find(item => item.node.id === 'gpu')?.parentId).toBe('one')
      const network = buildSpatialScene(scene(nodes, ordered, authority), 'network')
      expect(network.placements.find(item => item.node.id === 'one')).toMatchObject({ role: 'RAW', tier: 1, y: 16, kind: 'network', x: 0 })
      expect(network.placements.find(item => item.node.id === 'two')).toMatchObject({ role: 'OTHER', tier: 1, y: 16, x: 0 })
      nodes.find(node => node.id === 'one')!.properties = { role: 'T0', parent_type_id: 54 }
    }
  })
})
