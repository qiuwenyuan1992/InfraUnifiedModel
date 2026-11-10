import { createSSRApp, defineComponent, effectScope, h, nextTick, reactive, ref, shallowRef, type Ref } from 'vue'
import { renderToString } from 'vue/server-renderer'
import type { TopologyNode } from '../api/topologyTypes'
import type { SpatialSceneData } from '../services/spatialScene'
import { afterEach, expect, it, vi } from 'vitest'
import SpatialBrowser from './SpatialBrowser.vue'

const calls = vi.hoisted(() => ({ load: vi.fn(), reset: vi.fn(), node: vi.fn(), fixture: null as null | { data: SpatialSceneData; directoryAfter?: string; sceneAfter?: string; sceneHistory?: (string | undefined)[] } }))
vi.mock('../api/topology', () => ({ topologyApi: { node: calls.node } }))
vi.mock('../composables/useSpatialScene', () => ({ useSpatialScene: () => ({
  rootItems: shallowRef([]), rootsNextAfter: ref(calls.fixture?.directoryAfter || null), selectedRoot: shallowRef(calls.fixture?.data.root || null), loading: ref(false), error: ref(''),
  sceneNextAfter: ref(calls.fixture?.sceneAfter || null), sceneHistory: ref(calls.fixture?.sceneHistory || []), cabinetNextAfter: shallowRef({}),
  data: shallowRef(calls.fixture?.data || { nodes: [], edges: [], root: null, truncated: false, warnings: [] }), mode: ref('space'),
  loadRoots: calls.load, reset: calls.reset, selectRoot: vi.fn(), nextScenePage: vi.fn(), previousScenePage: vi.fn(), expandNetwork: vi.fn(),
}) }))
vi.mock('./SpatialThreeCanvas.vue', () => ({ default: { render: () => null } }))
vi.mock('vue', async original => ({ ...await original<typeof import('vue')>(), useSSRContext: () => ({ modules: new Set() }) }))
afterEach(() => { vi.clearAllMocks(); calls.fixture = null })

it('automatically uses the sole enabled CMDB source without a selector', () => {
  const scope = effectScope()
  const setup = (SpatialBrowser as unknown as { setup: (p: unknown, c: unknown) => unknown }).setup
  try {
    scope.run(() => setup({ sources: [{ id: 's', enabled: true, adapter_kind: 'cmdb' }], sourcesLoaded: true, sourcesLoading: false, sourcesError: '' }, { expose: () => {} }))
    expect(calls.load).toHaveBeenCalledWith('s', 'data_center')
  } finally { scope.stop() }
})

it('does not choose an arbitrary source when multiple CMDB sources are active', () => {
  const scope = effectScope()
  const setup = (SpatialBrowser as unknown as { setup: (p: unknown, c: unknown) => unknown }).setup
  try {
    scope.run(() => setup({ sources: [{ id: 'a', enabled: true, adapter_kind: 'cmdb' }, { id: 'b', enabled: true, adapter_kind: 'cmdb' }], sourcesLoaded: true, sourcesLoading: false, sourcesError: '' }, { expose: () => {} }))
    expect(calls.load).not.toHaveBeenCalled()
    expect(calls.reset).toHaveBeenCalled()
  } finally { scope.stop() }
})

const node = (id: string): TopologyNode => ({ id, source_id: 's', stable_id: id, entity_type: 'device', label: id, properties: { role: '' }, synced_at: null, created_at: null })
const props = () => ({ sources: [{ id: 's', name: 'CMDB', enabled: true, adapter_kind: 'cmdb' }], sourcesLoaded: true, sourcesLoading: false, sourcesError: '' })
function mountInspector() {
  calls.fixture = { data: { nodes: [node('first'), node('second')], edges: [], root: null, truncated: false, warnings: [] } }
  const scope = effectScope()
  const setup = (SpatialBrowser as unknown as { setup: (p: unknown, c: unknown) => {
    selection: Ref<string | null>; detailNode: Ref<TopologyNode | null>; detailsError: Ref<string>; detailsLoading: Ref<boolean>
  } }).setup
  const input = reactive(props())
  return { scope, input, state: scope.run(() => setup(input, { expose: () => {} }))! }
}
it('fetches full properties only on selection and caches each identity for this view', async () => {
  calls.node.mockImplementation(async identity => ({ ...node(identity.stable_id), properties: { serial: 'full-serial', model: 'actual-model' } }))
  const { scope, state } = mountInspector()
  try {
    expect(calls.node).not.toHaveBeenCalled()
    state.selection.value = 'first'; await nextTick()
    expect(calls.node).toHaveBeenCalledWith({ source_id: 's', entity_type: 'device', stable_id: 'first' }, expect.any(AbortSignal))
    expect(state.detailNode.value?.properties).toEqual({ serial: 'full-serial', model: 'actual-model' })
    state.selection.value = null; state.selection.value = 'first'; await nextTick()
    expect(calls.node).toHaveBeenCalledTimes(1)
    state.selection.value = 'second'; await nextTick()
    state.selection.value = 'first'; await nextTick()
    expect(calls.node).toHaveBeenCalledTimes(2)
  } finally { scope.stop() }
})
it('aborts stale detail reads and disposal without showing stale results', async () => {
  let complete!: (node: TopologyNode) => void
  calls.node.mockImplementationOnce(() => new Promise<TopologyNode>(resolve => { complete = resolve }))
    .mockResolvedValueOnce({ ...node('second'), properties: { model: 'second-model' } })
  const { scope, state } = mountInspector()
  state.selection.value = 'first'
  const signal = calls.node.mock.calls[0]![1] as AbortSignal
  state.selection.value = 'second'; await nextTick()
  expect(signal.aborted).toBe(true)
  complete(node('first')); await nextTick()
  expect(state.detailNode.value?.id).toBe('second')
  calls.node.mockImplementationOnce(() => new Promise(() => {}))
  state.selection.value = 'first'
  const active = calls.node.mock.calls.at(-1)![1] as AbortSignal
  scope.stop()
  expect(active.aborted).toBe(true)
})
it('rejects foreign full details and clears the cache when the source changes', async () => {
  calls.node.mockResolvedValueOnce({ ...node('first'), source_id: 'foreign' }).mockResolvedValue(node('first'))
  const { scope, state, input } = mountInspector()
  try {
    state.selection.value = 'first'; await nextTick()
    expect(state.detailNode.value).toBeNull(); expect(state.detailsError.value).not.toBe('')
    state.selection.value = null; state.selection.value = 'first'; await nextTick()
    expect(state.detailNode.value?.id).toBe('first')
    input.sources[0]!.id = 'new-source'
    state.selection.value = 'first'; await nextTick()
    expect(calls.node).toHaveBeenCalledTimes(3)
  } finally { scope.stop() }
})
it('renders separate directory and scene pagination without revealing opaque cursors', async () => {
  const root = { ...node('dc'), entity_type: 'data_center' as const }
  calls.fixture = { data: { nodes: [root], root, edges: [], truncated: true, warnings: [] }, directoryAfter: 'private-directory', sceneAfter: 'private-scene', sceneHistory: [undefined] }
  const app = createSSRApp({ render: () => h(SpatialBrowser, props()) })
  app.component('el-button', defineComponent({ setup(_props, { slots }) { return () => h('button', slots.default?.()) } }))
  app.component('el-alert', { render: () => null })
  app.directive('loading', { getSSRProps: () => ({}) })
  const html = await renderToString(app)
  expect(html).toContain('目录第 1 页'); expect(html).toContain('场景第 2 页')
  expect(html).toContain('下一场景页'); expect(html).toContain('上一场景页')
  expect(html).not.toContain('private-directory'); expect(html).not.toContain('private-scene')
})
