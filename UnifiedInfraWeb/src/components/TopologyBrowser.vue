<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import type { Source } from '../api/types'
import type { TopologyNode, TopologySelection } from '../api/topologyTypes'
import { useTopology } from '../composables/useTopology'
import { useTopologyDirectory } from '../composables/useTopologyDirectory'
import { edgeColors, edgeLabels, entityLabels, validIdentity, topologyDisplayName } from '../services/topology'
import TopologyCanvas from './TopologyCanvas.vue'
import TopologyDetails from './TopologyDetails.vue'
import DeviceTopologyView from './DeviceTopologyView.vue'

const props = defineProps<{ sources: Source[]; sourcesLoading: boolean; sourcesLoaded: boolean; sourcesError: string }>()
const {
  sourceId, stableId, direction, edgeType, graph, selection, selectedNode, selectedEdge,
  rootId, loading, error, notice, warnings, contextNotice, loadDeviceContext, expand, reset,
} = useTopology()
const enabledSources = computed(() => props.sources.filter(source => source.enabled && source.adapter_kind === 'cmdb'))
const sourceStatus = computed(() => {
  if (props.sourcesError) return `来源加载失败：${props.sourcesError}`
  if (props.sourcesLoading || !props.sourcesLoaded) return '正在读取 CMDB 来源配置…'
  if (!enabledSources.value.length) return '当前没有已启用的 CMDB 来源，请由后台配置唯一来源。'
  if (enabledSources.value.length !== 1) return '当前存在多个已启用的 CMDB 来源，请由后台配置唯一来源；不会任意选择来源。'
  return ''
})
const resolvedSourceId = computed(() => sourceStatus.value ? '' : enabledSources.value[0]!.id)
const sourceEnabled = computed(() => !!resolvedSourceId.value && sourceId.value === resolvedSourceId.value)
const canLocate = computed(() => sourceEnabled.value && validIdentity('device', stableId.value))
const directory = useTopologyDirectory(resolvedSourceId)
const { tab, items, path, itemType, pageIndex, nextAfter, loading: directoryLoading, error: directoryError } = directory
const showGraph = ref(false)
const viewMode = ref<'device' | 'graph'>('device')
const compact = ref(false)
const detailsOpen = ref(false)
let media: MediaQueryList | undefined
const resize = () => { compact.value = media?.matches || false }
onMounted(() => { media = window.matchMedia('(max-width: 1200px)'); resize(); media.addEventListener('change', resize) })
onBeforeUnmount(() => media?.removeEventListener('change', resize))
watch(resolvedSourceId, id => { sourceId.value = id; backToDirectory() }, { immediate: true, flush: 'sync' })
watch(tab, backToDirectory, { flush: 'sync' })
watch(stableId, () => { if (showGraph.value) backToDirectory() }, { flush: 'sync' })
watch(selection, value => { if (!value) detailsOpen.value = false }, { flush: 'sync' })
function select(value: TopologySelection) { selection.value = value; detailsOpen.value = true }
function sourceName(id: string) { return props.sources.find(source => source.id === id)?.name || id }
function displayName(item: TopologyNode) { return topologyDisplayName(item) }
function warningText(code: string) {
  const messages: Record<string, string> = {
    missing_cabinet: '设备尚无有效机柜关系，无法继续追踪供电路径',
    missing_rpp: '机柜尚无有效列头柜供电关系，供电路径不完整',
    missing_ups_group: '列头柜尚无有效UPS组关系，供电路径不完整',
    missing_transformer: 'UPS组尚无有效变压器上游关系，供电路径不完整',
    missing_ups: 'UPS组尚无已同步的UPS成员关系',
    query_budget_exhausted: '已达到设备视图查询预算，请手动按需继续展开',
    node_budget_exhausted: '已达到设备视图节点预算，当前展示不是完整依赖',
    edge_budget_exhausted: '已达到设备视图关系预算，当前展示不是完整依赖',
    step_truncated: '部分路径读取已截断，请手动缩小范围后继续展开',
  }
  return messages[code] || code
}
function backToDirectory() { showGraph.value = false; reset() }
function breadcrumb(depth: number) { backToDirectory(); directory.back(depth) }
async function openItem(item: TopologyNode) {
  if (!sourceEnabled.value || directoryLoading.value || item.entity_type !== itemType.value || item.source_id !== sourceId.value) return
  if (item.entity_type === 'device') {
    stableId.value = item.stable_id
    await submit()
  } else {
    backToDirectory()
    await directory.open(item)
  }
}
async function submit() {
  if (!canLocate.value) return
  showGraph.value = true
  await loadDeviceContext()
}
async function reloadDirectory() { backToDirectory(); await directory.reload() }
</script>

<template>
  <section class="topology-browser" aria-label="业务关联拓扑">
    <section class="panel topology-directory">
      <div class="panel-title">
        <div><h2>业务关联拓扑</h2><span class="muted">从数据中心或 POD 选择设备，查看真实供电、GPU 与网络关联</span></div>
        <el-tag effect="plain">只读浏览</el-tag>
      </div>
      <el-alert v-if="sourceStatus" :title="sourceStatus" :type="sourcesError ? 'error' : 'info'" :closable="false" class="spaced" />
      <div class="topology-directory-actions">
        <div class="topology-tabs" role="tablist" aria-label="业务目录入口">
          <button role="tab" :aria-selected="tab === 'data_center'" :class="{ active: tab === 'data_center' }" @click="tab = 'data_center'">数据中心</button>
          <button role="tab" :aria-selected="tab === 'pod'" :class="{ active: tab === 'pod' }" @click="tab = 'pod'">POD</button>
        </div>
        <form class="topology-shortcut" aria-label="设备 SN 快捷定位" @submit.prevent="submit">
          <el-input v-model="stableId" placeholder="输入设备 SN 快捷定位" aria-label="设备 SN" clearable />
          <el-button native-type="submit" type="primary" :loading="loading" :disabled="!canLocate">查看设备关联</el-button>
        </form>
      </div>
      <div class="topology-directory-heading">
        <nav class="topology-breadcrumb" aria-label="业务目录路径">
          <button @click="breadcrumb(0)">{{ entityLabels[tab] }}</button>
          <template v-for="(parent, index) in path" :key="parent.id"><span aria-hidden="true">/</span><button @click="breadcrumb(index + 1)">{{ displayName(parent) }}</button></template>
          <template v-if="showGraph"><span aria-hidden="true">/</span><span>{{ graph.nodes.find(node => node.id === rootId)?.label || stableId }}</span></template>
        </nav>
        <el-button v-if="!showGraph" :disabled="!sourceEnabled" :loading="directoryLoading" @click="reloadDirectory">刷新当前页</el-button>
        <el-button v-else @click="backToDirectory">返回目录</el-button>
      </div>
      <template v-if="!showGraph">
        <p class="muted topology-directory-description">{{ path.length ? itemType === 'cabinet' ? '选择机柜，继续查看柜内设备' : tab === 'pod' ? 'POD 直接成员设备，不通过机柜标签推导' : '选择柜内设备，查看业务关联' : `选择${entityLabels[tab]}，逐层浏览设备` }} · 每页最多 50 项</p>
        <el-alert v-if="directoryError" :title="directoryError" type="error" :closable="false" show-icon class="spaced" />
        <div v-loading="directoryLoading" class="topology-directory-list" aria-label="业务目录列表">
          <button v-for="item in items" :key="item.id" class="topology-directory-card" :disabled="directoryLoading || !sourceEnabled" @click="openItem(item)">
            <span class="topology-card-type">{{ entityLabels[item.entity_type] }}</span>
            <strong>{{ displayName(item) }}</strong>
            <span class="muted">{{ item.entity_type === 'device' ? 'SN' : 'UUID' }} · {{ item.stable_id }}</span>
            <span class="topology-card-action">{{ item.entity_type === 'device' ? '查看关联拓扑' : '查看下级' }} →</span>
          </button>
          <el-empty v-if="!items.length" :description="directoryLoading ? '正在读取目录' : directoryError ? '目录读取失败，请刷新重试' : sourceStatus ? '等待唯一 CMDB 来源配置' : '当前目录没有已同步条目'" :image-size="90" />
        </div>
        <div class="topology-pagination" aria-label="业务目录分页">
          <el-button :disabled="directoryLoading || !sourceEnabled || pageIndex === 0" @click="directory.previous">上一页</el-button>
          <span class="muted">第 {{ pageIndex + 1 }} 页 · 当前 {{ items.length }} 项</span>
          <el-button :disabled="directoryLoading || !sourceEnabled || !nextAfter" @click="directory.next">下一页</el-button>
        </div>
      </template>
    </section>
    <template v-if="showGraph">
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="spaced" />
      <el-alert v-if="contextNotice" :title="contextNotice" type="warning" :closable="false" show-icon class="spaced" />
      <el-alert v-for="(warning, index) in warnings" :key="index" :title="warningText(warning)" type="warning" :closable="false" show-icon class="spaced" />
      <el-alert v-if="notice && notice !== contextNotice" :title="notice" :type="notice.includes('截断') ? 'warning' : 'info'" :closable="false" show-icon class="spaced" />
      <section class="panel topology-workspace">
        <div class="topology-toolbar">
          <div><h2>设备业务关联拓扑</h2><span class="muted">{{ graph.nodes.length }} / 500 节点 · {{ graph.edges.length }} / 1000 关系</span></div>
          <div class="topology-tabs" role="tablist" aria-label="拓扑展示方式">
            <button role="tab" :aria-selected="viewMode === 'device'" :class="{ active: viewMode === 'device' }" @click="viewMode = 'device'">设备视图</button>
            <button role="tab" :aria-selected="viewMode === 'graph'" :class="{ active: viewMode === 'graph' }" @click="viewMode = 'graph'">关系图</button>
          </div>
          <el-select v-model="edgeType" placeholder="全部关系领域" aria-label="展开关系领域"><el-option label="全部关系领域" value="" /><el-option v-for="(label, type) in edgeLabels" :key="type" :label="label" :value="type" /></el-select>
          <el-select v-model="direction" aria-label="展开方向"><el-option label="双向邻居" value="both" /><el-option label="入向邻居" value="in" /><el-option label="出向邻居" value="out" /></el-select>
          <el-button type="primary" plain :disabled="!selectedNode || !sourceEnabled" :loading="loading" @click="expand">展开选中节点一跳</el-button>
          <el-button :disabled="!canLocate" :loading="loading" @click="submit">重新读取设备关联</el-button>
          <el-button v-if="(compact || viewMode === 'device') && selection" @click="detailsOpen = true">查看详情</el-button>
        </div>
        <p class="muted topology-selection">{{ selectedNode ? `选中：${selectedNode.label || selectedNode.stable_id}` : selectedEdge ? '已选中关系；请选择节点后展开' : '点击节点选择，点击关系查看属性' }} · 筛选仅作用于下一次手动读取，不删除已展示关系。</p>
        <p class="muted">原始真实图，不生成供电直连。缺失关系与截断会明确提示，不能据此认定设备没有依赖。</p>
        <div class="topology-layout" :style="viewMode === 'device' ? { gridTemplateColumns: 'minmax(0, 1fr)' } : undefined">
          <div v-loading="loading" class="topology-graph-area">
            <DeviceTopologyView v-if="graph.nodes.length && viewMode === 'device'" :graph="graph" :selection="selection" :root-id="rootId" @select="select" />
            <TopologyCanvas v-else-if="graph.nodes.length" :graph="graph" :selection="selection" :root-id="rootId" @select="select" />
            <el-empty v-else :description="loading ? '正在读取设备关联路径' : '未展示设备，请核对 SN 或服务状态后重试'" :image-size="100" />
          </div>
          <aside v-if="!compact && viewMode === 'graph'" class="topology-detail-sidebar"><TopologyDetails :node="selectedNode" :edge="selectedEdge" :source-name="sourceName" /></aside>
        </div>
        <div class="topology-legend" aria-label="关系图例"><span v-for="(label, type) in edgeLabels" :key="type"><i :style="{ background: edgeColors[type] }" />{{ label }}</span><span>箭头保留存储方向 · A/B 路径及 UPS 成员旁支保留 · 可拖拽 / 滚轮缩放</span></div>
      </section>
    </template>
    <el-drawer v-if="(compact || viewMode === 'device') && showGraph" v-model="detailsOpen" title="拓扑对象详情" size="min(420px, 100%)" destroy-on-close><TopologyDetails :node="selectedNode" :edge="selectedEdge" :source-name="sourceName" /></el-drawer>
    <p class="page-footnote">目录按需分页，不读取全图库；设备自动读取有界供电、GPU 与网络路径，其他节点仅手动展开一跳（每次最多 100 条关系）。同步标记可能包含旧数据，不代表原子全局快照；无故障模拟或写操作。</p>
  </section>
</template>
