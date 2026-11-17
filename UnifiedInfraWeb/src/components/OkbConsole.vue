<template>
  <div class="okb-console">
    <el-alert v-if="connectionError" type="error" :closable="false" show-icon
      title="OKB 服务不可达"
      :description="`${connectionError}——请确认 OKB 后端已启动（默认 :18090）且 Vite 代理已配置 /api → OKB。`" />
    <el-tabs v-model="tab">
      <!-- 知识清单：分类折叠列表 + 行内管理 -->
      <el-tab-pane label="知识清单" name="list">
        <div class="query-bar">
          <el-input v-model="listFilter.q" placeholder="搜索编码 / 名称 / signature" clearable style="width: 260px"
            @keyup.enter="loadAllLists" />
          <el-select v-model="listFilter.status" placeholder="全部状态" clearable style="width: 130px">
            <el-option label="curated" value="curated" />
            <el-option label="draft" value="draft" />
          </el-select>
          <el-button type="primary" :loading="loading.list" @click="loadAllLists">刷新清单</el-button>
          <span class="dim">展开各分区查看与管理</span>
        </div>
        <el-collapse v-model="listOpen">
          <el-collapse-item name="anomaly">
            <template #title><span class="section-title">异常类型（节点）</span><span class="dim count">{{ listAnomaly?.total ?? '—' }} 条</span></template>
            <el-table :data="listAnomaly?.rows ?? []" size="small" border>
              <el-table-column prop="stable_code" label="异常编码" min-width="200" show-overflow-tooltip />
              <el-table-column prop="name" label="名称" min-width="140" />
              <el-table-column label="状态" width="90">
                <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
              </el-table-column>
              <el-table-column prop="updated_at" label="更新时间" min-width="160">
                <template #default="{ row }">{{ new Date(row.updated_at).toLocaleString() }}</template>
              </el-table-column>
              <el-table-column label="操作" width="150" fixed="right">
                <template #default="{ row }">
                  <el-button link type="primary" size="small" @click="traceCode = row.stable_code; tab = 'query'; doTrace()">trace</el-button>
                  <el-button link size="small" @click="doEvidence(row.stable_code)">证据</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无异常类型" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
          <el-collapse-item name="rule">
            <template #title><span class="section-title">因果规则（边）</span><span class="dim count">{{ listRule?.total ?? '—' }} 条</span></template>
            <el-table :data="listRule?.rows ?? []" size="small" border>
              <el-table-column label="因果（源 → 目标）" min-width="280">
                <template #default="{ row }"><span class="mono">{{ row.source_code }}</span> → <span class="mono">{{ row.target_code }}</span></template>
              </el-table-column>
              <el-table-column label="证据等级" width="120">
                <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ row.evidence_level }}</el-tag></template>
              </el-table-column>
              <el-table-column label="置信度" width="85">
                <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="印证" width="70">
                <template #default="{ row }">{{ row.case_support_count }} 工单</template>
              </el-table-column>
              <el-table-column label="状态" width="90">
                <template #default="{ row }">
                  <el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag>
                  <el-tag v-if="row.review_flagged" type="danger" size="small">待复核</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="200" fixed="right">
                <template #default="{ row }">
                  <el-button link type="primary" size="small" @click="doEvidence(row.id)">证据</el-button>
                  <el-button link type="success" size="small" @click="doConfirmEdge(row.id)">确认</el-button>
                  <el-button link type="danger" size="small" @click="doRejectEdge(row.id)">否决</el-button>
                  <el-button link size="small" @click="report.edgeId = row.id; tab = 'report'">反馈</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无因果规则" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
          <el-collapse-item name="mapping">
            <template #title><span class="section-title">告警映射（入口）</span><span class="dim count">{{ listMapping?.total ?? '—' }} 条</span></template>
            <el-table :data="listMapping?.rows ?? []" size="small" border>
              <el-table-column prop="alert_signature" label="告警 signature" min-width="200" show-overflow-tooltip />
              <el-table-column label="候选异常" min-width="180">
                <template #default="{ row }"><span class="mono">{{ row.stable_code }}</span>（{{ row.name }}）</template>
              </el-table-column>
              <el-table-column label="置信度" width="85">
                <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="证据等级" width="120">
                <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ row.evidence_level }}</el-tag></template>
              </el-table-column>
              <el-table-column label="状态" width="90">
                <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
              </el-table-column>
              <el-table-column label="操作" width="150" fixed="right">
                <template #default="{ row }">
                  <el-button link type="primary" size="small" @click="alertSignature = row.alert_signature; doLookup()">lookup</el-button>
                  <el-button link size="small" @click="doEvidence(row.id)">证据</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无告警映射" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
          <el-collapse-item name="review">
            <template #title><span class="section-title">复核队列（被否决待处理）</span><span class="dim count">{{ reviewEdges?.length ?? '—' }} 条</span></template>
            <el-table :data="reviewEdges ?? []" size="small" border>
              <el-table-column prop="ID" label="边 ID" min-width="260" show-overflow-tooltip />
              <el-table-column label="置信度" width="90">
                <template #default="{ row }">{{ row.Confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="状态" width="90">
                <template #default="{ row }"><el-tag type="info" size="small">{{ row.Status }}</el-tag></template>
              </el-table-column>
              <el-table-column label="操作" width="180" fixed="right">
                <template #default="{ row }">
                  <el-button link type="success" size="small" @click="doConfirmEdge(row.ID)">确认恢复</el-button>
                  <el-button link type="danger" size="small" @click="doRejectEdge(row.ID)">维持否决</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="复核队列为空" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
        </el-collapse>
      </el-tab-pane>
      <!-- 知识图谱：力导向可视化 -->
      <el-tab-pane label="知识图谱" name="graph">
        <div class="query-bar">
          <el-button type="primary" :loading="loading.graph" @click="doGraph">加载故障知识图谱</el-button>
          <el-checkbox v-model="graphIncludeDraft">显示草稿边</el-checkbox>
          <span class="dim">节点 = 异常类型；连线 = 因果边（颜色按证据等级，粗细按置信度）</span>
        </div>
        <div ref="graphContainer" class="graph-container" v-loading="loading.graph"
          element-loading-text="正在从 OKB 加载图谱…" />
        <div v-if="graphLegend.length" class="graph-legend">
          <span v-for="item in graphLegend" :key="item.label" class="legend-item">
            <i :style="{ background: item.color }" />{{ item.label }}
          </span>
        </div>
        <el-empty v-if="graphEmpty" description="图中没有节点，请先在资料摄取页入库工单" :image-size="80" />
        <el-dialog v-model="nodeDialogVisible" :title="nodeDialogNode?.stable_code" width="480px">
          <p v-if="nodeDialogNode">{{ nodeDialogNode.name }}（{{ nodeDialogNode.status }}）</p>
          <div class="query-bar">
            <el-button type="primary" size="small" @click="traceCode = nodeDialogNode!.stable_code; tab = 'query'; doTrace()">从该异常 trace</el-button>
            <el-button size="small" @click="doEvidence(nodeDialogNode!.stable_code)">查看证据</el-button>
          </div>
        </el-dialog>
      </el-tab-pane>
      <!-- 图谱查询：lookup + trace + evidence -->
      <el-tab-pane label="图谱查询" name="query">
        <div class="query-bar">
          <el-input v-model="alertSignature" placeholder="告警 signature，如 ALERT_GPU_ECC" clearable @keyup.enter="doLookup">
            <template #prepend>lookup</template>
          </el-input>
          <el-button type="primary" :loading="loading.lookup" @click="doLookup">按告警查候选异常</el-button>
        </div>
        <el-table v-if="lookup" :data="lookup.candidates" size="small" border>
          <el-table-column prop="stable_code" label="异常编码" min-width="180" />
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column label="置信度" width="90">
            <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
          </el-table-column>
          <el-table-column label="证据等级" width="130">
            <template #default="{ row }">
              <el-tag :type="levelTag(row.evidence_level)" size="small">{{ row.evidence_level }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="160">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="traceCode = row.stable_code; doTrace()">trace</el-button>
              <el-button link size="small" @click="doEvidence(row.stable_code)">证据</el-button>
            </template>
          </el-table-column>
        </el-table>

        <el-divider content-position="left">因果链遍历 trace</el-divider>
        <div class="query-bar">
          <el-input v-model="traceCode" placeholder="异常 stable_code，如 gpu.ecc.error" clearable @keyup.enter="doTrace">
            <template #prepend>trace</template>
          </el-input>
          <el-input-number v-model="traceHops" :min="1" :max="5" />
          <el-checkbox v-model="traceIncludeDraft">含草稿</el-checkbox>
          <el-button type="primary" :loading="loading.trace" @click="doTrace">遍历候选根因链</el-button>
        </div>
        <div v-if="trace">
          <el-empty v-if="trace.chains.length === 0" description="无候选链（可能均为草稿，勾选含草稿后重试）" :image-size="60" />
          <el-card v-for="(chain, i) in trace.chains" :key="i" class="chain-card" shadow="hover">
            <div class="chain-head">
              <el-tag :type="chain.fully_curated ? 'success' : 'warning'" size="small">
                {{ chain.fully_curated ? '逐跳已核' : '含未审核跳' }}
              </el-tag>
              <span class="chain-meta">最小置信度 {{ chain.min_confidence.toFixed(2) }} · {{ chain.hops.length }} 跳</span>
            </div>
            <div class="chain-hops">
              <el-tag v-for="(hop, j) in chain.hops" :key="j" class="hop-tag"
                :type="hop.evidence_level === 'case_verified' ? 'success' : 'warning'">
                {{ hop.target_stable_code }}
                <span class="hop-eval">{{ evalSummary(hop) }}</span>
              </el-tag>
              <el-button link type="primary" size="small" @click="doEvidence(chain.hops[chain.hops.length - 1].edge_id)">边证据</el-button>
            </div>
            <div v-if="chain.combined_note" class="chain-note">{{ chain.combined_note }}</div>
          </el-card>
        </div>

        <el-dialog v-model="evidenceVisible" title="证据回链" width="720px">
          <div v-if="evidence" class="evidence-head">
            <span>实体：{{ evidence.entity_type }} / {{ evidence.entity_id }}</span>
            <el-tag size="small">revision {{ evidence.revision_id?.slice(0, 8) }}</el-tag>
          </div>
          <el-empty v-if="evidence && evidence.items.length === 0" description="无证据" :image-size="60" />
          <div v-for="item in evidence?.items || []" :key="item.evidence_id" class="evidence-item">
            <div class="evidence-meta">
              <el-tag :type="levelTag(item.level)" size="small">{{ item.level }}</el-tag>
              <span v-if="item.source_kind" class="dim">{{ sourceLabel(item.source_kind) }}</span>
              <span class="dim">{{ item.created_by === 'model' ? '模型提取' : '专家' }}</span>
            </div>
            <div class="evidence-summary">{{ item.summary }}</div>
            <pre class="evidence-content">{{ item.content }}</pre>
          </div>
        </el-dialog>
      </el-tab-pane>

      <!-- 摄取：工单/文档入库 -->
      <el-tab-pane label="资料摄取" name="ingest">
        <el-form label-width="90px" class="ingest-form">
          <el-form-item label="数据源">
            <el-select v-model="ingest.kind" style="width: 220px">
              <el-option label="脱敏工单/复盘" value="internal_case" />
              <el-option label="厂商文档" value="vendor_doc" />
              <el-option label="专家经验" value="expert_input" />
            </el-select>
          </el-form-item>
          <el-form-item label="标题">
            <el-input v-model="ingest.title" placeholder="如 工单-2026-001 GPU ECC 复盘" />
          </el-form-item>
          <el-form-item label="正文">
            <el-input v-model="ingest.content" type="textarea" :rows="10"
              placeholder="脱敏后的工单/文档正文。当前开发提取器按 JSON 标记片段识别：&#10;{&quot;anomaly&quot;: &quot;gpu.ecc.error&quot;, &quot;name&quot;: &quot;GPU ECC 错误&quot;, &quot;code&quot;: &quot;gpu.ecc.error&quot;, &quot;alert&quot;: &quot;ALERT_GPU_ECC&quot;}&#10;{&quot;source&quot;: &quot;gpu.ecc.error&quot;, &quot;target&quot;: &quot;gpu.link.degrade&quot;}&#10;含 IP/手机号/凭据等内容会被脱敏门禁拒绝。" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="loading.ingest" @click="doIngest">摄取入库</el-button>
            <span v-if="ingestResult" class="ingest-result">
              新增 {{ ingestResult.new_anomalies }} 异常 / {{ ingestResult.new_rules }} 因果边 /
              {{ ingestResult.new_mappings }} 告警映射<template v-if="ingestResult.curated_rules">，{{ ingestResult.curated_rules }} 边自动升 curated（独立工单印证）</template>
            </span>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <!-- 反馈：report 飞轮 -->
      <el-tab-pane label="诊断反馈" name="report">
        <el-form label-width="110px" class="report-form">
          <el-form-item label="诊断引用">
            <el-input v-model="report.diagnosisRef" placeholder="本次诊断唯一引用，幂等键" />
          </el-form-item>
          <el-form-item label="因果边 ID">
            <el-input v-model="report.edgeId" placeholder="trace 结果中的 edge_id" />
          </el-form-item>
          <el-form-item label="判定">
            <el-radio-group v-model="report.verdict">
              <el-radio value="confirmed">确认（置信度 +0.05）</el-radio>
              <el-radio value="rejected">否决（降级入复核队列）</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="备注">
            <el-input v-model="report.notes" type="textarea" :rows="2" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="loading.report" @click="doReport">上报反馈</el-button>
            <span v-if="reportResult" class="ingest-result">
              {{ reportResult.recorded ? `已记录：置信度 ${reportResult.new_confidence.toFixed(2)}，状态 ${reportResult.new_status}` : '重复上报：幂等忽略' }}
            </span>
          </el-form-item>
        </el-form>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import * as echarts from 'echarts'
import {
  okbApi, type LookupResult, type TraceResult, type EvidenceView,
  type IngestOutput, type ReportResult, type ReviewEdge, type SourceKind, type Verdict, type GraphView,
  type KnowledgeList,
} from '../api/okb'

const tab = ref('list')
const loading = reactive({ lookup: false, trace: false, ingest: false, report: false, review: false, graph: false, list: false })
const connectionError = ref('')

// 知识清单（默认全部分区展开）
const listOpen = ref<string[]>(['anomaly', 'rule', 'mapping', 'review'])
const listFilter = reactive({ q: '', status: '' })
const listAnomaly = ref<KnowledgeList | null>(null)
const listRule = ref<KnowledgeList | null>(null)
const listMapping = ref<KnowledgeList | null>(null)

async function loadAllLists() {
  loading.list = true
  connectionError.value = ''
  const params = { q: listFilter.q || undefined, status: listFilter.status || undefined, limit: 200 }
  try {
    const [a, r, m, q] = await Promise.all([
      okbApi.knowledge('anomaly', params),
      okbApi.knowledge('rule', params),
      okbApi.knowledge('mapping', params),
      okbApi.reviewQueue().catch(() => ({ edges: [] as ReviewEdge[] })),
    ])
    listAnomaly.value = a
    listRule.value = r
    listMapping.value = m
    reviewEdges.value = q.edges
  } catch (err: unknown) {
    const msg = err instanceof Error ? err.message : String(err)
    if (msg.includes('Failed to fetch')) connectionError.value = msg
    ElMessage.error(msg)
  } finally {
    loading.list = false
  }
}
void loadAllLists()

// 图谱
const graphContainer = ref<HTMLDivElement>()
const graphIncludeDraft = ref(false)
const graphEmpty = ref(false)
const graphLegend = ref<Array<{ label: string; color: string }>>([])
const nodeDialogVisible = ref(false)
const nodeDialogNode = ref<GraphView['nodes'][number] | null>(null)
let chart: echarts.ECharts | null = null

const LEVEL_COLORS: Record<string, string> = {
  case_verified: '#67c23a',
  doc_explicit: '#e6a23c',
  inferred: '#909399',
  expert_only: '#b199df',
}

async function doGraph() {
  const view = await wrap('graph', () => okbApi.graph())
  if (!view) return
  graphEmpty.value = view.nodes.length === 0
  if (graphEmpty.value) return
  renderGraph(view)
}

function renderGraph(view: GraphView) {
  if (!graphContainer.value) return
  if (!chart) chart = echarts.init(graphContainer.value)
  const edgeVisible = (e: GraphView['edges'][number]) => graphIncludeDraft.value || e.status === 'curated'
  const edges = view.edges.filter(edgeVisible)
  const usedNodeIds = new Set<string>()
  edges.forEach(e => { usedNodeIds.add(e.source); usedNodeIds.add(e.target) })
  view.nodes.forEach(n => usedNodeIds.add(n.id))
  const presentLevels = new Set(edges.map(e => e.evidence_level))
  graphLegend.value = Array.from(presentLevels).map(level => ({
    label: { case_verified: '案例实证', doc_explicit: '文档明示', inferred: '模型推导', expert_only: '专家经验' }[level] || level,
    color: LEVEL_COLORS[level] || '#909399',
  }))
  chart.setOption({
    tooltip: {
      formatter: (params: { dataType: string; data: Record<string, unknown> & { source?: string; target?: string } }) => {
        if (params.dataType === 'edge') {
          const d = params.data
          return `<b>${d.source} → ${d.target}</b><br/>证据：${String(d.evidence_level ?? '')} · 置信度 ${Number(d.confidence ?? 0).toFixed(2)}<br/>状态：${String(d.status ?? '')} · 印证 ${String(d.case_support_count ?? 0)} 工单`
        }
        const n = params.data
        return `<b>${n.name}</b><br/>${String(n.stable_code ?? '')}<br/>状态：${String(n.status ?? '')}`
      },
    },
    series: [{
      type: 'graph', layout: 'force', roam: true, draggable: true,
      force: { repulsion: 320, edgeLength: [90, 180], gravity: 0.08 },
      label: { show: true, position: 'right', fontSize: 11, formatter: (p: { data: { name: string } }) => p.data.name },
      edgeLabel: { show: false },
      data: view.nodes.map(n => ({
        id: n.stable_code, name: n.name, stable_code: n.stable_code, status: n.status,
        category: n.category, symbolSize: 34,
        itemStyle: { color: n.status === 'curated' ? '#409eff' : '#a0cfff' },
      })),
      edges: edges.map(e => ({
        source: e.source, target: e.target, evidence_level: e.evidence_level,
        confidence: e.confidence, status: e.status, case_support_count: e.case_support_count,
        lineStyle: {
          color: LEVEL_COLORS[e.evidence_level] || '#909399',
          width: 1 + Math.round(e.confidence * 3),
          type: e.status === 'curated' ? 'solid' : 'dashed',
          curveness: 0.15,
        },
      })),
      emphasis: { focus: 'adjacency' },
    }],
  })
  chart.off('click')
  chart.on('click', (params: echarts.ECElementEvent) => {
    const data = (params.data ?? {}) as { stable_code?: string }
    if (params.dataType === 'node' && data.stable_code) {
      nodeDialogNode.value = view.nodes.find(n => n.stable_code === data.stable_code) ?? null
      nodeDialogVisible.value = true
    }
  })
}

onBeforeUnmount(() => { chart?.dispose(); chart = null })

// 查询
const alertSignature = ref('')
const traceCode = ref('')
const traceHops = ref(3)
const traceIncludeDraft = ref(false)
const lookup = ref<LookupResult | null>(null)
const trace = ref<TraceResult | null>(null)

// 证据
const evidenceVisible = ref(false)
const evidence = ref<EvidenceView | null>(null)

// 摄取
const ingest = reactive<{ kind: SourceKind; title: string; content: string }>({
  kind: 'internal_case', title: '', content: '',
})
const ingestResult = ref<IngestOutput | null>(null)

// 反馈
const report = reactive<{ diagnosisRef: string; edgeId: string; verdict: Verdict; notes: string }>({
  diagnosisRef: '', edgeId: '', verdict: 'confirmed', notes: '',
})
const reportResult = ref<ReportResult | null>(null)

// 复核
const reviewEdges = ref<ReviewEdge[] | null>(null)

function wrap<T>(key: keyof typeof loading, fn: () => Promise<T>): Promise<T | null> {
  loading[key] = true
  connectionError.value = ''
  return fn().catch((err: unknown) => {
    const msg = err instanceof Error ? err.message : String(err)
    if (msg.includes('Failed to fetch') || msg.includes('服务')) connectionError.value = msg
    ElMessage.error(msg)
    return null
  }).finally(() => { loading[key] = false })
}

async function doLookup() {
  if (!alertSignature.value.trim()) return void ElMessage.warning('请输入告警 signature')
  lookup.value = await wrap('lookup', () => okbApi.lookup(alertSignature.value.trim(), traceIncludeDraft.value))
}

async function doTrace() {
  if (!traceCode.value.trim()) return void ElMessage.warning('请输入异常 stable_code')
  trace.value = await wrap('trace', () => okbApi.trace(traceCode.value.trim(), traceHops.value, traceIncludeDraft.value))
}

async function doEvidence(ref: string) {
  const view = await wrap('trace', () => okbApi.evidence(ref))
  if (view) { evidence.value = view; evidenceVisible.value = true }
}

async function doIngest() {
  if (!ingest.title.trim() || !ingest.content.trim()) return void ElMessage.warning('标题与正文必填')
  const out = await wrap('ingest', () => okbApi.ingest({ ...ingest }))
  if (out) {
    ingestResult.value = out
    ElMessage.success('摄取成功')
  }
}

async function doReport() {
  if (!report.diagnosisRef.trim() || !report.edgeId.trim()) return void ElMessage.warning('诊断引用与边 ID 必填')
  const out = await wrap('report', () => okbApi.report({
    diagnosis_ref: report.diagnosisRef.trim(), edge_id: report.edgeId.trim(),
    verdict: report.verdict, notes: report.notes,
  }))
  if (out) {
    reportResult.value = out
    ElMessage.success(out.recorded ? '反馈已记录' : '重复上报已忽略')
  }
}

async function doReviewQueue() {
  const res = await wrap('review', () => okbApi.reviewQueue())
  if (res) reviewEdges.value = res.edges
}

async function doConfirmEdge(id: string) {
  if (await wrap('review', () => okbApi.confirmEdge(id))) { ElMessage.success('已确认'); doReviewQueue() }
}

async function doRejectEdge(id: string) {
  if (await wrap('review', () => okbApi.rejectEdge(id))) { ElMessage.success('已维持否决'); doReviewQueue() }
}

function levelTag(level: string): 'success' | 'warning' | 'info' | 'danger' {
  if (level === 'case_verified') return 'success'
  if (level === 'doc_explicit') return 'warning'
  return 'info'
}

function sourceLabel(kind: SourceKind): string {
  if (kind === 'internal_case') return '工单/复盘'
  if (kind === 'vendor_doc') return '厂商文档'
  return '专家经验'
}

function evalSummary(hop: { evaluations?: Array<{ result: string }> }): string {
  const evals = hop.evaluations || []
  if (evals.length === 0) return '无条件'
  const unknown = evals.filter(e => e.result === 'unknown').length
  const passed = evals.filter(e => e.result === 'passed').length
  if (unknown > 0) return `${unknown} 项待验证`
  return passed === evals.length ? '条件满足' : '条件不满足'
}
</script>

<style scoped>
.okb-console { padding: 4px 0; }
.query-bar { display: flex; gap: 12px; align-items: center; margin-bottom: 12px; flex-wrap: wrap; }
.query-bar .el-input { width: 340px; }
.chain-card { margin-bottom: 10px; }
.chain-head { display: flex; gap: 10px; align-items: center; margin-bottom: 8px; }
.chain-meta { color: var(--el-text-color-secondary); font-size: 12px; }
.chain-hops { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.hop-tag { display: inline-flex; align-items: center; gap: 4px; }
.hop-eval { font-size: 11px; opacity: 0.75; }
.chain-note { margin-top: 6px; color: var(--el-color-warning); font-size: 12px; }
.evidence-head { display: flex; justify-content: space-between; margin-bottom: 10px; }
.evidence-item { border-top: 1px solid var(--el-border-color-lighter); padding: 10px 0; }
.evidence-meta { display: flex; gap: 8px; align-items: center; margin-bottom: 6px; }
.evidence-summary { color: var(--el-text-color-secondary); font-size: 12px; margin-bottom: 4px; }
.evidence-content { background: var(--el-fill-color-light); padding: 8px; border-radius: 4px; font-size: 12px; white-space: pre-wrap; word-break: break-all; margin: 0; }
.dim { color: var(--el-text-color-secondary); font-size: 12px; }
.section-title { font-weight: 600; margin-right: 8px; }
.dim.count { margin-right: 4px; }
.mono { font-family: var(--el-font-family-mono, monospace); font-size: 12px; }
.graph-container { width: 100%; height: 520px; border: 1px solid var(--el-border-color-lighter); border-radius: 6px; }
.graph-legend { display: flex; gap: 16px; margin-top: 8px; }
.legend-item { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--el-text-color-secondary); }
.legend-item i { width: 18px; height: 3px; border-radius: 2px; display: inline-block; }
.ingest-form, .report-form { max-width: 720px; }
.ingest-result { margin-left: 12px; color: var(--el-color-success); font-size: 12px; }
</style>
