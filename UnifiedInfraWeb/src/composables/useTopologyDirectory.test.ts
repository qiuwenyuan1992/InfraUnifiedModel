import { effectScope, ref } from 'vue'
import { expect, it, vi } from 'vitest'
import { useTopologyDirectory } from './useTopologyDirectory'
import type { TopologyNode } from '../api/topologyTypes'

const node = (entity_type: TopologyNode['entity_type'], stable_id: string): TopologyNode => ({ id: stable_id, source_id: 's', entity_type, stable_id, label: `Name ${stable_id}`, properties: {}, created_at: null, synced_at: null })
const page = (...items: TopologyNode[]) => ({ items, next_after: null })
const tick = async () => { await Promise.resolve(); await Promise.resolve() }
function fixture() {
  const source = ref('s')
  const api = { roots: vi.fn().mockResolvedValue(page(node('data_center', 'dc'))), children: vi.fn().mockResolvedValue(page()) }
  const scope = effectScope()
  const state = scope.run(() => useTopologyDirectory(source, api))!
  return { source, api, scope, state }
}

it('starts with bounded DC roots and drills DC → rooms → cabinets → devices without skipping rooms', async () => {
  const { api, scope, state } = fixture()
  try {
    await tick()
    expect(state.items.value[0]?.entity_type).toBe('data_center')
    expect(api.roots).toHaveBeenCalledWith({ source_id: 's', entity_type: 'data_center', limit: 50 }, expect.any(AbortSignal))
    api.children.mockResolvedValueOnce(page(node('room', 'room-uuid')))
    await state.open(state.items.value[0]!)
    expect(state.itemType.value).toBe('room')
    expect(state.items.value[0]?.stable_id).toBe('room-uuid')
    api.children.mockResolvedValueOnce(page(node('cabinet', 'cab')))
    await state.open(state.items.value[0]!)
    expect(api.children).toHaveBeenLastCalledWith({ source_id: 's', entity_type: 'room', stable_id: 'room-uuid', limit: 50 }, expect.any(AbortSignal))
    expect(state.itemType.value).toBe('cabinet')
    expect(state.items.value[0]?.entity_type).toBe('cabinet')
    api.children.mockResolvedValueOnce(page(node('device', 'SN')))
    await state.open(state.items.value[0]!)
    expect(state.items.value[0]?.stable_id).toBe('SN')
    expect(api.children).toHaveBeenLastCalledWith({ source_id: 's', entity_type: 'cabinet', stable_id: 'cab', limit: 50 }, expect.any(AbortSignal))
    expect(state.path.value.map(item => item.stable_id)).toEqual(['dc', 'room-uuid', 'cab'])
  } finally { scope.stop() }
})
it('POD reads direct device members and never traverses cabinet tags', async () => {
  const { api, scope, state } = fixture()
  try {
    await tick()
    api.roots.mockResolvedValueOnce(page(node('pod', 'pod')))
    state.tab.value = 'pod'
    await tick()
    api.children.mockResolvedValueOnce(page(node('device', 'SN')))
    await state.open(state.items.value[0]!)
    expect(state.items.value[0]?.entity_type).toBe('device')
    expect(api.children).toHaveBeenCalledTimes(1)
    expect(api.children).toHaveBeenCalledWith({ source_id: 's', entity_type: 'pod', stable_id: 'pod', limit: 50 }, expect.any(AbortSignal))
    await state.open(state.items.value[0]!)
    expect(api.children).toHaveBeenCalledTimes(1)
  } finally { scope.stop() }
})
it('keeps opaque cursors isolated by parent, supports previous page, breadcrumb return and reload', async () => {
  const { api, scope, state } = fixture()
  try {
    await tick()
    api.roots.mockResolvedValueOnce({ items: [node('data_center', 'dc2')], next_after: 'signed+root/==' })
    await state.reload()
    api.roots.mockResolvedValueOnce(page(node('data_center', 'dc3')))
    await state.next()
    expect(api.roots).toHaveBeenLastCalledWith({ source_id: 's', entity_type: 'data_center', limit: 50, after: 'signed+root/==' }, expect.any(AbortSignal))
    api.children.mockResolvedValueOnce({ items: [node('room', 'room')], next_after: 'signed-room' })
    await state.open(state.items.value[0]!)
    expect(api.children).toHaveBeenLastCalledWith({ source_id: 's', entity_type: 'data_center', stable_id: 'dc3', limit: 50 }, expect.any(AbortSignal))
    api.children.mockResolvedValueOnce(page(node('room', 'room2')))
    await state.next()
    expect(api.children).toHaveBeenLastCalledWith({ source_id: 's', entity_type: 'data_center', stable_id: 'dc3', limit: 50, after: 'signed-room' }, expect.any(AbortSignal))
    state.back(0)
    expect(state.items.value[0]?.stable_id).toBe('dc3')
    expect(state.pageIndex.value).toBe(1)
    api.roots.mockResolvedValueOnce(page(node('data_center', 'dc2')))
    await state.previous()
    expect(api.roots).toHaveBeenLastCalledWith({ source_id: 's', entity_type: 'data_center', limit: 50 }, expect.any(AbortSignal))
  } finally { scope.stop() }
})
it('ignores old tab, parent, source, and unmounted responses even when transport ignores abort', async () => {
  const { api, source, scope, state } = fixture()
  await tick()
  let resolve!: (value: ReturnType<typeof page>) => void
  api.children.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const pending = state.open(state.items.value[0]!)
  const childSignal = api.children.mock.calls[0]![1] as AbortSignal
  state.back(0)
  expect(childSignal.aborted).toBe(true)
  resolve(page(node('cabinet', 'stale')))
  await pending
  expect(state.items.value[0]?.stable_id).toBe('dc')
  api.roots.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  state.tab.value = 'pod'
  const tabSignal = api.roots.mock.calls.at(-1)![1] as AbortSignal
  source.value = ''
  expect(tabSignal.aborted).toBe(true)
  resolve(page(node('pod', 'stale')))
  await tick()
  expect(state.items.value).toEqual([])
  source.value = 'new-source'
  await tick()
  api.roots.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const reload = state.reload()
  const signal = api.roots.mock.calls.at(-1)![1] as AbortSignal
  scope.stop()
  expect(signal.aborted).toBe(true)
  resolve(page(node('pod', 'unmounted')))
  await reload
  expect(state.items.value.some(item => item.stable_id === 'unmounted')).toBe(false)
})
