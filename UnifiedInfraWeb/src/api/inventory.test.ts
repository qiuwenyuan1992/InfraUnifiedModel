import { afterEach, describe, expect, it, vi } from 'vitest'
import { inventoryApi, ApiError } from './inventory'

function respond(data: unknown, status = 200, code = 0) {
  return new Response(JSON.stringify({ code, message: code ? 'state_conflict' : 'ok', data }), {
    status, headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'request-1' },
  })
}

afterEach(() => vi.unstubAllGlobals())

describe('inventory API contracts', () => {
  it('retains round cursor and filters while omitting empty parameters', async () => {
    const fetcher = vi.fn().mockImplementation(async () => respond({ items: [], next_cursor: 'next' }))
    vi.stubGlobal('fetch', fetcher)
    const page = await inventoryApi.rounds({ limit: 20, cursor: 'signed+/=', source_id: 'source', status: 'failed' })
    const url = new URL(fetcher.mock.calls[0]![0], 'http://localhost')
    expect(Object.fromEntries(url.searchParams)).toEqual({ limit: '20', cursor: 'signed+/=', source_id: 'source', status: 'failed' })
    expect(page.next_cursor).toBe('next')
    await inventoryApi.rounds({ source_id: '', status: '' })
    expect(fetcher.mock.calls[1]![0]).toBe('/v1/inventory/sync-rounds')
  })

  it('posts only full source with a caller-retained idempotency key and no auth', async () => {
    const fetcher = vi.fn().mockResolvedValue(respond({ id: 'round' }, 202))
    vi.stubGlobal('fetch', fetcher)
    await inventoryApi.create('source', 'stable-key')
    const [url, init] = fetcher.mock.calls[0]!
    expect(url).toBe('/v1/inventory/sync-rounds')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ source_id: 'source', mode: 'full' })
    expect(init.headers['Idempotency-Key']).toBe('stable-key')
    expect(init.headers.Authorization).toBeUndefined()
    expect(init.credentials).toBe('omit')
  })

  it('uses diagnostic cursors and source filters without status', async () => {
    const fetcher = vi.fn().mockImplementation(async () => respond({ items: [], next_cursor: null }))
    vi.stubGlobal('fetch', fetcher)
    await inventoryApi.diagnostics('round', { limit: 50, cursor: 'cursor', source_id: 'source' }, true)
    expect(fetcher.mock.calls[0]![0]).toBe('/v1/inventory/sync-rounds/round/diagnostics?limit=50&cursor=cursor&source_id=source')
    await inventoryApi.diagnostics('child', {}, false)
    expect(fetcher.mock.calls[1]![0]).toBe('/v1/inventory/sync-runs/child/diagnostics')
  })

  it('unwraps sources and cancel responses including HTTP 202', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(respond({ items: [], next_cursor: null }))
      .mockResolvedValueOnce(respond({ id: 'round', status: 'running' }, 202))
    vi.stubGlobal('fetch', fetcher)
    expect(await inventoryApi.sources({ limit: 200 })).toEqual({ items: [], next_cursor: null })
    expect((await inventoryApi.cancel('round')).status).toBe('running')
    expect(fetcher.mock.calls[1]![0]).toBe('/v1/inventory/sync-rounds/round/cancel')
  })

  it('exposes HTTP/envelope error and request ID without hiding network errors', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond({}, 409, 40903)))
    await expect(inventoryApi.detail('round')).rejects.toMatchObject({ status: 409, code: 40903, requestId: 'request-1' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(respond({}, 200, 40903)))
    await expect(inventoryApi.detail('round')).rejects.toBeInstanceOf(ApiError)
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('network')))
    await expect(inventoryApi.detail('round')).rejects.toThrow('network')
  })

  it('rejects non-JSON proxy responses with a readable transport error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('proxy offline', { status: 502 })))
    await expect(inventoryApi.detail('round')).rejects.toMatchObject({ status: 502 })
  })
})
