import { computed, onScopeDispose, ref, shallowRef, watch, type Ref } from 'vue'
import { topologyDirectoryApi } from '../api/topology'
import type { TopologyDirectoryPage, TopologyNode, TopologyRootType } from '../api/topologyTypes'
import { topologyError } from '../services/topology'

interface DirectoryFrame extends TopologyDirectoryPage {
  parent?: TopologyNode
  afters: (string | undefined)[]
  pageIndex: number
}
export function useTopologyDirectory(sourceId: Readonly<Ref<string>>, api: Pick<typeof topologyDirectoryApi, 'roots' | 'children'> = topologyDirectoryApi) {
  const tab = ref<TopologyRootType>('data_center')
  const frames = shallowRef<DirectoryFrame[]>([])
  const loading = ref(false)
  const error = ref('')
  const frame = computed(() => frames.value.at(-1))
  const items = computed(() => frame.value?.items || [])
  const path = computed(() => frames.value.flatMap(value => value.parent ? [value.parent] : []))
  const pageIndex = computed(() => frame.value?.pageIndex || 0)
  const nextAfter = computed(() => frame.value?.next_after || null)
  const itemType = computed(() => frame.value?.parent ? frame.value.parent.entity_type === 'data_center' ? 'cabinet' : 'device' : tab.value)
  let controller: AbortController | undefined
  let generation = 0
  let disposed = false
  function invalidate() {
    generation++
    controller?.abort()
    loading.value = false
    error.value = ''
  }
  async function load(index: number, afters: (string | undefined)[]) {
    invalidate()
    const parent = frame.value?.parent
    if (disposed || !sourceId.value || !frame.value) return
    const version = generation
    controller = new AbortController()
    loading.value = true
    const paging = { limit: 50, ...(afters[index] ? { after: afters[index] } : {}) }
    try {
      const result = parent
        ? await api.children({ source_id: sourceId.value, entity_type: parent.entity_type, stable_id: parent.stable_id, ...paging }, controller.signal)
        : await api.roots({ source_id: sourceId.value, entity_type: tab.value, ...paging }, controller.signal)
      if (disposed || version !== generation) return
      frames.value = [...frames.value.slice(0, -1), { ...result, parent, afters, pageIndex: index }]
    } catch (cause) {
      if (!disposed && version === generation) error.value = topologyError(cause)
    } finally { if (!disposed && version === generation) loading.value = false }
  }
  async function restart() {
    invalidate()
    frames.value = sourceId.value ? [{ items: [], next_after: null, afters: [undefined], pageIndex: 0 }] : []
    if (sourceId.value) await load(0, [undefined])
  }
  async function open(item: TopologyNode) {
    if (disposed || loading.value || item.entity_type !== itemType.value || item.source_id !== sourceId.value || item.entity_type === 'device') return
    invalidate()
    frames.value = [...frames.value, { parent: item, items: [], next_after: null, afters: [undefined], pageIndex: 0 }]
    await load(0, [undefined])
  }
  function back(depth: number) {
    if (disposed || depth < 0 || depth >= frames.value.length) return
    invalidate()
    frames.value = frames.value.slice(0, depth + 1)
  }
  async function next() {
    const value = frame.value
    if (!value?.next_after || loading.value) return
    await load(value.pageIndex + 1, [...value.afters.slice(0, value.pageIndex + 1), value.next_after])
  }
  async function previous() {
    const value = frame.value
    if (!value || !value.pageIndex || loading.value) return
    await load(value.pageIndex - 1, value.afters)
  }
  async function reload() {
    const value = frame.value
    if (value) await load(value.pageIndex, value.afters)
  }
  watch([sourceId, tab], restart, { immediate: true, flush: 'sync' })
  onScopeDispose(() => { disposed = true; invalidate() })
  return { tab, items, path, itemType, pageIndex, nextAfter, loading, error, open, back, next, previous, reload }
}
