import { afterEach, expect, it, vi } from 'vitest'
import { topologyApi, topologyDirectoryApi } from './topology'
import { ApiError } from './inventory'

afterEach(() => vi.unstubAllGlobals())
it('reads bounded roots, parent children and device context with opaque cursors via GET only', async () => {
  const data = { items: [], next_after: 'opaque+/==' }
  const fetcher = vi.fn(async () => new Response(JSON.stringify({ code: 0, data })))
  vi.stubGlobal('fetch', fetcher)
  const controller = new AbortController()
  await topologyDirectoryApi.roots({ source_id: 'source', entity_type: 'pod', limit: 50 }, controller.signal)
  await topologyDirectoryApi.children({ source_id: 'source', entity_type: 'pod', stable_id: 'uuid', limit: 50, after: 'opaque+/==' }, controller.signal)
  await topologyDirectoryApi.deviceContext({ source_id: 'source', stable_id: 'SN +/&' }, controller.signal)
  const calls = fetcher.mock.calls as unknown as [string, RequestInit][]
  const urls = calls.map(([url]) => new URL(url, 'http://localhost'))
  expect(urls.map(url => url.pathname)).toEqual(['/v1/inventory/topology/roots', '/v1/inventory/topology/children', '/v1/inventory/topology/device-context'])
  expect(Object.fromEntries(urls[0]!.searchParams)).toEqual({ source_id: 'source', entity_type: 'pod', limit: '50' })
  expect(Object.fromEntries(urls[1]!.searchParams)).toEqual({ source_id: 'source', entity_type: 'pod', stable_id: 'uuid', limit: '50', after: 'opaque+/==' })
  expect(Object.fromEntries(urls[2]!.searchParams)).toEqual({ source_id: 'source', stable_id: 'SN +/&' })
  expect(calls.every(([, init]) => init.method === 'GET' && init.signal === controller.signal && init.credentials === 'omit')).toBe(true)
})
it('uses only GET exact identity queries, envelope data, optional edge enum, and abort signal', async () => {
  const identity = { source_id: 's+/&', entity_type: 'device' as const, stable_id: 'SN+ /&' }
  const point = { ...identity, id: 'point', label: 'asset', properties: {}, created_at: null, synced_at: null }
  const data = { root: point, nodes: [], edges: [], truncated: false, limit: 100 }
  const fetcher = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ code: 0, data: point })))
    .mockImplementation(async () => new Response(JSON.stringify({ code: 0, data })))
  vi.stubGlobal('fetch', fetcher)
  const controller = new AbortController()
  expect(await topologyApi.node(identity, controller.signal)).toEqual(point)
  await topologyApi.neighbors({ ...point, direction: 'both', edge_type: '' }, controller.signal)
  await topologyApi.neighbors({ ...identity, direction: 'in', edge_type: 'power_relation' })
  const calls = fetcher.mock.calls as unknown as [string, RequestInit][]
  expect(new URL(calls[0]![0], 'http://localhost').pathname).toBe('/v1/inventory/topology/node')
  expect(Object.fromEntries(new URL(calls[1]![0], 'http://localhost').searchParams)).toEqual({ ...identity, direction: 'both', limit: '100' })
  expect(calls[2]![0]).toContain('edge_type=power_relation')
  expect(calls[0]![1]).toMatchObject({ method: 'GET', signal: controller.signal, credentials: 'omit' })
  expect((calls[0]![1].headers as Record<string, string>).Authorization).toBeUndefined()
})
it('does not swallow missing nodes, BAD_TYPE, auth, or unavailable graph errors', async () => {
  for (const status of [400, 401, 403, 404, 503]) {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ code: 123, message: 'BAD_TYPE', data: null }), { status })))
    await expect(topologyApi.node({ source_id: 's', entity_type: 'device', stable_id: 'sn' })).rejects.toBeInstanceOf(ApiError)
  }
})
