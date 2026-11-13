import { effectScope, reactive, type Ref } from 'vue'
import { afterEach, expect, it, vi } from 'vitest'
import TopologyBrowser from './TopologyBrowser.vue'
import { topologyApi, topologyDirectoryApi } from '../api/topology'
import type { Source } from '../api/types'
import type { DeviceContext, TopologyGraph, TopologyNode, TopologyRootType } from '../api/topologyTypes'

vi.mock('vue', async importOriginal => {
  const actual = await importOriginal<typeof import('vue')>()
  return { ...actual, onMounted: () => {}, onBeforeUnmount: () => {}, useSSRContext: () => ({ modules: new Set<string>() }) }
})
vi.mock('../api/topology', () => ({ topologyApi: { node: vi.fn(), neighbors: vi.fn() }, topologyDirectoryApi: { roots: vi.fn(async () => ({ items: [], next_after: null })), children: vi.fn(), deviceContext: vi.fn() } }))
afterEach(() => vi.clearAllMocks())
type State = { viewMode: Ref<'device' | 'graph'>; sourceId: Ref<string>; stableId: Ref<string>; canLocate: Ref<boolean>; graph: Ref<TopologyGraph>; sourceStatus: Ref<string>; showGraph: Ref<boolean>; tab: Ref<TopologyRootType>; items: Ref<TopologyNode[]>; path: Ref<TopologyNode[]>; submit: () => Promise<void>; backToDirectory: () => void; openItem: (item: TopologyNode) => Promise<void> }
const setup = (TopologyBrowser as unknown as { setup: (props: unknown, context: unknown) => State }).setup

it('automatically resolves exactly one enabled CMDB source, never an arbitrary first source', async () => {
  const props = reactive({ sources: [{ id: 's', enabled: true, adapter_kind: 'cmdb' }, { id: 'other', enabled: true, adapter_kind: 'other' }] as Source[], sourcesLoaded: true, sourcesLoading: false, sourcesError: '' })
  const scope = effectScope()
  const state = scope.run(() => setup(props, { expose: () => {} }))!
  try {
    expect(state.sourceId.value).toBe('s')
    props.sources.push({ id: 's2', enabled: true, adapter_kind: 'cmdb', name: 'Second' })
    expect(state.sourceId.value).toBe('')
    expect(state.sourceStatus.value).toContain('多个')
    state.stableId.value = 'SN'
    await state.submit()
    expect(topologyDirectoryApi.deviceContext).not.toHaveBeenCalled()
    props.sources = []
    expect(state.sourceStatus.value).toContain('没有')
  } finally { scope.stop() }
})
it('device SN shortcut uses sole-source context and returns to the retained directory', async () => {
  const props = reactive({ sources: [{ id: 's', enabled: true, adapter_kind: 'cmdb' }] as Source[], sourcesLoaded: true, sourcesLoading: false, sourcesError: '' })
  const scope = effectScope()
  const state = scope.run(() => setup(props, { expose: () => {} }))!
  try {
    const root = { id: 'device', source_id: 's', entity_type: 'device' as const, stable_id: 'SN', label: 'Server', properties: {}, created_at: null, synced_at: null }
    vi.mocked(topologyDirectoryApi.deviceContext).mockResolvedValueOnce({ root, nodes: [], edges: [], limit: 50, truncated: false, warnings: ['缺少供电关系'] })
    state.stableId.value = 'SN'
    await state.submit()
    expect(state.showGraph.value).toBe(true)
    expect(state.graph.value.nodes).toEqual([root])
    expect(state.viewMode.value).toBe('device')
    state.viewMode.value = 'graph'
    expect(state.graph.value.nodes).toEqual([root])
    expect(topologyDirectoryApi.deviceContext).toHaveBeenCalledTimes(1)
    expect(topologyDirectoryApi.deviceContext).toHaveBeenCalledWith({ source_id: 's', stable_id: 'SN' }, expect.any(AbortSignal))
    expect(topologyApi.node).not.toHaveBeenCalled()
    expect(topologyApi.neighbors).not.toHaveBeenCalled()
    state.backToDirectory()
    expect(state.showGraph.value).toBe(false)
  } finally { scope.stop() }
})
it('opens a directory device via context only and cancels that context when switching directory tabs', async () => {
  const props = reactive({ sources: [{ id: 's', enabled: true, adapter_kind: 'cmdb' }] as Source[], sourcesLoaded: true, sourcesLoading: false, sourcesError: '' })
  const makeNode = (entity_type: TopologyNode['entity_type'], stable_id: string): TopologyNode => ({ id: stable_id, source_id: 's', entity_type, stable_id, label: stable_id, properties: {}, created_at: null, synced_at: null })
  const dc = makeNode('data_center', 'dc')
  const room = makeNode('room', 'room-uuid')
  const cabinet = makeNode('cabinet', 'cabinet')
  const device = makeNode('device', 'SN')
  vi.mocked(topologyDirectoryApi.roots).mockResolvedValueOnce({ items: [dc], next_after: null })
  vi.mocked(topologyDirectoryApi.children).mockResolvedValueOnce({ items: [room], next_after: null }).mockResolvedValueOnce({ items: [cabinet], next_after: null }).mockResolvedValueOnce({ items: [device], next_after: null })
  let resolve!: (value: DeviceContext) => void
  vi.mocked(topologyDirectoryApi.deviceContext).mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const scope = effectScope()
  const state = scope.run(() => setup(props, { expose: () => {} }))!
  try {
    await Promise.resolve()
    await state.openItem(state.items.value[0]!)
    await state.openItem(state.items.value[0]!)
    await state.openItem(state.items.value[0]!)
    expect(state.showGraph.value).toBe(false)
    expect(state.graph.value.nodes).toEqual([])
    const pending = state.openItem(state.items.value[0]!)
    expect(state.showGraph.value).toBe(true)
    expect(state.path.value.map(item => item.stable_id)).toEqual(['dc', 'room-uuid', 'cabinet'])
    expect(topologyApi.node).not.toHaveBeenCalled()
    expect(topologyApi.neighbors).not.toHaveBeenCalled()
    const signal = vi.mocked(topologyDirectoryApi.deviceContext).mock.calls[0]![1]!
    state.tab.value = 'pod'
    expect(signal.aborted).toBe(true)
    resolve({ root: device, nodes: [], edges: [], truncated: false, limit: 50, warnings: ['stale'] })
    await pending
    expect(state.showGraph.value).toBe(false)
    expect(state.graph.value.nodes).toEqual([])
  } finally { scope.stop() }
})
it('clears a selected source and graph when the real source is disabled', async () => {
  const props = reactive({ sources: [{ id: 's', enabled: true, adapter_kind: 'cmdb' } as Source], sourcesLoaded: true, sourcesLoading: false, sourcesError: '' })
  const scope = effectScope()
  const state = scope.run(() => setup(props, { expose: () => {} }))!
  try {
    state.sourceId.value = 's'
    state.stableId.value = 'SN'
    expect(state.canLocate.value).toBe(true)
    props.sources[0]!.enabled = false
    expect(state.sourceId.value).toBe('')
    expect(state.canLocate.value).toBe(false)
    expect(state.graph.value).toEqual({ nodes: [], edges: [] })
    await state.submit()
    expect(topologyApi.node).not.toHaveBeenCalled()
  } finally { scope.stop() }
})
it('refuses an invalid source or failed source load before issuing any lookup', async () => {
  const props = reactive({ sources: [{ id: 's', enabled: false, adapter_kind: 'cmdb' } as Source], sourcesLoaded: true, sourcesLoading: false, sourcesError: '' })
  const scope = effectScope()
  const state = scope.run(() => setup(props, { expose: () => {} }))!
  try {
    state.sourceId.value = 's'
    state.stableId.value = 'SN'
    await state.submit()
    expect(state.canLocate.value).toBe(false)
    props.sources[0]!.enabled = true
    props.sourcesError = 'source load failed'
    await state.submit()
    expect(topologyApi.node).not.toHaveBeenCalled()
  } finally { scope.stop() }
})
