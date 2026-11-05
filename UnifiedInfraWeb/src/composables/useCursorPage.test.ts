import { effectScope } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { useCursorPage } from './useCursorPage'
import type { Page, RoundQuery } from '../api/types'

const settle = () => new Promise(resolve => setTimeout(resolve, 0))

describe('cursor pagination service', () => {
  it('preserves current cursor on refresh and restores cursor history', async () => {
    const scope = effectScope()
    const loader = vi.fn(async (query: RoundQuery) => ({ items: [query.cursor || 'first'], next_cursor: query.cursor ? null : 'next' }))
    const page = scope.run(() => useCursorPage<string, RoundQuery>(loader))!
    const filters = { limit: 20, status: 'failed', source_id: 'source' }
    await page.load(filters)
    page.next(filters)
    await settle()
    expect(page.items.value).toEqual(['next'])
    await page.load(filters)
    expect(loader.mock.lastCall?.[0]).toEqual({ ...filters, cursor: 'next' })
    page.previous(filters)
    await settle()
    expect(page.items.value).toEqual(['first'])
    expect(page.history.value).toEqual([])
    scope.stop()
  })

  it('resets cursor binding when filters change', async () => {
    const scope = effectScope()
    const loader = vi.fn(async (_query: RoundQuery) => ({ items: ['item'], next_cursor: 'bound-cursor' }))
    const page = scope.run(() => useCursorPage<string, RoundQuery>(loader))!
    await page.load({ status: 'failed' })
    page.next({ status: 'failed' })
    await settle()
    await page.load({ status: 'succeeded', source_id: 'new-source' }, true)
    expect(page.cursor.value).toBe('')
    expect(page.history.value).toEqual([])
    expect(loader.mock.lastCall?.[0]).toEqual({ status: 'succeeded', source_id: 'new-source', cursor: undefined })
    scope.stop()
  })

  it('aborts replaced requests and ignores late success after filter change/disposal', async () => {
    const scope = effectScope()
    const pending: Array<{ resolve: (page: Page<string>) => void; signal: AbortSignal }> = []
    const loader = (_query: RoundQuery, signal: AbortSignal) => new Promise<Page<string>>(resolve => pending.push({ resolve, signal }))
    const page = scope.run(() => useCursorPage<string, RoundQuery>(loader))!
    const obsolete = page.load({ source_id: 'old' })
    const current = page.load({ source_id: 'new' }, true)
    expect(pending[0]!.signal.aborted).toBe(true)
    pending[1]!.resolve({ items: ['new'], next_cursor: null })
    await current
    pending[0]!.resolve({ items: ['old'], next_cursor: 'old-cursor' })
    await obsolete
    expect(page.items.value).toEqual(['new'])
    expect(page.nextCursor.value).toBeNull()
    const afterClose = page.load({})
    scope.stop()
    expect(pending[2]!.signal.aborted).toBe(true)
    pending[2]!.resolve({ items: ['closed'], next_cursor: null })
    await afterClose
    expect(page.items.value).toEqual(['new'])
  })

  it('exposes a readable network error and allows retry without losing cursor', async () => {
    const scope = effectScope()
    let fail = true
    const loader = async () => {
      if (fail) throw new TypeError('offline')
      return { items: ['recovered'], next_cursor: null }
    }
    const page = scope.run(() => useCursorPage<string, RoundQuery>(loader))!
    await page.load({})
    expect(page.error.value).toContain('网络连接失败')
    expect(page.loading.value).toBe(false)
    fail = false
    await page.load({})
    expect(page.error.value).toBe('')
    expect(page.items.value).toEqual(['recovered'])
    scope.stop()
  })
})
