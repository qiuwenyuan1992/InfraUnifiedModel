import { effectScope, h, nextTick, reactive, type Ref } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { afterEach, expect, it, vi } from 'vitest'
import DeviceTopologyView from './DeviceTopologyView.vue'
import type { EdgeType, TopologyEdge, TopologyGraph, TopologyNode, TopologySelection } from '../api/topologyTypes'
import type { DeviceViewContainer, DeviceViewEdge, DeviceViewLayout, DeviceViewNode } from '../services/deviceView'

const hooks = vi.hoisted(() => ({ unmount: () => {} }))
vi.mock('vue', async importOriginal => {
  const actual = await importOriginal<typeof import('vue')>()
  return { ...actual, useId: () => 'test-device', onBeforeUnmount: (hook: () => void) => { hooks.unmount = hook }, useSSRContext: () => ({ modules: new Set<string>() }) }
})
afterEach(() => vi.clearAllMocks())
const node = (id: string, entity_type: TopologyNode['entity_type'] = 'device'): TopologyNode => ({ id, source_id: 's', entity_type, stable_id: `SN-${id}`, label: id, properties: {}, created_at: null, synced_at: null })
const edge = (id: string, from: string, to: string, edge_type: EdgeType, relation_kind: string): TopologyEdge => ({ id, from, to, edge_type, relation_kind, source_id: 's', rank: '0', properties: {}, created_at: null, synced_at: null })
const makeGraph = (): TopologyGraph => ({ nodes: [node('server'), node('gpu', 'gpu'), node('cab', 'cabinet'), node('rpp', 'rpp'), node('port', 'interface'), node('switch')], edges: [edge('contains', 'server', 'gpu', 'composition_relation', 'contains_gpu'), edge('spatial', 'server', 'cab', 'spatial_relation', 'located_in'), { ...edge('power-a', 'cab', 'rpp', 'power_relation', 'power_upstream'), properties: { power_path: 'A' } }, { ...edge('power-b', 'cab', 'rpp', 'power_relation', 'power_upstream'), properties: { power_path: 'B' } }, edge('uplink', 'gpu', 'port', 'network_relation', 'gpu_uplink'), edge('owns', 'switch', 'port', 'composition_relation', 'owns_interface')] })
type State = {
  layout: Ref<DeviceViewLayout>; visibleNodes: Ref<DeviceViewNode[]>; visibleEdges: Ref<DeviceViewEdge[]>
  filters: Ref<Record<EdgeType, boolean>>; zoomLevel: Ref<number>; camera: Ref<{ x: number; y: number }>
  svg: Ref<SVGSVGElement | undefined>; gpuCount: Ref<number>; networkCount: Ref<number>
  collapsedIds: Ref<ReadonlySet<string>>; hiddenIds: Ref<Set<string>>
  collapsedContainers: Ref<DeviceViewContainer[]>; collapsibleContainers: Ref<DeviceViewContainer[]>
  toggleCollapse: (id: string) => void
  select: (selection: TopologySelection) => void; edgeLabel: (edge: TopologyEdge) => string
  edgeHighlighted: (edge: TopologyEdge) => boolean; nodeHighlighted: (id: string) => boolean
  zoom: (factor: number) => void; fit: () => void; onWheel: (event: WheelEvent) => void
  startPan: (event: PointerEvent) => void; movePan: (event: PointerEvent) => void; endPan: (event?: PointerEvent) => void
}
function mount(graph = makeGraph()) {
  const props = reactive<{ graph: TopologyGraph; rootId: string; selection: TopologySelection | null }>({ graph, rootId: 'server', selection: null })
  const scope = effectScope(), emit = vi.fn()
  const setup = (DeviceTopologyView as unknown as { setup: (props: unknown, context: unknown) => State }).setup
  const state = scope.run(() => setup(props, { expose: () => {}, emit }))!
  return { state, props, emit, stop: () => { hooks.unmount(); scope.stop() } }
}

it('keeps actual IDs for selection and highlights neighboring true edges', () => {
  const { state, props, emit, stop } = mount()
  try {
    state.select({ kind: 'node', id: 'gpu' }); state.select({ kind: 'edge', id: 'power-b' })
    expect(emit.mock.calls).toEqual([['select', { kind: 'node', id: 'gpu' }], ['select', { kind: 'edge', id: 'power-b' }]])
    props.selection = { kind: 'node', id: 'gpu' }
    expect(state.edgeHighlighted(props.graph.edges.find(edge => edge.id === 'uplink')!)).toBe(true)
    expect(state.edgeHighlighted(props.graph.edges.find(edge => edge.id === 'power-a')!)).toBe(false)
    expect(state.nodeHighlighted('port')).toBe(true)
    props.selection = { kind: 'edge', id: 'power-b' }
    expect(state.edgeHighlighted(props.graph.edges.find(edge => edge.id === 'power-a')!)).toBe(true)
    expect(state.nodeHighlighted('cab')).toBe(true)
    expect(state.edgeLabel(props.graph.edges.find(edge => edge.id === 'power-a')!)).toContain('路径 A')
    expect(state.edgeLabel(props.graph.edges.find(edge => edge.id === 'power-b')!)).toContain('路径 B')
  } finally { stop() }
})

it('filters presentation only with all actual nodes and edges shown by default', () => {
  const { state, props, stop } = mount()
  try {
    const original = JSON.stringify(props.graph)
    expect(state.visibleNodes.value).toHaveLength(6); expect(state.visibleEdges.value).toHaveLength(3)
    expect(state.visibleEdges.value.map(item => item.edge.id).sort()).toEqual(['power-a', 'power-b', 'uplink'])
    for (const domain of ['power_relation', 'network_relation', 'spatial_relation', 'composition_relation'] as const) {
      state.filters.value[domain] = false
      expect(state.visibleEdges.value.some(item => item.edge.edge_type === domain)).toBe(false)
      state.filters.value[domain] = true
    }
    expect(JSON.stringify(props.graph)).toBe(original)
    expect(state.layout.value.nodes).toHaveLength(6); expect(state.layout.value.edges).toHaveLength(6)
    expect(state.gpuCount.value).toBe(1); expect(state.networkCount.value).toBe(1)
  } finally { stop() }
})

it('bounds zoom and pan, suppresses drag clicks, and releases pointer capture on cancellation and unmount', () => {
  const { state, emit, stop } = mount()
  const target = { closest: () => null }
  const fakeSVG = { getBoundingClientRect: () => ({ width: 1200, height: 600 }), setPointerCapture: vi.fn(), hasPointerCapture: vi.fn(() => true), releasePointerCapture: vi.fn() }
  state.svg.value = fakeSVG as unknown as SVGSVGElement
  const pointer = (x: number, y: number, pointerId = 1) => ({ clientX: x, clientY: y, pointerId, button: 0, target, preventDefault: vi.fn() }) as unknown as PointerEvent
  try {
    state.zoom(10000); expect(state.zoomLevel.value).toBe(4)
    state.zoom(0.00001); expect(state.zoomLevel.value).toBe(0.2)
    state.zoom(NaN); expect(Number.isFinite(state.zoomLevel.value)).toBe(true)
    state.fit(); expect(state.zoomLevel.value).toBe(1); expect(state.camera.value).toEqual({ x: 0, y: 0 })
    state.startPan(pointer(10, 10)); state.movePan(pointer(10000, -10000))
    expect(Math.abs(state.camera.value.x)).toBeLessThanOrEqual(state.layout.value.width)
    expect(Math.abs(state.camera.value.y)).toBeLessThanOrEqual(state.layout.value.height)
    state.endPan(pointer(10000, -10000)); state.select({ kind: 'node', id: 'server' })
    expect(emit).not.toHaveBeenCalled()
    expect(fakeSVG.releasePointerCapture).toHaveBeenCalledWith(1)
    state.startPan(pointer(20, 20, 2)); state.endPan(pointer(20, 20, 2)); state.select({ kind: 'node', id: 'server' })
    expect(emit).toHaveBeenCalledWith('select', { kind: 'node', id: 'server' })
    state.startPan(pointer(30, 30, 3))
  } finally { stop() }
  expect(fakeSVG.releasePointerCapture).toHaveBeenCalledWith(3)
})

it('does not start canvas panning on a node/edge and ignores other pointers', () => {
  const { state, stop } = mount()
  const fakeSVG = { getBoundingClientRect: () => ({ width: 1200, height: 600 }), setPointerCapture: vi.fn(), hasPointerCapture: () => true, releasePointerCapture: vi.fn() }
  state.svg.value = fakeSVG as unknown as SVGSVGElement
  const event = { clientX: 10, clientY: 10, pointerId: 1, button: 0, target: { closest: () => ({}) }, preventDefault: vi.fn() } as unknown as PointerEvent
  try {
    state.startPan(event); expect(fakeSVG.setPointerCapture).not.toHaveBeenCalled()
    state.startPan({ ...event, target: { closest: () => null } } as unknown as PointerEvent)
    state.movePan({ ...event, pointerId: 2, clientX: 100 } as PointerEvent)
    expect(state.camera.value).toEqual({ x: 0, y: 0 })
    state.endPan({ ...event, pointerId: 2 } as PointerEvent)
    expect(fakeSVG.releasePointerCapture).not.toHaveBeenCalled()
    state.endPan(event); expect(fakeSVG.releasePointerCapture).toHaveBeenCalledWith(1)
  } finally { stop() }
})

it('reacts to dynamic roots without inventing GPU or network hardware and resets the camera', async () => {
  const { state, props, stop } = mount()
  try {
    state.zoom(2)
    props.graph = { nodes: [node('new-server')], edges: [] }; props.rootId = 'new-server'
    await nextTick()
    expect(state.gpuCount.value).toBe(0); expect(state.networkCount.value).toBe(0)
    expect(state.layout.value.nodes.map(item => item.node.id)).toEqual(['new-server'])
    expect(state.zoomLevel.value).toBe(1)
    props.rootId = 'missing'; await nextTick()
    expect(state.visibleNodes.value).toEqual([]); expect(state.visibleEdges.value).toEqual([])
  } finally { stop() }
})

it('renders original SVG IDs, directed A/B paths behind cards, readable GPU identity/model and escaped text', async () => {
  const graph = makeGraph()
  graph.nodes.find(item => item.id === 'gpu')!.properties.model = 'Real GPU model'
  graph.nodes.find(item => item.id === 'gpu')!.label = '<script>alert(1)</script>'
  const html = await renderToString(h(DeviceTopologyView, { graph, rootId: 'server', selection: null }))
  expect((html.match(/data-node-id=/g) || [])).toHaveLength(graph.nodes.length)
  expect((html.match(/data-edge-id=/g) || [])).toHaveLength(3)
  expect(html).toContain('data-container-id="cab"')
  expect(html).toContain('data-container-id="server"')
  expect(html).toContain('data-container-id="switch"')
  expect(html).toContain('data-contained-edge-id="contains"')
  expect(html).toContain('data-contained-edge-id="spatial"')
  expect(html).toContain('data-contained-edge-id="owns"')
  expect(html).toContain('data-edge-id="power-a"'); expect(html).toContain('data-edge-id="power-b"')
  expect(html).toContain('marker-end="url(#device-arrow-test-device)"')
  expect(html.indexOf('class="device-edges"')).toBeLessThan(html.indexOf('data-node-id="gpu"'))
  expect(html).toContain('SN SN-gpu'); expect(html).toContain('Real GPU model')
  expect(html).toContain('&lt;script&gt;'); expect(html).not.toContain('<script>alert')
  expect(html).toContain('路径 A'); expect(html).toContain('路径 B')
  expect(html).not.toContain('data-edge-id="spatial"')
  expect(html).not.toContain('data-edge-id="contains"')
  expect(html).toContain('clip-path="url(#device-arrow-test-device-card-')
})

it('renders IDC metadata without any box and preserves spatial IDs only in the no-line relation list', async () => {
  const graph = makeGraph()
  graph.nodes.push(node('dc', 'data_center'), node('pod', 'pod'))
  graph.edges.push(edge('cab-dc', 'cab', 'dc', 'spatial_relation', 'located_in'), edge('pod-tag', 'server', 'pod', 'spatial_relation', 'member_of'))
  const html = await renderToString(h(DeviceTopologyView, { graph, rootId: 'server', selection: null }))
  expect(html).not.toContain('data-container-id="dc"')
  const metadata = html.match(/<g[^>]*data-node-id="dc"[\s\S]*?<\/g>/)![0]
  expect(metadata).not.toContain('<rect')
  expect(metadata).toContain('SN-dc')
  expect(html).toContain('data-contained-edge-id="cab-dc"')
  expect(html).toContain('data-contained-edge-id="pod-tag"')
  expect(html).not.toContain('data-edge-id="cab-dc"')
  expect(html).not.toContain('data-edge-id="pod-tag"')
  expect((html.match(/data-node-id=/g) || [])).toHaveLength(graph.nodes.length)
})

it('renders explicit zero GPU and missing uplink states with no placeholders', async () => {
  const html = await renderToString(h(DeviceTopologyView, { graph: { nodes: [node('server')], edges: [] }, rootId: 'server', selection: null }))
  expect(html).toContain('GPU 0 · 未发现实际包含关系')
  expect(html).toContain('未发现真实网络上联关系')
  expect(html).not.toContain('data-group="gpu"')
  expect((html.match(/data-node-id=/g) || [])).toHaveLength(1)
})

const portHeavyGraph = (): TopologyGraph => {
  const ports = Array.from({ length: 14 }, (_, index) => node(`port-${index}`, 'interface'))
  return { nodes: [node('server'), node('switch'), ...ports], edges: [edge('uplink', 'server', 'port-0', 'network_relation', 'server_uplink'), ...ports.map((port, index) => edge(`own-${index}`, 'switch', port.id, 'composition_relation', 'owns_interface'))] }
}

it('collapses port-heavy owners into summary boxes and expands back on demand', () => {
  const graph = portHeavyGraph()
  const { state, stop } = mount(graph)
  try {
    // 超过阈值的对端容器默认折叠为摘要框，端口与头部卡片隐藏。
    expect(state.collapsedIds.value.has('switch')).toBe(true)
    expect(state.hiddenIds.value.has('switch')).toBe(true)
    expect(state.hiddenIds.value.has('port-3')).toBe(true)
    expect(state.collapsedContainers.value.map(item => item.id)).toEqual(['switch'])
    expect(state.visibleNodes.value.map(item => item.node.id)).toEqual(['server'])
    expect(state.visibleEdges.value).toHaveLength(0)
    // 摘要框比展开网格窄得多，画布保持可读。
    const box = state.collapsedContainers.value[0]!
    expect(box.width).toBeLessThanOrEqual(320)
    // 点击摘要框展开：全部端口卡片与上联边恢复显示。
    state.toggleCollapse('switch')
    expect(state.collapsedIds.value.has('switch')).toBe(false)
    expect(state.collapsedContainers.value).toEqual([])
    expect(state.visibleNodes.value).toHaveLength(16)
    expect(state.visibleEdges.value.map(item => item.edge.id)).toEqual(['uplink'])
    expect(state.collapsibleContainers.value.map(item => item.id)).toEqual(['switch'])
    // 再次收起，恢复摘要框。
    state.toggleCollapse('switch')
    expect(state.collapsedIds.value.has('switch')).toBe(true)
    expect(state.visibleNodes.value.map(item => item.node.id)).toEqual(['server'])
  } finally { stop() }
})

it('never collapses the root device or its ancestor containers', () => {
  const ports = Array.from({ length: 14 }, (_, index) => node(`port-${index}`, 'interface'))
  const servers = Array.from({ length: 16 }, (_, index) => node(`peer-${index}`))
  const graph: TopologyGraph = { nodes: [node('server'), node('cab', 'cabinet'), ...servers, node('switch'), ...ports], edges: [edge('spatial', 'server', 'cab', 'spatial_relation', 'located_in'), ...servers.map(peer => edge(`s-${peer.id}`, peer.id, 'cab', 'spatial_relation', 'located_in')), ...ports.map((port, index) => edge(`own-${index}`, 'switch', port.id, 'composition_relation', 'owns_interface'))] }
  const { state, stop } = mount(graph)
  try {
    // 根设备与包含根的机柜（祖先）永不折叠；只有对端交换机折叠。
    expect(state.collapsedIds.value).toEqual(new Set(['switch']))
    state.toggleCollapse('server')
    state.toggleCollapse('cab')
    expect(state.collapsedIds.value).toEqual(new Set(['switch']))
  } finally { stop() }
})

it('renders summary boxes for crowded owners without leaking hidden port cards', async () => {
  const graph = portHeavyGraph()
  const html = await renderToString(h(DeviceTopologyView, { graph, rootId: 'server', selection: null }))
  expect(html).toContain('data-collapsed-container-id="switch"')
  expect(html).toContain('网络端口 ×14 · 点击展开')
  expect(html).toContain('设备 switch')
  expect((html.match(/data-node-id=/g) || [])).toHaveLength(1)
  expect(html).not.toContain('data-node-id="port-0"')
  expect(html).not.toContain('data-container-id="switch"')
  expect(html).not.toContain('data-edge-id="uplink"')
})
