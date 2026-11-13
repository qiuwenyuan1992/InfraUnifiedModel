import { describe, expect, it } from 'vitest'
import { mergeNeighborhood, planeLabel, propertyText, validIdentity, topologyDisplayName, directoryCountSummary, deviceCardSummary } from './topology'
import type { TopologyEdge, TopologyNode } from '../api/topologyTypes'
import { phaseNames } from './display'

const node = (id: string): TopologyNode => ({ id, source_id: 'source', entity_type: 'device', stable_id: id, label: id, properties: {}, created_at: null, synced_at: null })
const edge = (id: string): TopologyEdge => ({ id, source_id: 'source', edge_type: 'network_relation', relation_kind: 'links_to', from: 'a', to: 'b', rank: '9223372036854775807', properties: {}, created_at: null, synced_at: null })

describe('topology atomic merge', () => {
  it('labels all 16 sync phases including room and retains room UUID identity', () => {
    expect(Object.keys(phaseNames)).toHaveLength(16)
    expect(phaseNames.room).toBe('房间')
    const room: TopologyNode = { ...node('room-uuid'), entity_type: 'room', label: '' }
    expect(topologyDisplayName(room)).toBe('房间')
    expect(validIdentity('room', 'room-uuid')).toBe(true)
    expect(validIdentity('room', ' ')).toBe(false)
    expect(room.stable_id).toBe('room-uuid')
  })
  it('displays data center Chinese name and code without changing identity', () => {
    const dc: TopologyNode = { ...node('dc'), entity_type: 'data_center', label: 'CHJL', properties: { cn_name: '测试数据中心', code: 'CHJL' } }
    expect(topologyDisplayName(dc)).toBe('测试数据中心（CHJL）')
    expect(topologyDisplayName({ ...dc, properties: { cn_name: '', code: 'CHJL' } })).toBe('CHJL')
    expect(topologyDisplayName({ ...dc, properties: { cn_name: 'CHJL', code: 'CHJL' } })).toBe('CHJL')
    expect(topologyDisplayName(node('device'))).toBe('device')
  })
  it('deduplicates by actual ID and preserves parallel relations and string rank', () => {
    const graph = { nodes: [node('a')], edges: [] }
    const result = mergeNeighborhood(graph, { root: node('a'), nodes: [node('a'), node('b')], edges: [edge('one'), edge('two'), edge('one')], truncated: false, limit: 100 })
    expect(result.nodes).toHaveLength(2)
    expect(result.edges.map(item => item.id)).toEqual(['one', 'two'])
    expect(result.edges[0]!.rank).toBe('9223372036854775807')
    expect(graph.nodes).toHaveLength(1)
  })
  it('refuses the entire merge when either budget would be exceeded', () => {
    const graph = { nodes: [node('a'), node('b'), ...Array.from({ length: 498 }, (_, index) => node(`node-${index}`))], edges: Array.from({ length: 1000 }, (_, index) => edge(`edge-${index}`)) }
    const incoming = { root: node('a'), nodes: [node('c')], edges: [edge('new')], truncated: true, limit: 100 }
    expect(() => mergeNeighborhood(graph, { ...incoming, edges: [] })).toThrow('500')
    expect(() => mergeNeighborhood(graph, { ...incoming, nodes: [] })).toThrow('1000')
    expect(graph.edges).toHaveLength(1000)
    expect(graph.nodes).toHaveLength(500)
    expect(mergeNeighborhood(graph, { ...incoming, nodes: [], edges: [edge('edge-0')] })).toEqual(graph)
  })
  it('preserves property nulls, lists, booleans, unsafe integer strings and plane labels', () => {
    expect(propertyText(null)).toBe('null')
    expect(propertyText(false)).toBe('false')
    expect(propertyText(['9223372036854775807', null, true])).toBe('[\n  "9223372036854775807",\n  null,\n  true\n]')
    expect(planeLabel(2)).toBe('计算平面（2）')
    expect(planeLabel(99)).toBe('99')
    expect(validIdentity('ups', '9223372036854775807')).toBe(true)
  })
  it('summarizes source counts for data center and room cards without exposing UUIDs', () => {
    const room = { ...node('room-uuid'), entity_type: 'room' as const, properties: { cabinet_num: 90, server_num: 36, network_device_num: 54 } }
    expect(directoryCountSummary(room)).toBe('机柜 90 · 服务器 36 · 网络设备 54')
    const partial = { ...room, properties: { cabinet_num: 90, server_num: null } }
    expect(directoryCountSummary(partial)).toBe('机柜 90 · 服务器 — · 网络设备 —')
    const empty = { ...room, properties: {} }
    expect(directoryCountSummary(empty)).toBe('机柜 — · 服务器 — · 网络设备 —')
  })
  it('summarizes cabinet cards with device counts instead of UUIDs', () => {
    const cabinet = { ...node('cabinet-uuid'), entity_type: 'cabinet' as const, properties: { device_num: 11, server_num: 9, network_device_num: 2 } }
    expect(directoryCountSummary(cabinet)).toBe('设备 11 · 服务器 9 · 网络设备 2')
    const empty = { ...cabinet, properties: {} }
    expect(directoryCountSummary(empty)).toBe('设备 — · 服务器 — · 网络设备 —')
  })
  it('summarizes device cards with type, IPs, and network role', () => {
    const gpu = { ...node('SN-1'), entity_type: 'device' as const, properties: { parent_type_id: 52, device_type_id: 58, all_ips: ['10.0.0.1', '10.0.0.2', '10.0.0.3'] } }
    expect(deviceCardSummary(gpu)).toBe('GPU服务器 · IP 10.0.0.1, 10.0.0.2 等3个')
    const switchNode = { ...node('SN-2'), entity_type: 'device' as const, properties: { parent_type_id: 54, device_type_id: 188, all_ips: ['10.1.0.1'], role: ' T0 ' } }
    expect(deviceCardSummary(switchNode)).toBe('框式交换机 · IP 10.1.0.1 · T0')
    const server = { ...node('SN-3'), entity_type: 'device' as const, properties: { parent_type_id: 52, device_type_id: 57, all_ips: [], role: 'T1' } }
    expect(deviceCardSummary(server)).toBe('通用服务器')
    const unknown = { ...node('SN-4'), entity_type: 'device' as const, properties: { parent_type_id: 52, device_type_id: 999 } }
    expect(deviceCardSummary(unknown)).toBe('类型 999')
    const bare = { ...node('SN-5'), entity_type: 'device' as const, properties: {} }
    expect(deviceCardSummary(bare)).toBe('—')
  })
})
