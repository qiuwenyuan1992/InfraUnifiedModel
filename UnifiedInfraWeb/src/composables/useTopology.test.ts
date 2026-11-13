import { effectScope } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useTopology } from './useTopology'
import { ApiError } from '../api/inventory'
import type { DeviceContext, Neighborhood, NeighborQuery, RoomPowerContext, TopologyNode } from '../api/topologyTypes'
import { validIdentity } from '../services/topology'

const node = (id: string): TopologyNode => ({ id, source_id: 'source', entity_type: 'device', stable_id: id, label: id, properties: {}, created_at: null, synced_at: null })
const neighbors = (id: string): Neighborhood => ({ root: node(id), nodes: [], edges: [], truncated: false, limit: 100 })
const context = (id: string): DeviceContext => ({ ...neighbors(id), warnings: [] })
const roomContext = (id: string, powerMissing: string[] = []): RoomPowerContext => ({ ...neighbors(id), warnings: [], power_missing: powerMissing })
const contextApi = () => ({ deviceContext: vi.fn(async () => context('a')) })
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
    const context = { ...neighbors('a'), nodes: [{ ...node('cab'), source_id: 'cab-source', entity_type: 'cabinet' as const, stable_id: 'cab-uuid' }], edges: ['A', 'B'].map(rank => ({ id: rank, source_id: 'source', edge_type: 'power_relation' as const, relation_kind: 'power_upstream', from: 'a', to: 'cab', rank, properties: { path: rank }, created_at: null, synced_at: null })), warnings: ['缺少上联关系'], truncated: true, limit: 50 }
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
    expect(api.neighbors).toHaveBeenCalledWith(expect.objectContaining({ source_id: 'cab-source', entity_type: 'cabinet', stable_id: 'cab-uuid' }), expect.any(AbortSignal))
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
  it('reads one-hop only on manual expansion; selection and filter changes do not fetch', async () => {
    const api = { node: vi.fn(), neighbors: vi.fn(async (_query: NeighborQuery) => neighbors('a')) }
    const { state } = setup(api)
    await state.loadDeviceContext(contextApi())
    state.selection.value = { kind: 'node', id: 'a' }
    state.direction.value = 'out'
    state.edgeType.value = 'power_relation'
    expect(api.node).not.toHaveBeenCalled()
    expect(api.neighbors).not.toHaveBeenCalled()
    await state.expand()
    expect(api.neighbors).toHaveBeenCalledTimes(1)
    expect(state.notice.value).toContain('没有邻居')
    expect(api.neighbors.mock.lastCall?.[0]).toMatchObject({ source_id: 'source', entity_type: 'device', stable_id: 'a', direction: 'out', edge_type: 'power_relation' })
  })
  it('aborts and ignores stale device context after loading a new root', async () => {
    const old = deferred<DeviceContext>()
    const api = { node: vi.fn(), neighbors: vi.fn() }
    const deviceApi = { deviceContext: vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(context('b')) }
    const { state } = setup(api)
    const obsolete = state.loadDeviceContext(deviceApi)
    const signal = deviceApi.deviceContext.mock.calls[0]![1] as AbortSignal
    state.stableId.value = 'b'
    expect(signal.aborted).toBe(true)
    await state.loadDeviceContext(deviceApi)
    old.resolve({ ...context('a'), warnings: ['stale'] })
    await obsolete
    expect(state.graph.value.nodes.map(item => item.id)).toEqual(['b'])
    expect(state.warnings.value).toEqual([])
    expect(api.neighbors).not.toHaveBeenCalled()
    expect(state.rootId.value).toBe('b')
  })
  it('does not apply stale expansion responses or context errors after switching source', async () => {
    const stale = deferred<Neighborhood>()
    const api = { node: vi.fn(), neighbors: vi.fn().mockReturnValue(stale.promise) }
    const { state } = setup(api)
    await state.loadDeviceContext(contextApi())
    const request = state.expand()
    const signal = api.neighbors.mock.calls[0]![1] as AbortSignal
    state.sourceId.value = 'new-source'
    expect(signal.aborted).toBe(true)
    stale.resolve(neighbors('a'))
    await request
    expect(state.graph.value).toEqual({ nodes: [], edges: [] })
    expect(state.selection.value).toBeNull()
    expect(state.notice.value).toBe('')
    expect(state.loading.value).toBe(false)
    const staleError = deferred<DeviceContext>()
    const deviceApi = { deviceContext: vi.fn((_identity: unknown, _signal?: AbortSignal) => staleError.promise) }
    const failed = state.loadDeviceContext(deviceApi)
    state.sourceId.value = 'third'
    expect(deviceApi.deviceContext.mock.calls[0]![1]!.aborted).toBe(true)
    staleError.reject(new Error('obsolete error'))
    await failed
    expect(state.error.value).toBe('')
  })
  it('cleans up on unmount and never applies a late expansion response', async () => {
    const late = deferred<Neighborhood>()
    const api = { node: vi.fn(), neighbors: vi.fn().mockReturnValue(late.promise) }
    const { state, scope } = setup(api)
    await state.loadDeviceContext(contextApi())
    const request = state.expand()
    scope.stop()
    expect((api.neighbors.mock.lastCall?.[1] as AbortSignal).aborted).toBe(true)
    late.resolve({ ...neighbors('a'), nodes: [node('late')] })
    await request
    expect(state.graph.value.nodes).toHaveLength(1)
    expect(state.loading.value).toBe(false)
  })
  it('clears old graph on missing device and rejects empty context without a fabricated point', async () => {
    const api = { node: vi.fn(), neighbors: vi.fn() }
    const deviceApi = { deviceContext: vi.fn().mockResolvedValueOnce(context('a')).mockRejectedValueOnce(new ApiError(404, 40401, 'not_found', 'r1')).mockResolvedValueOnce(null) }
    const { state } = setup(api)
    await state.loadDeviceContext(deviceApi)
    await state.loadDeviceContext(deviceApi)
    expect(state.graph.value.nodes).toEqual([])
    expect(state.error.value).toContain('未找到')
    await state.loadDeviceContext(deviceApi)
    expect(state.graph.value.nodes).toEqual([])
    expect(state.error.value).toContain('空响应')
    expect(api.node).not.toHaveBeenCalled()
    expect(api.neighbors).not.toHaveBeenCalled()
  })
  it('shows BAD_TYPE rather than swallowing it and reports truncation without extra fetches', async () => {
    const api = { node: vi.fn(), neighbors: vi.fn().mockRejectedValueOnce(new ApiError(503, 50301, 'BAD_TYPE', 'r1')).mockResolvedValueOnce({ ...neighbors('a'), truncated: true }) }
    const { state } = setup(api)
    await state.loadDeviceContext(contextApi())
    await state.expand()
    expect(state.error.value).toContain('BAD_TYPE')
    expect(state.graph.value.nodes).toHaveLength(1)
    await state.expand()
    expect(state.error.value).toBe('')
    expect(state.notice.value).toContain('截断')
    expect(api.neighbors).toHaveBeenCalledTimes(2)
  })
  it('rejects missing source and blank device SN before making context requests', async () => {
    const api = { node: vi.fn(), neighbors: vi.fn() }
    const deviceApi = contextApi()
    const { state } = setup(api)
    for (const id of ['', '   ']) {
      state.stableId.value = id
      await state.loadDeviceContext(deviceApi)
      expect(state.error.value).toContain('有效的设备 SN')
    }
    state.stableId.value = 'a'
    state.sourceId.value = ''
    await state.loadDeviceContext(deviceApi)
    expect(state.error.value).toContain('有效的设备 SN')
    expect(deviceApi.deviceContext).not.toHaveBeenCalled()
    expect(api.node).not.toHaveBeenCalled()
    expect(api.neighbors).not.toHaveBeenCalled()
  })
  it('keeps public identity validation for invalid power identifiers', () => {
    for (const id of ['0', '-1', '01', '9.1', '9223372036854775808', 'Name']) {
      expect(validIdentity('ups', id)).toBe(false)
    }
  })
  it('invalidates in-flight expansion on filter changes without deleting visible graph', async () => {
    const late = deferred<Neighborhood>()
    const api = { node: vi.fn(), neighbors: vi.fn().mockReturnValue(late.promise) }
    const { state } = setup(api)
    await state.loadDeviceContext(contextApi())
    const request = state.expand()
    state.edgeType.value = 'network_relation'
    expect((api.neighbors.mock.lastCall?.[1] as AbortSignal).aborted).toBe(true)
    late.resolve({ ...neighbors('a'), nodes: [node('obsolete')] })
    await request
    expect(state.graph.value.nodes.map(item => item.id)).toEqual(['a'])
  })
  it('loads room power view with missing-power markers and truncation notice', async () => {
    const api = { node: vi.fn(), neighbors: vi.fn() }
    const power = { ...roomContext('room-uuid', ['cab-1', 'cab-2']), nodes: [node('room-uuid'), { ...node('cab-1'), entity_type: 'cabinet' as const }, { ...node('cab-2'), entity_type: 'cabinet' as const }], truncated: true, limit: 600, warnings: ['room_power_truncated'] }
    const powerApi = { roomPower: vi.fn(async () => power) }
    const { state } = setup(api)
    await state.loadRoomPower('room-uuid', powerApi)
    expect(powerApi.roomPower).toHaveBeenCalledWith({ source_id: 'source', stable_id: 'room-uuid' }, expect.any(AbortSignal))
    expect(state.rootId.value).toBe('room-uuid')
    expect(state.graph.value.nodes).toHaveLength(3)
    expect(state.powerView.value).toBe(true)
    expect(state.powerMissing.value).toEqual(['cab-1', 'cab-2'])
    expect(state.warnings.value).toEqual(['room_power_truncated'])
    expect(state.notice.value).toContain('截断')
    // 目录返回清空供电视图状态，不留旧缺失标记。
    state.reset()
    expect(state.powerView.value).toBe(false)
    expect(state.powerMissing.value).toEqual([])
  })
  it('rejects blank room stable ID before requesting the power view', async () => {
    const api = { node: vi.fn(), neighbors: vi.fn() }
    const powerApi = { roomPower: vi.fn(async () => roomContext('room-uuid')) }
    const { state } = setup(api)
    await state.loadRoomPower('  ', powerApi)
    expect(state.error.value).toContain('房间身份无效')
    expect(powerApi.roomPower).not.toHaveBeenCalled()
  })
})
