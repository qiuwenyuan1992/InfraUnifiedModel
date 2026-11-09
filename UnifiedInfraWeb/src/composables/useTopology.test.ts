import { effectScope } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useTopology } from './useTopology'
import { ApiError } from '../api/inventory'
import type { Neighborhood, NeighborQuery, TopologyNode } from '../api/topologyTypes'

const node = (id: string): TopologyNode => ({ id, source_id: 'source', entity_type: 'device', stable_id: id, label: id, properties: {}, created_at: null, synced_at: null })
const neighbors = (id: string): Neighborhood => ({ root: node(id), nodes: [], edges: [], truncated: false, limit: 100 })
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const scopes: ReturnType<typeof effectScope>[] = []
function setup(api: Parameters<typeof useTopology>[0]) {
  const scope = effectScope()
  scopes.push(scope)
  const state = scope.run(() => useTopology(api))!
  state.sourceId.value = 'source'
  state.stableId.value = 'a'
  return { state, scope }
}
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()) })

describe('topology request lifecycle', () => {
  it('changing manual filters does not cancel automatic device context', async () => {
    const late = deferred<Neighborhood & { warnings: string[] }>()
    const contextApi = { deviceContext: vi.fn((_identity: unknown, _signal?: AbortSignal) => late.promise) }
    const { state } = setup({ node: vi.fn(), neighbors: vi.fn() })
    const pending = state.loadDeviceContext(contextApi)
    const signal = contextApi.deviceContext.mock.calls[0]![1]!
    state.direction.value = 'in'
    state.edgeType.value = 'power_relation'
    expect(signal.aborted).toBe(false)
    late.resolve({ ...neighbors('a'), warnings: [] })
    await pending
    expect(state.graph.value.nodes.map(node => node.id)).toEqual(['a'])
    expect(state.loading.value).toBe(false)
  })
  it('loads device context once, keeps real warnings and A/B edges, then expands only manually', async () => {
    const api = { node: vi.fn(), neighbors: vi.fn(async () => neighbors('a')) }
    const context = { ...neighbors('a'), nodes: [node('cab')], edges: ['A', 'B'].map(rank => ({ id: rank, source_id: 'source', edge_type: 'power_relation' as const, relation_kind: 'power_upstream', from: 'a', to: 'cab', rank, properties: { path: rank }, created_at: null, synced_at: null })), warnings: ['缺少上联关系'], truncated: true, limit: 50 }
    const contextApi = { deviceContext: vi.fn(async () => context) }
    const { state } = setup(api)
    await state.loadDeviceContext(contextApi)
    expect(state.graph.value.edges.map(edge => edge.rank)).toEqual(['A', 'B'])
    expect(state.warnings.value).toEqual(['缺少上联关系'])
    expect(state.notice.value).toContain('截断')
    expect(state.rootId.value).toBe('a')
    expect(api.node).not.toHaveBeenCalled()
    expect(api.neighbors).not.toHaveBeenCalled()
    state.selection.value = { kind: 'node', id: 'cab' }
    await state.expand()
    expect(api.neighbors).toHaveBeenCalledTimes(1)
    expect(state.warnings.value).toEqual(['缺少上联关系'])
  })
  it('aborts old device context on directory reset and preserves no stale graph or warnings', async () => {
    const old = deferred<Neighborhood & { warnings: string[] }>()
    const contextApi = { deviceContext: vi.fn((_identity: { source_id: string; stable_id: string }, _signal?: AbortSignal) => old.promise) }
    const { state } = setup({ node: vi.fn(), neighbors: vi.fn() })
    const pending = state.loadDeviceContext(contextApi)
    const signal = contextApi.deviceContext.mock.calls[0]![1] as AbortSignal
    state.reset()
    expect(signal.aborted).toBe(true)
    old.resolve({ ...neighbors('a'), warnings: ['stale'] })
    await pending
    expect(state.graph.value).toEqual({ nodes: [], edges: [] })
    expect(state.warnings.value).toEqual([])
  })
  it('only reads automatic one-hop after explicit locate; selection and filter changes do not fetch', async () => {
    const api = { node: vi.fn(async () => node('a')), neighbors: vi.fn(async (_query: NeighborQuery) => neighbors('a')) }
    const { state } = setup(api)
    expect(api.node).not.toHaveBeenCalled()
    await state.locate()
    expect(api.neighbors).toHaveBeenCalledTimes(1)
    expect(state.notice.value).toContain('没有邻居')
    state.selection.value = { kind: 'node', id: 'a' }
    state.direction.value = 'out'
    state.edgeType.value = 'power_relation'
    expect(api.neighbors).toHaveBeenCalledTimes(1)
    await state.expand()
    expect(api.neighbors).toHaveBeenCalledTimes(2)
    expect(api.neighbors.mock.lastCall?.[0]).toMatchObject({ stable_id: 'a', direction: 'out', edge_type: 'power_relation' })
  })
  it('aborts and ignores stale node responses after a new root is located', async () => {
    const old = deferred<TopologyNode>()
    const api = { node: vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(node('b')), neighbors: vi.fn(async () => neighbors('b')) }
    const { state } = setup(api)
    const obsolete = state.locate()
    const signal = api.node.mock.calls[0]![1] as AbortSignal
    state.stableId.value = 'b'
    expect(signal.aborted).toBe(true)
    await state.locate()
    old.resolve(node('a'))
    await obsolete
    expect(state.graph.value.nodes.map(item => item.id)).toEqual(['b'])
    expect(api.neighbors).toHaveBeenCalledTimes(1)
    expect(state.rootId.value).toBe('b')
  })
  it('does not apply stale neighbor responses or errors after switching source', async () => {
    const stale = deferred<Neighborhood>()
    const api = { node: vi.fn(async () => node('a')), neighbors: vi.fn().mockReturnValue(stale.promise) }
    const { state } = setup(api)
    const request = state.locate()
    await Promise.resolve()
    const signal = api.neighbors.mock.calls[0]![1] as AbortSignal
    state.sourceId.value = 'new-source'
    expect(signal.aborted).toBe(true)
    stale.resolve(neighbors('a'))
    await request
    expect(state.graph.value).toEqual({ nodes: [], edges: [] })
    expect(state.selection.value).toBeNull()
    expect(state.notice.value).toBe('')
    expect(state.loading.value).toBe(false)
    const staleError = deferred<TopologyNode>()
    api.node.mockImplementation(() => staleError.promise)
    const failed = state.locate()
    state.sourceId.value = 'third'
    staleError.reject(new Error('obsolete error'))
    await failed
    expect(state.error.value).toBe('')
  })
  it('cleans up on unmount and never applies a late expansion response', async () => {
    const late = deferred<Neighborhood>()
    const api = { node: vi.fn(async () => node('a')), neighbors: vi.fn().mockResolvedValueOnce(neighbors('a')).mockReturnValueOnce(late.promise) }
    const { state, scope } = setup(api)
    await state.locate()
    const request = state.expand()
    scope.stop()
    expect((api.neighbors.mock.lastCall?.[1] as AbortSignal).aborted).toBe(true)
    late.resolve({ ...neighbors('a'), nodes: [node('late')] })
    await request
    expect(state.graph.value.nodes).toHaveLength(1)
    expect(state.loading.value).toBe(false)
  })
  it('clears old graph on missing node and rejects empty response without a fabricated point', async () => {
    const api = { node: vi.fn().mockResolvedValueOnce(node('a')).mockRejectedValueOnce(new ApiError(404, 40401, 'not_found', 'r1')).mockResolvedValueOnce(null), neighbors: vi.fn(async () => neighbors('a')) }
    const { state } = setup(api)
    await state.locate()
    await state.locate()
    expect(state.graph.value.nodes).toEqual([])
    expect(state.error.value).toContain('未找到')
    await state.locate()
    expect(state.graph.value.nodes).toEqual([])
    expect(state.error.value).toContain('空响应')
    expect(api.neighbors).toHaveBeenCalledTimes(1)
  })
  it('shows BAD_TYPE rather than swallowing it and reports truncation without extra fetches', async () => {
    const api = { node: vi.fn(async () => node('a')), neighbors: vi.fn().mockRejectedValueOnce(new ApiError(503, 50301, 'BAD_TYPE', 'r1')).mockResolvedValueOnce({ ...neighbors('a'), truncated: true }) }
    const { state } = setup(api)
    await state.locate()
    expect(state.error.value).toContain('BAD_TYPE')
    expect(state.graph.value.nodes).toHaveLength(1)
    await state.expand()
    expect(state.error.value).toBe('')
    expect(state.notice.value).toContain('截断')
    expect(api.neighbors).toHaveBeenCalledTimes(2)
  })
  it('rejects invalid power identifiers before making API requests', async () => {
    const api = { node: vi.fn(async () => node('a')), neighbors: vi.fn(async (_query: NeighborQuery) => neighbors('a')) }
    const { state } = setup(api)
    state.entityType.value = 'ups'
    for (const id of ['0', '-1', '01', '9.1', '9223372036854775808', 'Name']) {
      state.stableId.value = id
      await state.locate()
      expect(state.error.value).toContain('正整数')
    }
    expect(api.node).not.toHaveBeenCalled()
  })
  it('invalidates in-flight expansion on filter changes without deleting visible graph', async () => {
    const late = deferred<Neighborhood>()
    const api = { node: vi.fn(async () => node('a')), neighbors: vi.fn().mockResolvedValueOnce(neighbors('a')).mockReturnValueOnce(late.promise) }
    const { state } = setup(api)
    await state.locate()
    const request = state.expand()
    state.edgeType.value = 'network_relation'
    expect((api.neighbors.mock.lastCall?.[1] as AbortSignal).aborted).toBe(true)
    late.resolve({ ...neighbors('a'), nodes: [node('obsolete')] })
    await request
    expect(state.graph.value.nodes.map(item => item.id)).toEqual(['a'])
  })
})
