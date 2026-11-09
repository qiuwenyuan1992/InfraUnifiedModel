import { describe, expect, it } from 'vitest'
import { mergeNeighborhood, planeLabel, propertyText, validIdentity, topologyDisplayName } from './topology'
import type { TopologyEdge, TopologyNode } from '../api/topologyTypes'

const node = (id: string): TopologyNode => ({ id, source_id: 'source', entity_type: 'device', stable_id: id, label: id, properties: {}, created_at: null, synced_at: null })
const edge = (id: string): TopologyEdge => ({ id, source_id: 'source', edge_type: 'network_relation', relation_kind: 'links_to', from: 'a', to: 'b', rank: '9223372036854775807', properties: {}, created_at: null, synced_at: null })

describe('topology atomic merge', () => {
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
})
