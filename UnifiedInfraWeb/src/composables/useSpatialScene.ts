import { getCurrentScope, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { topologyApi, topologyDirectoryApi, topologySceneApi } from '../api/topology'
import { normalizeSceneResponse, type SceneQuery, type SceneResponse } from '../api/newSceneTypes'
import type { Neighborhood, NeighborQuery, TopologyDirectoryPage, TopologyNode, TopologyRootQuery, TopologyRootType } from '../api/topologyTypes'
import { topologyError } from '../services/topology'
import type { SpatialSceneData, SpatialSceneMode } from '../services/spatialScene'

export interface SpatialSceneApi {
  roots(query: TopologyRootQuery, signal?: AbortSignal): Promise<TopologyDirectoryPage>
  scene(query: SceneQuery, signal?: AbortSignal): Promise<SceneResponse>
  neighbors(query: NeighborQuery, signal?: AbortSignal): Promise<Neighborhood>
}
const empty = (): SpatialSceneData => ({ nodes: [], edges: [], root: null, truncated: false, warnings: [], cabinetAssignments: {}, componentOwners: {}, networkLevels: {} })
export function useSpatialScene(api: SpatialSceneApi = { ...topologyDirectoryApi, ...topologyApi, ...topologySceneApi }) {
  const rootItems = shallowRef<TopologyNode[]>([])
  const rootsNextAfter = ref<string | null>(null)
  const sceneNextAfter = ref<string | null>(null)
  const sceneHistory = ref<(string | undefined)[]>([])
  const sceneAfter = ref<string | undefined>()
  const cabinetNextAfter = shallowRef<Record<string, string>>({})
  function resetScenePage() {
    sceneNextAfter.value = null
    sceneHistory.value = []
    sceneAfter.value = undefined
    cabinetNextAfter.value = {}
  }
  const selectedRoot = shallowRef<TopologyNode | null>(null)
  const loading = ref(false)
  const error = ref('')
  const data = shallowRef<SpatialSceneData>(empty())
  const mode = ref<SpatialSceneMode>('space')
  const sourceId = ref('')
  const rootType = ref<TopologyRootType>('data_center')
  let generation = 0
  let controller: AbortController | undefined
  let disposed = false
  function invalidate() {
    generation++
    controller?.abort()
    loading.value = false
    error.value = ''
  }
  function reset() {
    invalidate()
    rootItems.value = []
    rootsNextAfter.value = null
    selectedRoot.value = null
    resetScenePage()
    data.value = empty()
  }
  watch([sourceId, rootType], reset, { flush: 'sync' })
  function start() {
    invalidate()
    const owner = new AbortController()
    controller = owner
    loading.value = true
    const version = generation
    const signal = owner.signal
    const active = () => !disposed && generation === version
    const current = () => active() && !signal.aborted
    async function boundedMap<T, R>(items: T[], worker: (item: T) => Promise<R>): Promise<R[]> {
      const results = new Array<R>(items.length)
      let index = 0
      let failed = false
      let failure: unknown
      await Promise.all(Array.from({ length: Math.min(4, items.length) }, async () => {
        while (!failed && current() && index < items.length) {
          const position = index++
          try { results[position] = await worker(items[position]!) }
          catch (cause) {
            if (!failed) { failed = true; failure = cause; owner.abort() }
          }
        }
      }))
      if (failed) throw failure
      return results
    }
    return { signal, active, current, boundedMap, abort: () => owner.abort() }
  }
  async function loadRoots(source: string, type: TopologyRootType, after?: string) {
    if (disposed) return
    sourceId.value = source
    rootType.value = type
    selectedRoot.value = null
    resetScenePage()
    data.value = empty()
    rootItems.value = []
    rootsNextAfter.value = null
    const run = start()
    if (!source || !['data_center', 'pod'].includes(type)) { loading.value = false; return }
    try {
      const page = await api.roots({ source_id: source, entity_type: type, limit: 20, ...(after ? { after } : {}) }, run.signal)
      if (!run.current()) return
      rootItems.value = page.items.filter(node => node.source_id === source && node.entity_type === type).slice(0, 20)
      rootsNextAfter.value = page.next_after
    } catch (cause) { if (run.current()) error.value = topologyError(cause) }
    finally { if (run.current()) loading.value = false }
  }
  function collector(root: TopologyNode, initial: SpatialSceneData = empty()) {
    const nodes = new Map(initial.nodes.map(node => [node.id, node]))
    nodes.set(root.id, root)
    const edges = new Map(initial.edges.map(edge => [edge.id, edge]))
    const warnings = new Set(initial.warnings)
    const assignmentStatus = { ...initial.assignmentStatus }
    let truncated = initial.truncated
    function partial(message: string) { truncated = true; warnings.add(message) }
    function addNode(node: TopologyNode) {
      if (node.source_id !== root.source_id) { partial('已忽略不同来源的节点。'); return }
      if (nodes.size >= 400 && !nodes.has(node.id)) { partial('达到 400 个已加载节点预算。'); return }
      if (!nodes.has(node.id)) {
        assignmentStatus[node.id] = 'unknown'
        warnings.add('手动展开端点层级未确认，请重载场景以获取后端核验归属。')
        nodes.set(node.id, node)
      }
    }
    function merge(incoming: Neighborhood, accept: (node: TopologyNode) => boolean, acceptEdge: (edge: Neighborhood['edges'][number]) => boolean) {
      for (const node of [incoming.root, ...incoming.nodes]) if (accept(node)) addNode(node)
      for (const edge of incoming.edges) {
        if (edge.source_id !== root.source_id || !nodes.has(edge.from) || !nodes.has(edge.to) || !acceptEdge(edge)) continue
        if (edges.size >= 600 && !edges.has(edge.id)) { partial('达到 600 条已加载关系预算。'); continue }
        edges.set(edge.id, edge)
      }
      if (incoming.truncated) partial('邻居读取已截断（每次最多 100 条关系），未自动继续展开。')
    }
    return { nodes, partial, merge,
      snapshot: (): SpatialSceneData => ({ root, nodes: [...nodes.values()], edges: [...edges.values()], truncated, warnings: [...warnings], assignmentStatus,
        cabinetAssignments: { ...initial.cabinetAssignments }, componentOwners: { ...initial.componentOwners }, networkLevels: { ...initial.networkLevels } }) }
  }
  async function loadScenePage(node: TopologyNode, after?: string, pageHistory: (string | undefined)[] = []) {
    if (disposed || node.source_id !== sourceId.value || node.entity_type !== rootType.value) return
    const run = start()
    selectedRoot.value = node
    data.value = empty()
    cabinetNextAfter.value = {}
    sceneNextAfter.value = null
    try {
      const page = await api.scene({ source_id: node.source_id, entity_type: rootType.value,
        stable_id: node.stable_id, limit: 5, ...(after ? { after } : {}) }, run.signal)
      if (!run.current()) return
      if (page.root.source_id !== node.source_id || page.root.entity_type !== node.entity_type
        || page.root.stable_id !== node.stable_id || page.root.id !== node.id) throw new Error('场景根节点与当前来源或选择不匹配。')
      const normalized = normalizeSceneResponse(page)
      selectedRoot.value = normalized.data.root
      data.value = normalized.data
      cabinetNextAfter.value = normalized.cabinetNextAfter
      sceneNextAfter.value = page.next_after || null
      sceneAfter.value = after
      sceneHistory.value = pageHistory
    } catch (cause) {
      if (run.current()) error.value = topologyError(cause)
    } finally { if (run.current()) loading.value = false }
  }
  async function selectRoot(node: TopologyNode) {
    if (disposed || node.source_id !== sourceId.value || node.entity_type !== rootType.value) return
    resetScenePage()
    await loadScenePage(node)
  }
  async function nextScenePage() {
    if (loading.value || !selectedRoot.value || !sceneNextAfter.value) return
    await loadScenePage(selectedRoot.value, sceneNextAfter.value, [...sceneHistory.value, sceneAfter.value])
  }
  async function previousScenePage() {
    if (loading.value || !selectedRoot.value || !sceneHistory.value.length) return
    const history = [...sceneHistory.value]
    await loadScenePage(selectedRoot.value, history.pop(), history)
  }
  async function expandNetwork(node: TopologyNode) {
    const root = selectedRoot.value
    if (disposed || loading.value || !root || node.source_id !== sourceId.value || !Object.hasOwn(data.value.networkLevels || {}, node.id)
      || !data.value.nodes.some(item => item.id === node.id)) return
    const run = start()
    const graph = collector(root, data.value)
    const sameSourceEdge = (edge: Neighborhood['edges'][number]) => edge.source_id === root.source_id
    async function read(target: TopologyNode, edge_type: NeighborQuery['edge_type']) {
      if (!run.current()) return null
      const incoming = await api.neighbors({ source_id: sourceId.value, entity_type: target.entity_type,
        stable_id: target.stable_id, direction: 'both', edge_type }, run.signal)
      return run.current() ? incoming : null
    }
    try {
      const composition = await read(node, 'composition_relation')
      if (!composition) return
      const portIds = new Set(composition.edges.filter(edge => sameSourceEdge(edge) && edge.edge_type === 'composition_relation'
        && edge.relation_kind === 'owns_interface' && edge.from === node.id).map(edge => edge.to))
      graph.merge(composition, item => graph.nodes.has(item.id) || (item.entity_type === 'interface' && portIds.has(item.id)), edge =>
        edge.edge_type === 'composition_relation' && edge.relation_kind === 'owns_interface' && edge.from === node.id && portIds.has(edge.to))
      const ports = [...graph.nodes.values()].filter(item => item.entity_type === 'interface' && portIds.has(item.id))
      if (ports.length > 7) graph.partial('仅展开前 7 个已加载端口的真实 links_to 关系。')
      const portResults = await run.boundedMap(ports.slice(0, 7), async port => ({ port, incoming: await read(port, 'network_relation') }))
      for (const result of portResults) {
        if (!result?.incoming) continue
        const { port, incoming } = result
        const endpoints = new Set(incoming.edges.filter(edge => sameSourceEdge(edge) && edge.edge_type === 'network_relation' && edge.relation_kind === 'links_to'
          && (edge.from === port.id || edge.to === port.id)).flatMap(edge => [edge.from, edge.to]))
        graph.merge(incoming, item => graph.nodes.has(item.id) || (item.entity_type === 'interface' && endpoints.has(item.id)), edge =>
          edge.edge_type === 'network_relation' && edge.relation_kind === 'links_to' && (edge.from === port.id || edge.to === port.id))
      }
      if (run.current()) data.value = graph.snapshot()
    } catch (cause) {
      run.abort()
      if (run.active()) { graph.partial('网络展开读取失败，仅保留已加载关系。'); data.value = graph.snapshot(); error.value = topologyError(cause) }
    } finally { if (run.active()) loading.value = false }
  }
  if (getCurrentScope()) onScopeDispose(() => { disposed = true; invalidate() })
  return { rootItems, rootsNextAfter, sceneNextAfter, sceneHistory, cabinetNextAfter,
    selectedRoot, loading, error, data, mode, sourceId, rootType, loadRoots, selectRoot, nextScenePage, previousScenePage, expandNetwork, reset }
}
