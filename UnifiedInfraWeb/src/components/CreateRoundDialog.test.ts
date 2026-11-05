import { effectScope, nextTick, reactive, type Ref } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CreateRoundDialog from './CreateRoundDialog.vue'
import { inventoryApi } from '../api/inventory'
import type { Source } from '../api/types'

vi.mock('vue', async importOriginal => {
  const actual = await importOriginal<typeof import('vue')>()
  return { ...actual, useSSRContext: () => ({ modules: new Set<string>() }) }
})
vi.mock('../api/inventory', () => ({ inventoryApi: { create: vi.fn() } }))
vi.mock('element-plus', () => ({ ElMessage: { success: vi.fn() } }))

type State = { sourceId: Ref<string>; sourceEnabled: Ref<boolean>; submit: () => Promise<void> }
afterEach(() => vi.clearAllMocks())

describe('创建任务的来源校验', () => {
  it('已选来源停用后清空选择，拒绝提交', async () => {
    const props = reactive({ modelValue: true, sources: [{ id: 'source-a', enabled: true } as Source] })
    const scope = effectScope()
    const setup = (CreateRoundDialog as unknown as { setup: (props: unknown, context: unknown) => State }).setup
    const state = scope.run(() => setup(props, { expose: () => {}, emit: vi.fn() }))!
    try {
      state.sourceId.value = 'source-a'
      expect(state.sourceEnabled.value).toBe(true)
      await nextTick()
      props.sources[0]!.enabled = false
      await nextTick()
      expect(state.sourceId.value).toBe('')
      await state.submit()
      expect(inventoryApi.create).not.toHaveBeenCalled()
    } finally { scope.stop() }
  })

  it('失效选择尚未被watch清理时也不允许提交', async () => {
    const props = reactive({ modelValue: true, sources: [{ id: 'source-a', enabled: false } as Source] })
    const scope = effectScope()
    const setup = (CreateRoundDialog as unknown as { setup: (props: unknown, context: unknown) => State }).setup
    const state = scope.run(() => setup(props, { expose: () => {}, emit: vi.fn() }))!
    try {
      state.sourceId.value = 'source-a'
      await state.submit()
      expect(state.sourceEnabled.value).toBe(false)
      expect(inventoryApi.create).not.toHaveBeenCalled()
    } finally { scope.stop() }
  })
})
