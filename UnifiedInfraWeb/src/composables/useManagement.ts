import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { inventoryApi } from '../api/inventory'
import type { Round, Source } from '../api/types'
import { errorText, isActive } from '../services/display'
import { createPoller } from '../services/polling'
import { useCursorPage } from './useCursorPage'

export function useManagement() {
  const section = ref('rounds')
  const sources = shallowRef<Source[]>([])
  const sourcesLoading = ref(false)
  const sourcesLoaded = ref(false)
  const sourcesError = ref('')
  const sourceFilter = ref('')
  const statusFilter = ref('')
  const pageSize = ref(20)
  const filters = computed(() => ({ source_id: sourceFilter.value, status: statusFilter.value, limit: pageSize.value }))
  const page = useCursorPage(inventoryApi.rounds)
  const selectedId = ref('')
  const selected = shallowRef<Round | null>(null)
  const detailLoading = ref(false)
  const detailError = ref('')
  const cancelLoading = ref(false)
  const createOpen = ref(false)
  const diagnostic = shallowRef<{ id: string; isRound: boolean; sourceId: string } | null>(null)
  let sourceController: AbortController | undefined
  let detailController: AbortController | undefined
  let detailGeneration = 0
  let disposed = false

  async function loadSources() {
    if (sourcesLoading.value) return
    sourcesLoading.value = true
    sourcesError.value = ''
    sourceController = new AbortController()
    const loaded: Source[] = []
    try {
      let cursor: string | undefined
      do {
        const result = await inventoryApi.sources({ limit: 200, cursor }, sourceController.signal)
        loaded.push(...result.items)
        cursor = result.next_cursor || undefined
      } while (cursor)
      if (!disposed) { sources.value = loaded; sourcesLoaded.value = true }
    } catch (cause) {
      if (!disposed) sourcesError.value = errorText(cause)
    } finally {
      if (!disposed) sourcesLoading.value = false
    }
  }

  async function loadDetail() {
    if (!selectedId.value || detailLoading.value) return
    const id = selectedId.value
    const version = ++detailGeneration
    detailController = new AbortController()
    detailLoading.value = true
    detailError.value = ''
    try {
      const result = await inventoryApi.detail(id, detailController.signal)
      if (version === detailGeneration && id === selectedId.value) selected.value = result
    } catch (cause) {
      if (version === detailGeneration && !disposed) detailError.value = errorText(cause)
    } finally {
      if (version === detailGeneration) detailLoading.value = false
    }
  }
  function openDetail(id: string) {
    detailController?.abort()
    detailGeneration++
    detailLoading.value = false
    selected.value = null
    selectedId.value = id
    void loadDetail()
  }
  function closeDetail() {
    detailController?.abort()
    detailGeneration++
    selectedId.value = ''
    selected.value = null
    detailLoading.value = false
    detailError.value = ''
  }
  async function cancel(round: Round) {
    if (cancelLoading.value || !isActive(round.status) || round.cancel_requested_at) return
    try {
      await ElMessageBox.confirm('取消会阻止后续阶段，但不会回滚已经写入的图数据。执行中的任务需等待 Worker 响应。确定取消？', '取消同步任务', {
        type: 'warning', confirmButtonText: '确认取消', cancelButtonText: '继续执行',
      })
    } catch { return }
    cancelLoading.value = true
    try {
      const result = await inventoryApi.cancel(round.id)
      if (disposed) return
      ElMessage.success(result.status === 'canceled' ? '任务已取消' : '取消请求已提交，等待 Worker 响应')
      if (selectedId.value === round.id) {
        detailController?.abort()
        detailGeneration++
        detailLoading.value = false
        await loadDetail()
      }
      await page.load(filters.value)
    } catch (cause) {
      if (!disposed) ElMessage.error(errorText(cause))
    } finally { cancelLoading.value = false }
  }
  function created(round: Round) {
    void page.load(filters.value, true)
    openDetail(round.id)
  }
  function showDiagnostics(id: string, isRound: boolean, sourceId: string) {
    diagnostic.value = { id, isRound, sourceId }
  }
  const listPoller = createPoller(async () => {
    if (section.value === 'rounds' && !page.loading.value && !cancelLoading.value && !document.hidden) await page.load(filters.value)
  }, 10000)
  const detailPoller = createPoller(async () => {
    if (selectedId.value && selected.value && isActive(selected.value.status) && !document.hidden) await loadDetail()
  }, 10000)
  watch(filters, () => { void page.load(filters.value, true) })
  watch(section, (value) => {
    if (value === 'rounds') { listPoller.start(); void page.load(filters.value) }
    else listPoller.stop()
  })
  watch([selectedId, () => selected.value?.status], () => {
    if (selectedId.value && selected.value && isActive(selected.value.status)) detailPoller.start()
    else detailPoller.stop()
  })
  onMounted(() => { void loadSources(); void page.load(filters.value); listPoller.start() })
  onScopeDispose(() => {
    disposed = true
    sourceController?.abort()
    detailController?.abort()
    detailGeneration++
    listPoller.stop()
    detailPoller.stop()
  })
  const sourceName = (id: string) => sources.value.find((source) => source.id === id)?.name || id
  return {
    ...page, section, sources, sourcesLoading, sourcesLoaded, sourcesError, sourceFilter, statusFilter,
    pageSize, filters, selectedId, selected, detailLoading, detailError, cancelLoading, createOpen, diagnostic,
    loadSources, loadDetail, openDetail, closeDetail, cancel, created, showDiagnostics, sourceName,
  }
}
