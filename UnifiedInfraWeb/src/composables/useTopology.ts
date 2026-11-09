import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { topologyApi, topologyDirectoryApi } from '../api/topology'
import type { Direction, EdgeType, EntityType, TopologyGraph, TopologyIdentity, TopologySelection } from '../api/topologyTypes'
import { mergeNeighborhood, topologyError, validIdentity } from '../services/topology'

export function useTopology(api = topologyApi) {
  const sourceId = ref('')
  const entityType = ref<EntityType>('device')
  const stableId = ref('')
  const direction = ref<Direction>('both')
  const edgeType = ref<EdgeType | ''>('')
  const graph = shallowRef<TopologyGraph>({ nodes: [], edges: [] })
  const selection = shallowRef<TopologySelection | null>(null)
  const loading = ref(false)
  const error = ref('')
  const notice = ref('')
  const warnings = ref<string[]>([])
  const contextNotice = ref('')
  const rootId = ref('')
  const selectedNode = computed(() => selection.value?.kind === 'node' ? graph.value.nodes.find(node => node.id === selection.value?.id) : undefined)
  const selectedEdge = computed(() => selection.value?.kind === 'edge' ? graph.value.edges.find(edge => edge.id === selection.value?.id) : undefined)
  let controller: AbortController | undefined
  let generation = 0
  let disposed = false
  let contextRequest = false
  const current = (version: number) => !disposed && version === generation
  function invalidate() {
    generation++
    contextRequest = false
    controller?.abort()
    loading.value = false
    error.value = ''
    notice.value = ''
  }
  function reset() {
    invalidate()
    graph.value = { nodes: [], edges: [] }
    selection.value = null
    rootId.value = ''
    warnings.value = []
    contextNotice.value = ''
  }
  async function loadDeviceContext(contextApi: Pick<typeof topologyDirectoryApi, 'deviceContext'> = topologyDirectoryApi) {
    reset()
    if (disposed) return
    if (!sourceId.value || !validIdentity('device', stableId.value)) {
      error.value = '请输入有效的设备 SN，并由后台配置唯一启用的 CMDB 来源。'
      return
    }
    const version = generation
    controller = new AbortController()
    loading.value = true
    contextRequest = true
    try {
      const result = await contextApi.deviceContext({ source_id: sourceId.value, stable_id: stableId.value }, controller.signal)
      if (!current(version)) return
      if (!result?.root?.id) throw new Error('拓扑接口未返回有效设备；空响应不会生成占位节点。')
      graph.value = mergeNeighborhood({ nodes: [], edges: [] }, result)
      rootId.value = result.root.id
      selection.value = { kind: 'node', id: result.root.id }
      warnings.value = result.warnings
      contextNotice.value = result.truncated ? `设备关联路径已截断（设备视图最多 ${result.limit} 个节点，每步最多50条关系），当前展示并非完整依赖。` : ''
      notice.value = contextNotice.value
    } catch (cause) {
      if (current(version)) error.value = topologyError(cause)
    } finally { if (current(version)) {loading.value = false; contextRequest = false} }
  }
  async function fetchNeighbors(identity: TopologyIdentity, version: number, signal: AbortSignal) {
    const result = await api.neighbors({ ...identity, direction: direction.value, edge_type: edgeType.value }, signal)
    if (!current(version)) return
    graph.value = mergeNeighborhood(graph.value, result)
    notice.value = result.truncated ? `本次邻居已截断（最多 ${result.limit} 条关系），并非完整拓扑；可调整关系类型或方向。` : result.edges.length ? '已读取一跳邻居；继续展开需要手动选择节点。' : '该节点在当前关系类型与方向下没有邻居。'
  }
  async function locate() {
    reset()
    if (disposed) return
    if (!sourceId.value || !validIdentity(entityType.value, stableId.value)) {
      error.value = '请选择已启用来源，并输入有效的精确稳定 ID；电力 inst_id 必须是范围内的正整数。'
      return
    }
    const version = generation
    controller = new AbortController()
    loading.value = true
    const identity = { source_id: sourceId.value, entity_type: entityType.value, stable_id: stableId.value }
    try {
      const node = await api.node(identity, controller.signal)
      if (!current(version)) return
      if (!node?.id) throw new Error('拓扑接口未返回有效节点；空响应不会生成占位节点。')
      graph.value = { nodes: [node], edges: [] }
      rootId.value = node.id
      selection.value = { kind: 'node', id: node.id }
      await fetchNeighbors(node, version, controller.signal)
    } catch (cause) {
      if (current(version)) error.value = topologyError(cause)
    } finally { if (current(version)) loading.value = false }
  }
  async function expand() {
    const node = selectedNode.value
    if (!node || loading.value || disposed) return
    invalidate()
    const version = generation
    controller = new AbortController()
    loading.value = true
    try { await fetchNeighbors(node, version, controller.signal) }
    catch (cause) { if (current(version)) error.value = topologyError(cause) }
    finally { if (current(version)) loading.value = false }
  }
  watch([sourceId, entityType, stableId], reset, { flush: 'sync' })
  watch([direction, edgeType], () => {if (!contextRequest) invalidate()}, { flush: 'sync' })
  onScopeDispose(() => { disposed = true; invalidate() })
  return { sourceId, entityType, stableId, direction, edgeType, graph, selection, selectedNode, selectedEdge, rootId, loading, error, notice, warnings, contextNotice, locate, loadDeviceContext, expand, reset }
}
