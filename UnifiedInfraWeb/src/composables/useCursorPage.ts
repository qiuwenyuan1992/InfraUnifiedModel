import { onScopeDispose, ref, shallowRef } from 'vue'
import type { Page, PageQuery } from '../api/types'
import { errorText } from '../services/display'

export function useCursorPage<T, Q extends PageQuery>(loader: (query: Q, signal: AbortSignal) => Promise<Page<T>>) {
  const items = shallowRef<T[]>([])
  const loading = ref(false)
  const error = ref('')
  const cursor = ref('')
  const nextCursor = ref<string | null>(null)
  const history = ref<string[]>([])
  let controller: AbortController | undefined
  let generation = 0

  async function load(filters: Q, reset = false) {
    controller?.abort()
    const version = ++generation
    controller = new AbortController()
    if (reset) {
      cursor.value = ''
      history.value = []
      items.value = []
      nextCursor.value = null
    }
    error.value = ''
    loading.value = true
    try {
      const page = await loader({ ...filters, cursor: cursor.value || undefined }, controller.signal)
      if (version !== generation) return
      items.value = page.items
      nextCursor.value = page.next_cursor
    } catch (cause) {
      if (version === generation && !controller.signal.aborted) error.value = errorText(cause)
    } finally {
      if (version === generation) loading.value = false
    }
  }
  function next(filters: Q) {
    if (!nextCursor.value || loading.value) return
    history.value.push(cursor.value)
    cursor.value = nextCursor.value
    items.value = []
    void load(filters)
  }
  function previous(filters: Q) {
    if (!history.value.length || loading.value) return
    cursor.value = history.value.pop() || ''
    items.value = []
    void load(filters)
  }
  onScopeDispose(() => { generation++; controller?.abort() })
  return { items, loading, error, cursor, nextCursor, history, load, next, previous }
}
