import { getCurrentScope, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { topologyApi, topologyDirectoryApi, topologySceneApi } from '../api/topology'
import { sceneTopologyEdge, sceneTopologyNode, type SceneNode, type SceneQuery, type SceneResponse } from '../api/newSceneTypes'
import type { Neighborhood, NeighborQuery, TopologyChildQuery, TopologyDirectoryPage, TopologyNode, TopologyRootQuery, TopologyRootType } from '../api/topologyTypes'
import { topologyError } from '../services/topology'
import { isSpatialNetworkDevice, type SpatialSceneData, type SpatialSceneMode } from '../services/spatialScene'

export interface SpatialSceneApi {
  roots(query: TopologyRootQuery, signal?: AbortSignal): Promise<TopologyDirectoryPage>
  scene(query: SceneQuery, signal?: AbortSignal): Promise<SceneResponse>
  children(query: TopologyChildQuery, signal?: AbortSignal): Promise<TopologyDirectoryPage>
  neighbors(query: NeighborQuery, signal?: AbortSignal): Promise<Neighborhood>
}
const empty = (): SpatialSceneData => ({ nodes: [], edges: [], root: null, truncated: false, warnings: [] })
export function useSpatialScene(api: SpatialSceneApi = { ...topologyDirectoryApi, ...topologyApi, ...topologySceneApi }) {
  const rootItems = shallowRef<TopologyNode[]>([])
  const rootsNextAfter = ref<string | null>(null)
  const sceneNextAfter = ref<string | null>(null)
  const sceneHistory = ref<(string | undefined)[]>([])
  const sceneAfter = ref<string | undefined>()
  const cabinetNextAfter = shallowRef<Record<string, string>>({})
  const scenePage = shallowRef<SceneResponse | null>(null)
  function resetScenePage() {
    sceneNextAfter.value = null
    sceneHistory.value = []
    sceneAfter.value = undefined
    cabinetNextAfter.value = {}
    scenePage.value = null
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
      nodes.set(node.id, node)
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
    return { nodes, edges, warnings, assignmentStatus, partial, addNode, merge,
      snapshot: (): SpatialSceneData => ({ root, nodes: [...nodes.values()], edges: [...edges.values()], truncated, warnings: [...warnings], assignmentStatus }) }
  }
  async function loadScenePage(node: TopologyNode, after?: string, pageHistory: (string | undefined)[] = []) {
    if (disposed || node.source_id !== sourceId.value || node.entity_type !== rootType.value) return
    const run = start()
    selectedRoot.value = node
    data.value = empty()
    cabinetNextAfter.value = {}
    scenePage.value = null
    sceneNextAfter.value = null
    try {
      const page = await api.scene({ source_id: node.source_id, entity_type: rootType.value,
        stable_id: node.stable_id, limit: 5, ...(after ? { after } : {}) }, run.signal)
      if (!run.current()) return
      if (page.root.source_id !== node.source_id || page.root.entity_type !== node.entity_type
        || page.root.stable_id !== node.stable_id || page.root.id !== node.id) throw new Error('场景根节点与当前来源或选择不匹配。')
      const nodes = new Map<string, TopologyNode>()
      const warnings = new Set(page.warnings)
      let truncated = page.truncated
      function add(item: SceneNode) {
        if (item.source_id !== node.source_id) { warnings.add('已忽略不同来源的节点。'); truncated = true; return }
        if (!nodes.has(item.id)) nodes.set(item.id, sceneTopologyNode(item))
      }
      add(page.root)
      page.nodes.forEach(add)
      const cursors: Record<string, string> = {}
      for (const cabinet of page.cabinets) {
        if (cabinet.node.source_id !== node.source_id || cabinet.node.entity_type !== 'cabinet') {
          warnings.add('已忽略无效机柜层级。'); truncated = true; continue
        }
        add(cabinet.node)
        cabinet.devices.forEach(add)
        if (cabinet.next_after) {
          cursors[cabinet.node.id] = cabinet.next_after
          warnings.add(`${cabinet.node.label || cabinet.node.stable_id}：该机柜还有设备未加载`)
          truncated = true
        }
      }
      page.unassigned.forEach(item => add(item.node))
      page.network_levels.forEach(level => level.nodes.forEach(add))
      const edges = new Map<string, ReturnType<typeof sceneTopologyEdge>>()
      for (const edge of page.edges) {
        if (edge.source_id !== node.source_id || !nodes.has(edge.from) || !nodes.has(edge.to)) {
          warnings.add('已忽略来源或端点不匹配的关系。'); truncated = true; continue
        }
        if (!edges.has(edge.id)) edges.set(edge.id, sceneTopologyEdge(edge))
      }
      if (page.next_after) { warnings.add('当前场景还有下一页未加载。'); truncated = true }
      const root = sceneTopologyNode(page.root)
      selectedRoot.value = root
      data.value = { root, nodes: [...nodes.values()], edges: [...edges.values()], truncated, warnings: [...warnings], assignmentStatus: Object.fromEntries(page.unassigned.map(item => [item.node.id, item.status])) }
      cabinetNextAfter.value = cursors
      scenePage.value = page
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
    if (disposed || loading.value || !root || node.source_id !== sourceId.value || !isSpatialNetworkDevice(node)
      || !data.value.nodes.some(item => item.id === node.id)) return
    const run = start()
    const graph = collector(root, data.value)
    let requests = 0
    let budgetReached = false
    const sameSourceEdge = (edge: Neighborhood['edges'][number]) => edge.source_id === root.source_id
    async function read(target: TopologyNode, edge_type: NeighborQuery['edge_type']) {
      if (!run.current()) return null
      if (requests >= 16) { budgetReached = true; return null }
      requests++
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
      const remoteIds = new Set<string>()
      const portResults = await run.boundedMap(ports.slice(0, 7), async port => ({ port, incoming: await read(port, 'network_relation') }))
      for (const result of portResults) {
        if (!result?.incoming) continue
        const { port, incoming } = result
        const endpoints = new Set(incoming.edges.filter(edge => sameSourceEdge(edge) && edge.edge_type === 'network_relation' && edge.relation_kind === 'links_to'
          && (edge.from === port.id || edge.to === port.id)).flatMap(edge => [edge.from, edge.to]))
        graph.merge(incoming, item => graph.nodes.has(item.id) || (item.entity_type === 'interface' && endpoints.has(item.id)), edge =>
          edge.edge_type === 'network_relation' && edge.relation_kind === 'links_to' && (edge.from === port.id || edge.to === port.id))
        endpoints.forEach(id => { if (!portIds.has(id)) remoteIds.add(id) })
      }
      const unresolved = [...remoteIds].filter(id => ![...graph.edges.values()].some(edge => edge.relation_kind === 'owns_interface' && edge.to === id))
      if (unresolved.length > 8) graph.partial('仅核验前 8 个远端端口的真实设备归属。')
      const remotePorts = unresolved.slice(0, 8).map(id => graph.nodes.get(id)).filter((port): port is TopologyNode => !!port)
      const ownerResults = await run.boundedMap(remotePorts, async port => ({ port, incoming: await read(port, 'composition_relation') }))
      for (const result of ownerResults) {
        if (!result?.incoming) continue
        const { port, incoming } = result
        if (incoming.truncated) graph.assignmentStatus[port.id] = 'truncated'
        const owners = new Set(incoming.edges.filter(edge => sameSourceEdge(edge) && edge.edge_type === 'composition_relation'
          && edge.relation_kind === 'owns_interface' && edge.to === port.id).map(edge => edge.from))
        const remote = [incoming.root, ...incoming.nodes].filter(item => item.source_id === root.source_id
          && item.entity_type === 'device' && owners.has(item.id) && !graph.nodes.has(item.id))
        graph.merge(incoming, item => graph.nodes.has(item.id) || (item.entity_type === 'device' && owners.has(item.id)), edge =>
          edge.edge_type === 'composition_relation' && edge.relation_kind === 'owns_interface' && edge.to === port.id && owners.has(edge.from))
        if (remote.length) graph.warnings.add('显式网络展开包含远端设备：仅有真实端口互联，未核验机柜或数据中心归属；不表示属于当前范围。')
      }
      if (budgetReached) graph.partial('单次网络展开达到 16 次读取预算。')
      if (run.current()) data.value = graph.snapshot()
    } catch (cause) {
      run.abort()
      if (run.active()) { graph.partial('网络展开读取失败，仅保留已加载关系。'); data.value = graph.snapshot(); error.value = topologyError(cause) }
    } finally { if (run.active()) loading.value = false }
  }
  if (getCurrentScope()) onScopeDispose(() => { disposed = true; invalidate() })
  return { rootItems, rootsNextAfter, sceneNextAfter, sceneHistory, sceneAfter, cabinetNextAfter, scenePage,
    selectedRoot, loading, error, data, mode, sourceId, rootType, loadRoots, selectRoot, nextScenePage, previousScenePage, expandNetwork, reset }
}
