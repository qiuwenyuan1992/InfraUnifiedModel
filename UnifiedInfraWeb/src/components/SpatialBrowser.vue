<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import type { Source } from '../api/types'
import { topologyApi } from '../api/topology'
import type { TopologyNode, TopologyRootType } from '../api/topologyTypes'
import { useSpatialScene } from '../composables/useSpatialScene'
import { buildSpatialScene, isSpatialNetworkDevice } from '../services/spatialScene'
import { entityLabels, topologyDisplayName, topologyError } from '../services/topology'
import { formatTime } from '../services/display'
import SpatialThreeCanvas from './SpatialThreeCanvas.vue'

const props = defineProps<{ sources: Source[]; sourcesLoading: boolean; sourcesLoaded: boolean; sourcesError: string }>()
const scene = useSpatialScene()
const { rootItems, rootsNextAfter, sceneNextAfter, sceneHistory, cabinetNextAfter, selectedRoot, loading, error, data, mode,
  loadRoots, selectRoot, nextScenePage, previousScenePage, expandNetwork } = scene
const rootType = ref<TopologyRootType>('data_center')
const selection = ref<string | null>(null)
const history = ref<(string | undefined)[]>([])
const currentAfter = ref<string | undefined>()
const enabled = computed(() => props.sources.filter(source => source.enabled && source.adapter_kind === 'cmdb'))
const sourceId = computed(() => enabled.value.length === 1 ? enabled.value[0]!.id : '')
const available = computed(() => props.sourcesLoaded && !props.sourcesLoading && !props.sourcesError && !!sourceId.value)
const issue = computed(() => props.sourcesError ? '' : !props.sourcesLoaded || props.sourcesLoading ? '正在读取CMDB来源' : enabled.value.length === 0 ? '没有启用的CMDB来源' : enabled.value.length > 1 ? '存在多个启用来源，请先配置唯一CMDB来源' : '')
const layout = computed(() => buildSpatialScene(data.value, mode.value))
const selectedNode = computed(() => data.value.nodes.find(node => node.id === selection.value) || null)
const detailNode = shallowRef<TopologyNode | null>(null)
const detailsLoading = ref(false)
const detailsError = ref('')
const detailsCache = new Map<string, TopologyNode>()
let detailsController: AbortController | undefined
let detailsGeneration = 0
watch([sourceId, available], () => { detailsCache.clear() }, { flush: 'sync' })
watch(selectedNode, async node => {
  const version = ++detailsGeneration
  detailsController?.abort()
  detailNode.value = null
  detailsError.value = ''
  detailsLoading.value = false
  if (!node) return
  const identity = { source_id: node.source_id, entity_type: node.entity_type, stable_id: node.stable_id }
  const key = JSON.stringify(identity)
  const cached = detailsCache.get(key)
  if (cached) { detailNode.value = cached; return }
  const owner = new AbortController()
  detailsController = owner
  detailsLoading.value = true
  try {
    const full = await topologyApi.node(identity, owner.signal)
    if (version !== detailsGeneration || owner.signal.aborted) return
    if (full.source_id !== node.source_id || full.entity_type !== node.entity_type || full.stable_id !== node.stable_id
      || full.id !== node.id) throw new Error('资产详情与当前选择不匹配。')
    detailsCache.set(key, full)
    detailNode.value = full
  } catch (cause) {
    if (version === detailsGeneration && !owner.signal.aborted) detailsError.value = topologyError(cause)
  } finally {
    if (version === detailsGeneration && !owner.signal.aborted) detailsLoading.value = false
  }
}, { flush: 'sync' })
const networkNode = (node: TopologyNode) => isSpatialNetworkDevice(node, data.value.networkLevels)
const networkCount = computed(() => data.value.nodes.filter(networkNode).length)
const cabinetCount = computed(() => data.value.nodes.filter(node => node.entity_type === 'cabinet').length)
const deviceCount = computed(() => data.value.nodes.filter(node => node.entity_type === 'device' && !networkNode(node)).length)
watch([sourceId, available, rootType], () => {
  history.value = []; currentAfter.value = undefined; selection.value = null
  if (available.value) void loadRoots(sourceId.value, rootType.value)
  else scene.reset()
}, { immediate: true, flush: 'sync' })
watch(selectedRoot, () => { selection.value = null })
async function next() {
  if (!rootsNextAfter.value || loading.value) return
  history.value.push(currentAfter.value); currentAfter.value = rootsNextAfter.value
  await loadRoots(sourceId.value, rootType.value, currentAfter.value)
}
async function previous() {
  if (!history.value.length || loading.value) return
  currentAfter.value = history.value.pop(); await loadRoots(sourceId.value, rootType.value, currentAfter.value)
}
async function reload() {
  if (selectedRoot.value) await selectRoot(selectedRoot.value)
  else if (available.value) await loadRoots(sourceId.value, rootType.value, currentAfter.value)
}
function sceneWarning(code: string) {
  return ({directory_truncated: '当前目录还有下一页未加载', step_truncated: '部分关系读取已截断，未核验的归属不会被当成缺失', query_budget_exhausted: '达到本页图库读取预算，请缩小范围或按需展开', node_budget_exhausted: '达到本页节点预算', edge_budget_exhausted: '达到本页关系预算', parent_outside_scope: '部分归属在当前分页范围以外'} as Record<string, string>)[code] || code
}
function property(value: unknown) { return value === null || value === undefined ? '—' : typeof value === 'object' ? JSON.stringify(value) : String(value) }
onScopeDispose(() => { detailsGeneration++; detailsController?.abort(); detailsCache.clear(); selection.value = null })
</script>

<template>
  <section class="spatial-browser">
    <div class="panel spatial-entry">
      <div class="panel-title"><div><h2>空间三维 · 试用版</h2><p class="muted">示意布局，非真实机房坐标 · 只显示已读取资产与关系</p></div><el-button :loading="loading" :disabled="!available" @click="reload">重新读取</el-button></div>
      <el-alert v-if="issue" :title="issue" type="info" :closable="false" show-icon />
      <div class="spatial-options">
        <div class="topology-tabs" role="tablist" aria-label="三维目录">
          <button role="tab" :aria-selected="rootType === 'data_center'" :class="{ active: rootType === 'data_center' }" @click="rootType = 'data_center'">数据中心</button>
          <button role="tab" :aria-selected="rootType === 'pod'" :class="{ active: rootType === 'pod' }" @click="rootType = 'pod'">POD</button>
        </div>
        <div class="topology-tabs" role="tablist" aria-label="三维场景模式">
          <button role="tab" :aria-selected="mode === 'space'" :class="{ active: mode === 'space' }" @click="mode = 'space'">机柜空间</button>
          <button role="tab" :aria-selected="mode === 'network'" :class="{ active: mode === 'network' }" @click="mode = 'network'">网络分层</button>
        </div>
      </div>
      <div class="spatial-roots" v-loading="loading && !selectedRoot">
        <button v-for="node in rootItems" :key="node.id" class="spatial-root" :class="{ active: selectedRoot?.id === node.id }" :disabled="loading" @click="selectRoot(node)">
          <strong>{{ topologyDisplayName(node) }}</strong><small>{{ node.entity_type === 'pod' ? 'POD成员场景' : '数据中心机柜场景' }}</small>
        </button>
      </div>
      <div v-if="history.length || rootsNextAfter" class="spatial-page"><el-button :disabled="!history.length || loading" @click="previous">上一页</el-button><span>目录第 {{ history.length + 1 }} 页</span><el-button :disabled="!rootsNextAfter || loading" @click="next">下一页</el-button></div>
      <p v-if="available && !loading && !rootItems.length && !error" class="muted">当前目录暂无同步数据</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
    </div>
    <div v-if="selectedRoot && (sceneHistory.length || sceneNextAfter)" class="panel spatial-page" aria-label="场景分页">
      <el-button :disabled="!sceneHistory.length || loading" @click="previousScenePage">上一场景页</el-button>
      <span>场景第 {{ sceneHistory.length + 1 }} 页 · 每页最多5个机柜</span>
      <el-button :disabled="!sceneNextAfter || loading" @click="nextScenePage">下一场景页</el-button>
    </div>
    <div v-if="selectedRoot" class="spatial-stats">
      <article><span>当前场景</span><strong>{{ topologyDisplayName(selectedRoot) }}</strong></article>
      <article><span>已加载机柜</span><strong>{{ cabinetCount }}</strong></article>
      <article><span>已加载业务设备</span><strong>{{ deviceCount }}</strong></article>
      <article><span>已加载网络设备</span><strong>{{ networkCount }}</strong></article>
    </div>
    <div class="spatial-workspace" v-loading="loading">
      <SpatialThreeCanvas :layout="layout" :selection="selection" :mode="mode" @select="selection = $event" />
      <aside class="spatial-inspector">
        <label class="spatial-node-picker">定位已加载对象
          <select v-model="selection" aria-label="定位三维对象"><option :value="null">在画布中点选</option><option v-for="item in layout.placements" :key="item.node.id" :value="item.node.id">{{ networkNode(item.node) ? '网络设备' : entityLabels[item.node.entity_type] }} · {{ topologyDisplayName(item.node) }} · {{ item.node.stable_id }}</option></select>
        </label>
        <template v-if="selectedNode">
          <span class="spatial-kicker">真实资产</span><h3>{{ topologyDisplayName(selectedNode) }}</h3>
          <dl><dt>类型</dt><dd>{{ networkNode(selectedNode) ? '网络设备' : entityLabels[selectedNode.entity_type] }}</dd><dt>稳定ID</dt><dd class="mono">{{ selectedNode.stable_id }}</dd><dt>role</dt><dd>{{ property(selectedNode.properties.role) }}</dd><dt>同步时间</dt><dd>{{ formatTime(selectedNode.synced_at) }}</dd></dl>
          <el-button v-if="networkNode(selectedNode)" type="primary" :disabled="loading" @click="expandNetwork(selectedNode)">展开真实网络连接</el-button>
          <p v-if="cabinetNextAfter[selectedNode.id]">该机柜还有设备未加载</p>
          <p v-if="data.assignmentStatus?.[selectedNode.id]">归属状态：{{ ({missing: '未发现有效归属', ambiguous: '多个归属待核验', truncated: '归属尚未完整读取', unknown: '手动展开层级未确认，请重载场景'} as Record<string, string>)[data.assignmentStatus[selectedNode.id]!] }}</p>
          <p v-if="detailsLoading" class="muted">正在读取完整资产属性</p>
          <el-alert v-if="detailsError" :title="detailsError" type="error" :closable="false" />
          <details v-if="detailNode"><summary>原始属性</summary><dl><template v-for="(value, key) in detailNode.properties" :key="key"><dt>{{ key }}</dt><dd>{{ property(value) }}</dd></template></dl></details>
        </template>
        <template v-else><span class="spatial-kicker">场景说明</span><h3>点选机柜或设备</h3><p>拖拽旋转，滚轮缩放，右键平移；点击对象查看真实属性。</p><p v-if="mode === 'network'">按后端场景层级布局，保留原始 role 标签；未分类设备单列，不根据名称或关系推断层级。</p><p v-else>机柜位置为示意行列，柜内仅展示已校验归属的设备，不表示真实U位。</p></template>
        <p class="spatial-notice">所有数量仅为本页已加载，不是全IDC/POD统计。POD不是机柜包含层级；远端网络设备的空间归属未自动推导。</p>
      </aside>
    </div>
    <details v-if="data.warnings.length || layout.warnings.length" class="panel spatial-warnings" open><summary>{{ data.truncated ? '局部场景：存在分页、预算或未核验数据' : '场景提示' }}</summary><p v-for="warning in [...new Set([...data.warnings, ...layout.warnings])]" :key="warning">{{ sceneWarning(warning) }}</p></details>
  </section>
</template>

<style scoped>
.spatial-browser { display: grid; gap: 16px; }
.spatial-node-picker { display: grid; gap: 8px; margin-bottom: 20px; font-size: 12px; color: #8ba9cd; }.spatial-node-picker select { max-width: 100%; padding: 8px; background: #142c49; border: 1px solid #355475; border-radius: 5px; color: #dcecff; }
.spatial-entry p { margin: 6px 0 0; }
.spatial-options { display: flex; gap: 20px; flex-wrap: wrap; margin: 14px 0; }
.spatial-roots { display: flex; gap: 10px; flex-wrap: wrap; min-height: 48px; }
.spatial-root { display: grid; gap: 6px; text-align: left; background: #f7faff; border: 1px solid #d7e2f2; border-radius: 8px; padding: 12px 18px; cursor: pointer; color: #264362; }
.spatial-root.active { background: #eaf3ff; border-color: #3479df; }.spatial-root small { color: #72859e; }
.spatial-page { display: flex; gap: 14px; align-items: center; margin-top: 12px; font-size: 12px; color: #72859e; }
.spatial-stats { display: grid; grid-template-columns: 2fr repeat(3, 1fr); gap: 12px; }.spatial-stats article { background: white; border: 1px solid #dfe7f3; border-radius: 10px; padding: 14px 18px; display: grid; gap: 7px; }.spatial-stats span { color: #72859e; font-size: 12px; }.spatial-stats strong { font-size: 19px; color: #213e61; }
.spatial-workspace { display: grid; grid-template-columns: minmax(0, 1fr) 280px; min-width: 0; border: 1px solid #dfe7f3; border-radius: 12px; overflow: hidden; background: #091326; }.spatial-inspector { padding: 22px 18px; color: #bed0ea; background: #112039; min-width: 0; max-height: 720px; overflow: auto; }.spatial-kicker { font-size: 11px; color: #6db4ff; letter-spacing: 2px; }.spatial-inspector h3 { color: #e9f2ff; margin: 12px 0 18px; overflow-wrap: anywhere; }.spatial-inspector p { font-size: 12px; line-height: 1.8; }.spatial-inspector dl { font-size: 12px; }.spatial-inspector dt { color: #7895b9; margin-top: 12px; }.spatial-inspector dd { margin: 5px 0; overflow-wrap: anywhere; }.spatial-inspector details { margin-top: 20px; font-size: 12px; }.spatial-notice { border-top: 1px solid #254064; margin-top: 22px; padding-top: 14px; color: #849fbe; }.spatial-warnings { padding: 14px 20px; color: #8a641e; font-size: 12px; overflow-wrap: anywhere; }.spatial-warnings p { margin: 7px 0; }.spatial-warnings summary { cursor: pointer; font-weight: 600; }
@media (max-width: 1000px) { .spatial-workspace { grid-template-columns: 1fr; }.spatial-stats { grid-template-columns: repeat(2, 1fr); } }
</style>
