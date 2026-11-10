import { describe, expect, it } from 'vitest'
import type { EntityType, TopologyEdge, TopologyGraph, TopologyNode } from '../api/topologyTypes'
import { buildDeviceView, type DeviceViewRect } from './deviceView'

const expectInside = (inner: DeviceViewRect, outer: DeviceViewRect) => {
  expect(inner.x).toBeGreaterThanOrEqual(outer.x)
  expect(inner.y).toBeGreaterThanOrEqual(outer.y)
  expect(inner.x + inner.width).toBeLessThanOrEqual(outer.x + outer.width)
  expect(inner.y + inner.height).toBeLessThanOrEqual(outer.y + outer.height)
}

const node = (id: string, entity_type: EntityType = 'device'): TopologyNode => ({ id, source_id: 'source', stable_id: id, entity_type, label: id, properties: {}, created_at: null, synced_at: null })
const edge = (id: string, from: string, to: string, relation_kind = 'contains_gpu', edge_type: TopologyEdge['edge_type'] = 'composition_relation'): TopologyEdge => ({ id, from, to, relation_kind, edge_type, source_id: 'source', rank: '0', properties: {}, created_at: null, synced_at: null })
const graphWithGPUs = (count: number): TopologyGraph => ({ nodes: [node('server'), ...Array.from({ length: count }, (_, i) => node(`gpu-${String(i).padStart(2, '0')}`, 'gpu'))], edges: Array.from({ length: count }, (_, i) => edge(`contains-${i}`, 'server', `gpu-${String(i).padStart(2, '0')}`)) })

it('preserves original records and does not mutate frozen inputs', () => {
  const graph = graphWithGPUs(8)
  const before = JSON.stringify(graph)
  graph.nodes.forEach(item => { Object.freeze(item.properties); Object.freeze(item) })
  graph.edges.forEach(item => { Object.freeze(item.properties); Object.freeze(item) })
  Object.freeze(graph.nodes); Object.freeze(graph.edges); Object.freeze(graph)
  const layout = buildDeviceView(graph, 'server')
  expect(JSON.stringify(graph)).toBe(before)
  expect(layout.nodes.map(item => item.node).sort((a, b) => a.id.localeCompare(b.id))).toEqual([...graph.nodes].sort((a, b) => a.id.localeCompare(b.id)))
  layout.nodes.forEach(item => expect(item.node).toBe(graph.nodes.find(node => node.id === item.node.id)))
  layout.edges.forEach(item => expect(item.edge).toBe(graph.edges.find(edge => edge.id === item.edge.id)))
})

it('contains only GPUs owned by outgoing actual root contains_gpu edges', () => {
  const graph: TopologyGraph = { nodes: [node('server'), node('other'), node('owned', 'gpu'), node('foreign', 'gpu'), node('reverse', 'gpu'), node('wrong-kind', 'gpu')], edges: [edge('own', 'server', 'owned'), edge('foreign-edge', 'other', 'foreign'), edge('reverse-edge', 'reverse', 'server'), edge('wrong', 'server', 'wrong-kind', 'gpu_uplink', 'network_relation')] }
  const layout = buildDeviceView(graph, 'server')
  expectInside(layout.nodes.find(item => item.node.id === 'owned')!, layout.container)
  const other = layout.containers.find(item => item.id === 'other')!
  expectInside(layout.nodes.find(item => item.node.id === 'foreign')!, other)
  expect(layout.containers.find(item => item.id === 'server')!.relationIds).toEqual(['own'])
  expect(layout.edges.filter(item => item.contained).map(item => item.edge.id)).toEqual(['foreign-edge', 'own'])
  expect(layout.nodes).toHaveLength(graph.nodes.length)
  expect(layout.edges).toHaveLength(graph.edges.length)
})

describe.each([0, 1, 8, 16])('%i actual GPUs', count => {
  it('lays out bounded non-overlapping GPU cards in one horizontal row', () => {
    const layout = buildDeviceView(graphWithGPUs(count), 'server')
    const cards = layout.nodes.filter(item => item.group === 'gpu')
    expect(cards).toHaveLength(count)
    expect(new Set(cards.map(item => item.x)).size).toBe(count)
    expect(new Set(cards.map(item => item.y)).size).toBe(count ? 1 : 0)
    cards.slice(1).forEach((card, index) => expect(card.x).toBeGreaterThan(cards[index]!.x + cards[index]!.width))
    for (const card of cards) {
      expect(card.x).toBeGreaterThanOrEqual(layout.container.x)
      expect(card.y).toBeGreaterThan(layout.container.y)
      expect(card.x + card.width).toBeLessThanOrEqual(layout.container.x + layout.container.width)
      expect(card.y + card.height).toBeLessThanOrEqual(layout.container.y + layout.container.height)
    }
    for (const item of layout.nodes) {
      expect(item.x).toBeGreaterThanOrEqual(0); expect(item.y).toBeGreaterThanOrEqual(0)
      expect(item.x + item.width).toBeLessThanOrEqual(layout.width)
      expect(item.y + item.height).toBeLessThanOrEqual(layout.height)
    }
    expect(new Set(cards.map(item => `${item.x},${item.y}`)).size).toBe(count)
  })
})

it('renders separate deterministic directed paths and label offsets for parallel A/B IDs', () => {
  const graph: TopologyGraph = { nodes: [node('server'), node('cab', 'cabinet'), node('rpp', 'rpp')], edges: [{ ...edge('B', 'cab', 'rpp', 'power_upstream', 'power_relation'), properties: { power_path: 'B' } }, { ...edge('A', 'cab', 'rpp', 'power_upstream', 'power_relation'), properties: { power_path: 'A' } }, edge('space', 'server', 'cab', 'located_in', 'spatial_relation')] }
  const layout = buildDeviceView(graph, 'server')
  expect(layout.edges).toHaveLength(3)
  expect(layout.edges[0]!.path).not.toBe(layout.edges[1]!.path)
  expect(layout.edges[0]!.labelY).not.toBe(layout.edges[1]!.labelY)
  layout.edges.forEach(item => expect(item.path).toMatch(/^M /))
  expect(buildDeviceView({ nodes: [...graph.nodes].reverse(), edges: [...graph.edges].reverse() }, 'server')).toEqual(layout)
  expect(layout.edges.filter(item => item.edge.from === 'server' && item.edge.edge_type === 'power_relation')).toHaveLength(0)
})

it('places a shared UPS group only once and real UPS members beside their owner group', () => {
  const graph: TopologyGraph = { nodes: [node('server'), node('t', 'transformer'), node('group', 'ups_group'), node('ups1', 'ups'), node('ups2', 'ups'), node('rpp1', 'rpp'), node('rpp2', 'rpp'), node('cab', 'cabinet')], edges: [edge('u1', 'ups1', 'group', 'member_of', 'power_relation'), edge('u2', 'ups2', 'group', 'member_of', 'power_relation'), edge('p1', 'rpp1', 'group', 'power_upstream', 'power_relation'), edge('p2', 'rpp2', 'group', 'power_upstream', 'power_relation')] }
  const layout = buildDeviceView(graph, 'server')
  const group = layout.nodes.find(item => item.node.id === 'group')!
  expect(layout.nodes.filter(item => item.node.id === 'group')).toHaveLength(1)
  expect(layout.nodes.find(item => item.node.id === 't')!.x).toBeLessThan(group.x)
  for (const id of ['ups1', 'ups2']) {
    const ups = layout.nodes.find(item => item.node.id === id)!
    expectInside(ups, layout.containers.find(item => item.id === 'group')!)
    expect(ups.y).toBeGreaterThan(group.y + group.height)
  }
  expect(layout.containers.some(item => item.id === 'cab')).toBe(false)
})

it('places only exact directed interface owners in the network column', () => {
  const graph: TopologyGraph = { nodes: [node('server'), node('gpu', 'gpu'), node('port1', 'interface'), node('port2', 'interface'), node('switch'), node('reversed')], edges: [edge('gpu', 'server', 'gpu'), edge('uplink1', 'server', 'port1', 'server_uplink', 'network_relation'), edge('uplink2', 'gpu', 'port2', 'gpu_uplink', 'network_relation'), edge('owns1', 'switch', 'port1', 'owns_interface'), edge('owns2', 'switch', 'port2', 'owns_interface'), edge('reverse', 'port1', 'reversed', 'owns_interface')] }
  const layout = buildDeviceView(graph, 'server')
  expect(layout.nodes.find(item => item.node.id === 'switch')!.group).toBe('network')
  expect(layout.nodes.find(item => item.node.id === 'reversed')!.group).toBe('background')
  const owner = layout.nodes.find(item => item.node.id === 'switch')!
  const ports = layout.nodes.filter(item => item.group === 'interface')
  const ownerContainer = layout.containers.find(item => item.id === 'switch')!
  ports.forEach(port => {
    expect(port.x).toBeGreaterThan(layout.container.x + layout.container.width)
    expectInside(port, ownerContainer)
    expect(port.y).toBeGreaterThan(owner.y + owner.height)
  })
  expect(ownerContainer.relationIds).toEqual(['owns1', 'owns2'])
  expect(layout.edges.map(item => item.edge.id).sort()).toEqual(graph.edges.map(item => item.id).sort())
})

it('puts actual DC/POD background above the server and returns empty when root is absent', () => {
  const graph: TopologyGraph = { nodes: [node('server'), node('dc', 'data_center'), node('pod', 'pod')], edges: [] }
  const layout = buildDeviceView(graph, 'server')
  expect(layout.nodes.filter(item => item.node.id !== 'server').every(item => item.y < layout.container.y)).toBe(true)
  const missing = buildDeviceView(graph, 'missing')
  expect(missing.nodes).toEqual([]); expect(missing.edges).toEqual([])
  expect(missing.container).toEqual({ x: 0, y: 0, width: 0, height: 0 })
  expect(missing.width).toBeGreaterThan(0); expect(missing.height).toBeGreaterThan(0)
})

it('orders interface owner groups by stable identity rather than opaque graph IDs', () => {
  const graph: TopologyGraph = { nodes: [node('server'), { ...node('owner-z'), stable_id: 'A' }, { ...node('owner-a'), stable_id: 'Z' }, node('port-z', 'interface'), node('port-a', 'interface')], edges: [edge('uplink-z', 'server', 'port-z', 'server_uplink', 'network_relation'), edge('uplink-a', 'server', 'port-a', 'server_uplink', 'network_relation'), edge('own-z', 'owner-z', 'port-z', 'owns_interface'), edge('own-a', 'owner-a', 'port-a', 'owns_interface')] }
  const layout = buildDeviceView(graph, 'server')
  expect(layout.nodes.find(item => item.node.id === 'port-z')!.x).toBeLessThan(layout.nodes.find(item => item.node.id === 'port-a')!.x)
  expect(layout.nodes.find(item => item.node.id === 'port-z')!.y).toBe(layout.nodes.find(item => item.node.id === 'port-a')!.y)
})

it('keeps IDC as top metadata and nests only actual cabinets, servers and GPUs', () => {
  const graph: TopologyGraph = {
    nodes: [node('server'), node('peer'), node('gpu', 'gpu'), node('peer-gpu', 'gpu'), node('cab', 'cabinet'), node('dc', 'data_center'), node('pod', 'pod'), node('rpp', 'rpp')],
    edges: [edge('server-cab', 'server', 'cab', 'located_in', 'spatial_relation'), edge('peer-cab', 'peer', 'cab', 'located_in', 'spatial_relation'), edge('cab-dc', 'cab', 'dc', 'located_in', 'spatial_relation'), edge('gpu-owner', 'server', 'gpu'), edge('peer-gpu-owner', 'peer', 'peer-gpu'), edge('pod-tag', 'server', 'pod', 'member_of', 'spatial_relation')],
  }
  const layout = buildDeviceView(graph, 'server')
  const containers = new Map(layout.containers.map(item => [item.id, item]))
  expect([...containers.keys()].sort()).toEqual(['cab', 'peer', 'server'])
  expect(layout.nodes.find(item => item.node.id === 'dc')!.y).toBeLessThan(containers.get('cab')!.y)
  expect(layout.edges.find(item => item.edge.id === 'cab-dc')!.contained).toBe(false)
  expect(containers.get('cab')!.depth).toBe(0)
  expect(containers.get('peer')!.y).toBe(containers.get('server')!.y)
  expect(containers.get('peer')!.x + containers.get('peer')!.width).toBeLessThan(containers.get('server')!.x)
  for (const id of ['server', 'peer']) {
    expectInside(containers.get(id)!, containers.get('cab')!)
    expectInside(layout.nodes.find(item => item.node.id === (id === 'server' ? 'gpu' : 'peer-gpu'))!, containers.get(id)!)
  }
  expect(containers.get('server')!.depth).toBe(1)
  for (const container of layout.containers) {
    const header = layout.nodes.find(item => item.node.id === container.id)!
    expectInside(header, container)
    expect(header.width).toBeGreaterThanOrEqual(240)
    expect(header.height).toBeLessThan(container.height)
  }
  const server = containers.get('server')!
  expect(layout.container).toEqual({ x: server.x, y: server.y, width: server.width, height: server.height })
  expect(layout.edges.find(item => item.edge.id === 'pod-tag')!.contained).toBe(false)
  expect(layout.nodes.find(item => item.node.id === 'pod')!.group).toBe('background')
  expect(layout.nodes.find(item => item.node.id === 'rpp')!.x + 180).toBeLessThan(containers.get('cab')!.x)
  expect(layout.edges.filter(item => item.contained).map(item => item.edge.id).sort()).toEqual(graph.edges.filter(item => !['pod-tag', 'cab-dc'].includes(item.id)).map(item => item.id).sort())
  expect(layout.nodes.map(item => item.node.id).sort()).toEqual(graph.nodes.map(item => item.id).sort())
  expect(layout.edges.map(item => item.edge.id).sort()).toEqual(graph.edges.map(item => item.id).sort())
  expect(layout.warnings).toEqual([])
})

it('arranges sibling roots and owned hardware horizontally without overlapping group bounds', () => {
  const graph: TopologyGraph = {
    nodes: [node('server'), node('peer'), node('other-server'), node('other-peer'), node('cab1', 'cabinet'), node('cab2', 'cabinet'), node('t1', 'transformer'), node('t2', 'transformer'), node('group1', 'ups_group'), node('group2', 'ups_group'), node('ups1', 'ups'), node('ups2', 'ups'), node('rpp1', 'rpp'), node('rpp2', 'rpp'), node('switch1'), node('switch2'), node('port1', 'interface'), node('port2', 'interface'), node('port3', 'interface')],
    edges: [edge('s1', 'server', 'cab1', 'located_in', 'spatial_relation'), edge('s2', 'peer', 'cab1', 'located_in', 'spatial_relation'), edge('s3', 'other-server', 'cab2', 'located_in', 'spatial_relation'), edge('s4', 'other-peer', 'cab2', 'located_in', 'spatial_relation'), edge('u1', 'ups1', 'group1', 'member_of', 'power_relation'), edge('u2', 'ups2', 'group1', 'member_of', 'power_relation'), edge('i1', 'switch1', 'port1', 'owns_interface'), edge('i2', 'switch1', 'port2', 'owns_interface'), edge('i3', 'switch2', 'port3', 'owns_interface')],
  }
  const layout = buildDeviceView(graph, 'server')
  const rect = (id: string) => layout.containers.find(item => item.id === id) || layout.nodes.find(item => item.node.id === id)!
  const expectRow = (ids: string[]) => {
    const items = ids.map(rect)
    expect(new Set(items.map(item => item.y)).size).toBe(1)
    items.slice(1).forEach((item, index) => expect(item.x).toBeGreaterThan(items[index]!.x + items[index]!.width))
  }
  expectRow(['cab1', 'cab2'])
  expectRow(['peer', 'server'])
  expectRow(['other-peer', 'other-server'])
  expectRow(['ups1', 'ups2'])
  expectRow(['port1', 'port2'])
  expectRow(['t1', 't2', 'group1', 'group2', 'rpp1', 'rpp2', 'cab1', 'cab2', 'switch1', 'switch2'])
  for (const [parent, children] of [['cab1', ['peer', 'server']], ['cab2', ['other-peer', 'other-server']], ['group1', ['ups1', 'ups2']], ['switch1', ['port1', 'port2']]] as const) {
    children.forEach(child => expectInside(rect(child), rect(parent)))
  }
  expect(layout.height).toBeLessThan(800)
  expect(layout.width).toBeGreaterThan(layout.height)
})

it('does not choose a first parent for ambiguous GPU, cabinet, UPS or interface relations', () => {
  const graph: TopologyGraph = {
    nodes: [node('server'), node('owner'), node('gpu', 'gpu'), node('cab1', 'cabinet'), node('cab2', 'cabinet'), node('port', 'interface'), node('ups', 'ups'), node('group1', 'ups_group'), node('group2', 'ups_group')],
    edges: [edge('g1', 'server', 'gpu'), edge('g2', 'owner', 'gpu'), edge('s1', 'server', 'cab1', 'located_in', 'spatial_relation'), edge('s2', 'server', 'cab2', 'located_in', 'spatial_relation'), edge('i1', 'server', 'port', 'owns_interface'), edge('i2', 'owner', 'port', 'owns_interface'), edge('u1', 'ups', 'group1', 'member_of', 'power_relation'), edge('u2', 'ups', 'group2', 'member_of', 'power_relation')],
  }
  graph.nodes[0]!.properties = { password: 'do-not-leak-secret' }
  const layout = buildDeviceView(graph, 'server')
  expect(layout.edges.every(item => item.contained === false)).toBe(true)
  expect(layout.containers.every(item => item.relationIds.length === 0)).toBe(true)
  expect(layout.warnings).toHaveLength(4)
  const warnings = layout.warnings.join(' ')
  for (const id of ['gpu', 'server', 'port', 'ups', 'owner', 'cab1', 'cab2', 'group1', 'group2']) expect(warnings).toContain(id)
  expect(warnings).toMatch(/[\u4e00-\u9fff]/)
  expect(warnings).not.toContain('do-not-leak-secret')
  expect(layout.nodes).toHaveLength(graph.nodes.length)
  expect(buildDeviceView({ nodes: [...graph.nodes].reverse(), edges: [...graph.edges].reverse() }, 'server')).toEqual(layout)
})

it('requires exact kinds, entity directions, present endpoints and identical sources for nesting', () => {
  const graph: TopologyGraph = {
    nodes: [node('server'), node('gpu', 'gpu'), node('cab', 'cabinet'), node('pod', 'pod'), { ...node('foreign', 'interface'), source_id: 'other' }, node('port', 'interface')],
    edges: [edge('wrong-type', 'server', 'gpu', 'contains_gpu', 'network_relation'), edge('wrong-kind', 'server', 'cab', 'member_of', 'spatial_relation'), edge('wrong-entity', 'server', 'pod', 'located_in', 'spatial_relation'), edge('cross-source', 'server', 'foreign', 'owns_interface'), { ...edge('edge-source', 'server', 'port', 'owns_interface'), source_id: 'other' }, edge('missing', 'server', 'missing'), edge('reversed', 'port', 'server', 'owns_interface')],
  }
  const layout = buildDeviceView(graph, 'server')
  expect(layout.edges.every(item => item.contained === false)).toBe(true)
  expect(layout.containers).toHaveLength(1)
  expect(layout.nodes).toHaveLength(graph.nodes.length)
  expect(layout.edges).toHaveLength(graph.edges.length)
})

it('anchors parallel cabinet power paths to its full container rather than the header', () => {
  const graph: TopologyGraph = { nodes: [node('server'), ...Array.from({ length: 4 }, (_, i) => node(`gpu-${i}`, 'gpu')), node('cab', 'cabinet'), node('rpp', 'rpp')], edges: [edge('space', 'server', 'cab', 'located_in', 'spatial_relation'), ...Array.from({ length: 4 }, (_, i) => edge(`gpu-${i}`, 'server', `gpu-${i}`)), edge('A', 'cab', 'rpp', 'power_upstream', 'power_relation'), edge('B', 'cab', 'rpp', 'power_upstream', 'power_relation')] }
  const layout = buildDeviceView(graph, 'server')
  const cabinet = layout.containers.find(item => item.id === 'cab')!
  const routes = layout.edges.filter(item => ['A', 'B'].includes(item.edge.id))
  for (const route of routes) {
    const [x, y] = route.path.match(/-?\d+(?:\.\d+)?/g)!.map(Number)
    expect(x).toBe(cabinet.x)
    expect(y).toBeGreaterThanOrEqual(cabinet.y)
    expect(y).toBeLessThanOrEqual(cabinet.y + cabinet.height)
    const [, , controlX] = route.path.match(/-?\d+(?:\.\d+)?/g)!.map(Number)
    expect(controlX).toBeLessThan(cabinet.x)
  }
  expect(routes[0]!.path).not.toBe(routes[1]!.path)
  expect(routes[0]!.labelY).not.toBe(routes[1]!.labelY)
})

it('keeps duplicate edges to the same explicit parent contained without creating duplicate cards', () => {
  const graph: TopologyGraph = { nodes: [node('server'), node('gpu', 'gpu')], edges: [edge('a', 'server', 'gpu'), edge('b', 'server', 'gpu')] }
  const layout = buildDeviceView(graph, 'server')
  expect(layout.containers.find(item => item.id === 'server')!.relationIds).toEqual(['a', 'b'])
  expect(layout.edges.every(item => item.contained)).toBe(true)
  expect(layout.nodes).toHaveLength(2)
  expect(layout.warnings).toEqual([])
})

it('keeps high multiplicity parallel paths and self-loop control points inside the canvas', () => {
  const graph: TopologyGraph = { nodes: [node('server'), node('cab', 'cabinet'), node('rpp', 'rpp')], edges: [...Array.from({ length: 100 }, (_, i) => edge(`parallel-${i}`, 'cab', 'rpp', 'power_upstream', 'power_relation')), edge('loop', 'rpp', 'rpp', 'has_standby', 'power_relation')] }
  const layout = buildDeviceView(graph, 'server')
  expect(new Set(layout.edges.map(item => item.path)).size).toBe(101)
  for (const item of layout.edges) {
    const points = item.path.match(/-?\d+(?:\.\d+)?/g)!.map(Number)
    for (let i = 0; i < points.length; i += 2) {
      expect(points[i]).toBeGreaterThanOrEqual(0); expect(points[i]).toBeLessThanOrEqual(layout.width)
      expect(points[i + 1]).toBeGreaterThanOrEqual(0); expect(points[i + 1]).toBeLessThanOrEqual(layout.height)
    }
  }
})
