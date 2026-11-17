<template>
  <div class="okb-console">
    <el-alert v-if="connectionError" type="error" :closable="false" show-icon class="spaced"
      title="OKB 服务不可达"
      :description="`${connectionError}——请确认 OKB 后端已启动（默认 :18090）且 Vite 代理已配置 /api → OKB。`" />

    <!-- 知识清单 -->
    <template v-if="page === 'okb:list'">
      <section class="panel">
        <div class="panel-title">
          <div><h2>知识总览</h2><span class="muted">按类型浏览库内全部知识，行内直达 trace / 证据 / 治理</span></div>
          <div class="okb-toolbar">
            <el-input v-model="listFilter.q" placeholder="搜索编码 / 名称 / signature" clearable class="okb-search" @keyup.enter="loadAllLists" />
            <el-select v-model="listFilter.status" placeholder="全部状态" clearable class="okb-select">
              <el-option label="curated" value="curated" /><el-option label="draft" value="draft" />
            </el-select>
            <el-button type="primary" :icon="Refresh" :loading="loading.list" @click="loadAllLists">刷新</el-button>
          </div>
        </div>
        <el-collapse v-model="listOpen" class="okb-collapse">
          <el-collapse-item name="anomaly">
            <template #title>
              <span class="okb-section-name"><el-icon><Warning /></el-icon>异常类型（节点）</span>
              <span class="muted">{{ listAnomaly?.total ?? '—' }} 条</span>
            </template>
            <el-table :data="listAnomaly?.rows ?? []" size="small" class="task-table" row-key="id">
              <el-table-column prop="stable_code" label="异常编码" min-width="200" show-overflow-tooltip />
              <el-table-column prop="name" label="名称" min-width="140" />
              <el-table-column label="状态" width="90">
                <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
              </el-table-column>
              <el-table-column label="更新时间" min-width="150">
                <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
              </el-table-column>
              <el-table-column label="操作" width="130" fixed="right">
                <template #default="{ row }">
                  <el-button link type="primary" size="small" @click="goto('okb:query', row.stable_code, true)">trace</el-button>
                  <el-button link size="small" @click="doEvidence(row.stable_code)">证据</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无异常类型" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
          <el-collapse-item name="rule">
            <template #title>
              <span class="okb-section-name"><el-icon><Link /></el-icon>因果规则（边）</span>
              <span class="muted">{{ listRule?.total ?? '—' }} 条</span>
            </template>
            <el-table :data="listRule?.rows ?? []" size="small" class="task-table" row-key="id">
              <el-table-column label="因果（源 → 目标）" min-width="280">
                <template #default="{ row }"><span class="mono">{{ row.source_code }}</span><el-icon class="okb-arrow"><ArrowRight /></el-icon><span class="mono">{{ row.target_code }}</span></template>
              </el-table-column>
              <el-table-column label="证据等级" width="120">
                <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ levelLabel(row.evidence_level) }}</el-tag></template>
              </el-table-column>
              <el-table-column label="置信度" width="85">
                <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="印证" width="80">
                <template #default="{ row }">{{ row.case_support_count }} 工单</template>
              </el-table-column>
              <el-table-column label="状态" width="100">
                <template #default="{ row }">
                  <el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag>
                  <el-tag v-if="row.review_flagged" type="danger" size="small">待复核</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="180" fixed="right">
                <template #default="{ row }">
                  <el-button link size="small" @click="doEvidence(row.id)">证据</el-button>
                  <el-button link type="success" size="small" @click="doConfirmEdge(row.id)">确认</el-button>
                  <el-button link type="danger" size="small" @click="doRejectEdge(row.id)">否决</el-button>
                  <el-button link size="small" @click="report.edgeId = row.id; goto('okb:report')">反馈</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无因果规则" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
          <el-collapse-item name="mapping">
            <template #title>
              <span class="okb-section-name"><el-icon><Bell /></el-icon>告警映射（入口）</span>
              <span class="muted">{{ listMapping?.total ?? '—' }} 条</span>
            </template>
            <el-table :data="listMapping?.rows ?? []" size="small" class="task-table" row-key="id">
              <el-table-column prop="alert_signature" label="告警 signature" min-width="200" show-overflow-tooltip />
              <el-table-column label="候选异常" min-width="180">
                <template #default="{ row }"><span class="mono">{{ row.stable_code }}</span><div class="table-secondary">{{ row.name }}</div></template>
              </el-table-column>
              <el-table-column label="置信度" width="85">
                <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="证据等级" width="120">
                <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ levelLabel(row.evidence_level) }}</el-tag></template>
              </el-table-column>
              <el-table-column label="状态" width="90">
                <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
              </el-table-column>
              <el-table-column label="操作" width="120" fixed="right">
                <template #default="{ row }">
                  <el-button link type="primary" size="small" @click="alertSignature = row.alert_signature; goto('okb:query'); doLookup()">lookup</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="暂无告警映射" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
          <el-collapse-item name="review">
            <template #title>
              <span class="okb-section-name"><el-icon><CircleCheck /></el-icon>复核队列（被否决待处理）</span>
              <span class="muted">{{ reviewEdges?.length ?? '—' }} 条</span>
            </template>
            <el-table :data="reviewEdges ?? []" size="small" class="task-table" row-key="id">
              <el-table-column label="因果（源 → 目标）" min-width="260">
                <template #default="{ row }"><span class="mono">{{ row.source_code }}</span><el-icon class="okb-arrow"><ArrowRight /></el-icon><span class="mono">{{ row.target_code }}</span></template>
              </el-table-column>
              <el-table-column label="置信度" width="90">
                <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
              </el-table-column>
              <el-table-column label="状态" width="90">
                <template #default="{ row }"><el-tag type="info" size="small">{{ row.status }}</el-tag></template>
              </el-table-column>
              <el-table-column label="操作" width="170" fixed="right">
                <template #default="{ row }">
                  <el-button link type="success" size="small" @click="doConfirmEdge(row.id)">确认恢复</el-button>
                  <el-button link type="danger" size="small" @click="doRejectEdge(row.id)">维持否决</el-button>
                </template>
              </el-table-column>
              <template #empty><el-empty description="复核队列为空" :image-size="60" /></template>
            </el-table>
          </el-collapse-item>
        </el-collapse>
      </section>
    </template>

    <!-- 知识图谱 -->
    <section v-else-if="page === 'okb:graph'" class="panel">
      <div class="panel-title">
        <div><h2>故障因果图</h2><span class="muted">节点 = 异常类型 · 连线 = 因果边（颜色按证据等级，粗细按置信度，虚线 = 未审核）</span></div>
        <div class="okb-toolbar">
          <el-checkbox v-model="graphIncludeDraft">显示草稿边</el-checkbox>
          <el-button type="primary" :icon="Refresh" :loading="loading.graph" @click="doGraph">加载图谱</el-button>
        </div>
      </div>
      <div v-show="graphLoaded" ref="graphContainer" class="okb-graph-area" />
      <el-empty v-if="graphEmpty" description="图中没有节点，请先在「提取数据 → 资料摄取」入库工单" :image-size="96" />
      <div v-if="graphLegend.length" class="topology-legend">
        <span v-for="item in graphLegend" :key="item.label"><i :style="{ background: item.color }" />{{ item.label }}</span>
      </div>
      <el-dialog v-model="nodeDialogVisible" :title="nodeDialogNode?.name" width="480px">
        <p class="dialog-intro">{{ nodeDialogNode?.stable_code }}（{{ nodeDialogNode?.status }}）</p>
        <div class="drawer-actions">
          <el-button type="primary" size="small" @click="goto('okb:query', nodeDialogNode!.stable_code, true)">从该异常 trace</el-button>
          <el-button size="small" @click="doEvidence(nodeDialogNode!.stable_code)">查看证据</el-button>
        </div>
      </el-dialog>
    </section>

    <!-- 图谱查询 -->
    <template v-else-if="page === 'okb:query'">
      <section class="panel">
        <div class="panel-title">
          <div><h2>告警入口 lookup</h2><span class="muted">按告警 signature 检索候选异常</span></div>
        </div>
        <div class="okb-toolbar okb-toolbar-row">
          <el-input v-model="alertSignature" placeholder="告警 signature，如 ALERT_GPU_ECC" clearable class="okb-search-lg" @keyup.enter="doLookup">
            <template #prepend>signature</template>
          </el-input>
          <el-checkbox v-model="traceIncludeDraft">含草稿</el-checkbox>
          <el-button type="primary" :loading="loading.lookup" @click="doLookup">查询候选异常</el-button>
        </div>
        <el-table v-if="lookup" :data="lookup.candidates" size="small" class="task-table" row-key="anomaly_id">
          <el-table-column prop="stable_code" label="异常编码" min-width="200" show-overflow-tooltip />
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column label="置信度" width="90">
            <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
          </el-table-column>
          <el-table-column label="证据等级" width="120">
            <template #default="{ row }"><el-tag :type="levelTag(row.evidence_level)" size="small">{{ levelLabel(row.evidence_level) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }"><el-tag :type="row.status === 'curated' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="130" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="traceCode = row.stable_code; doTrace()">trace</el-button>
              <el-button link size="small" @click="doEvidence(row.stable_code)">证据</el-button>
            </template>
          </el-table-column>
          <template #empty><el-empty description="该告警暂无候选异常（可能均为草稿，勾选含草稿后重试）" :image-size="60" /></template>
        </el-table>
      </section>
      <section class="panel spaced">
        <div class="panel-title">
          <div><h2>因果链遍历 trace</h2><span class="muted">多跳候选根因链 · 每跳带条件求值与证据等级</span></div>
        </div>
        <div class="okb-toolbar okb-toolbar-row">
          <el-input v-model="traceCode" placeholder="异常 stable_code，如 gpu.ecc.error" clearable class="okb-search-lg" @keyup.enter="doTrace">
            <template #prepend>anomaly</template>
          </el-input>
          <el-input-number v-model="traceHops" :min="1" :max="5" />
          <el-button type="primary" :loading="loading.trace" @click="doTrace">遍历候选链</el-button>
        </div>
        <el-empty v-if="trace && trace.chains.length === 0" description="无候选链（可能均为草稿，勾选含草稿后重试）" :image-size="80" />
        <div v-for="(chain, i) in trace?.chains ?? []" :key="i" class="okb-chain">
          <div class="okb-chain-head">
            <el-tag :type="chain.fully_curated ? 'success' : 'warning'" size="small">
              {{ chain.fully_curated ? '逐跳已核' : '含未审核跳' }}
            </el-tag>
            <span class="muted">{{ chain.hops.length }} 跳 · 最小置信度 {{ chain.min_confidence.toFixed(2) }}</span>
          </div>
          <div class="okb-chain-hops">
            <template v-for="(hop, j) in chain.hops" :key="hop.edge_id">
              <span class="okb-hop" :class="hopClass(hop)">
                <span class="okb-hop-name">{{ hop.target_name }}</span>
                <span class="okb-hop-eval">{{ evalSummary(hop) }}</span>
              </span>
              <el-icon v-if="j < chain.hops.length - 1" class="okb-arrow"><ArrowRight /></el-icon>
            </template>
            <el-button link type="primary" size="small" @click="doEvidence(chain.hops[chain.hops.length - 1].edge_id)">边证据</el-button>
          </div>
          <div v-if="chain.combined_note" class="okb-chain-note">{{ chain.combined_note }}</div>
        </div>
      </section>
    </template>

    <!-- 复核队列 -->
    <section v-else-if="page === 'okb:review'" class="panel">
      <div class="panel-title">
        <div><h2>复核队列</h2><span class="muted">被否决降级后待人工复核的因果边</span></div>
        <el-button type="primary" :icon="Refresh" :loading="loading.review" @click="doReviewQueue">刷新队列</el-button>
      </div>
      <el-table :data="reviewEdges ?? []" size="small" class="task-table" row-key="id">
        <el-table-column label="因果（源 → 目标）" min-width="260">
          <template #default="{ row }"><span class="mono">{{ row.source_code }}</span><el-icon class="okb-arrow"><ArrowRight /></el-icon><span class="mono">{{ row.target_code }}</span></template>
        </el-table-column>
        <el-table-column label="置信度" width="90">
          <template #default="{ row }">{{ row.confidence.toFixed(2) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }"><el-tag type="info" size="small">{{ row.status }}</el-tag></template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right">
          <template #default="{ row }">
            <el-button link type="success" size="small" @click="doConfirmEdge(row.id)">确认恢复</el-button>
            <el-button link type="danger" size="small" @click="doRejectEdge(row.id)">维持否决</el-button>
          </template>
        </el-table-column>
        <template #empty><el-empty description="复核队列为空" :image-size="80" /></template>
      </el-table>
    </section>

    <!-- 资料摄取 -->
    <section v-else-if="page === 'okb:ingest'" class="panel">
      <div class="panel-title">
        <div><h2>资料摄取</h2><span class="muted">脱敏工单 / 厂商文档 / 专家经验入库 → 提取候选知识；独立工单印证 ≥2 自动升 curated</span></div>
      </div>
      <el-form label-width="90px" class="okb-form" @submit.prevent>
        <el-form-item label="数据源">
          <el-select v-model="ingest.kind" class="okb-select">
            <el-option label="脱敏工单 / 复盘" value="internal_case" />
            <el-option label="厂商文档" value="vendor_doc" />
            <el-option label="专家经验" value="expert_input" />
          </el-select>
        </el-form-item>
        <el-form-item label="标题">
          <el-input v-model="ingest.title" placeholder="如 工单-2026-001 GPU ECC 复盘" />
        </el-form-item>
        <el-form-item label="正文">
          <el-input v-model="ingest.content" type="textarea" :rows="10"
            placeholder="脱敏后的工单/文档正文。当前开发提取器按 JSON 标记片段识别：&#10;{&quot;anomaly&quot;: &quot;gpu.ecc.error&quot;, &quot;name&quot;: &quot;GPU ECC 错误&quot;, &quot;code&quot;: &quot;gpu.ecc.error&quot;, &quot;alert&quot;: &quot;ALERT_GPU_ECC&quot;}&#10;{&quot;source&quot;: &quot;gpu.ecc.error&quot;, &quot;target&quot;: &quot;gpu.link.degrade&quot;}&#10;含 IP / 手机号 / 凭据等内容会被脱敏门禁拒绝（422）。" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading.ingest" @click="doIngest">摄取入库</el-button>
          <span v-if="ingestResult" class="okb-success">
            新增 {{ ingestResult.new_anomalies }} 异常 / {{ ingestResult.new_rules }} 因果边 / {{ ingestResult.new_mappings }} 告警映射<template v-if="ingestResult.curated_rules">，{{ ingestResult.curated_rules }} 边自动升 curated</template>
          </span>
        </el-form-item>
      </el-form>
    </section>

    <!-- 诊断反馈 -->
    <section v-else-if="page === 'okb:report'" class="panel">
      <div class="panel-title">
        <div><h2>诊断反馈</h2><span class="muted">上报诊断使用的边与最终判定：确认 +0.05 置信度，否决降级入复核；同一诊断引用幂等</span></div>
      </div>
      <el-form label-width="110px" class="okb-form" @submit.prevent>
        <el-form-item label="诊断引用">
          <el-input v-model="report.diagnosisRef" placeholder="本次诊断唯一引用（幂等键），如 diag-2026-001" />
        </el-form-item>
        <el-form-item label="因果边 ID">
          <el-input v-model="report.edgeId" placeholder="trace 结果或知识清单中的边 ID" />
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
          <span v-if="reportResult" class="okb-success">
            {{ reportResult.recorded ? `已记录：置信度 ${reportResult.new_confidence.toFixed(2)}，状态 ${reportResult.new_status}` : '重复上报：幂等忽略' }}
          </span>
        </el-form-item>
      </el-form>
    </section>

    <!-- 证据回链对话框（各页共用） -->
    <el-dialog v-model="evidenceVisible" title="证据回链" width="720px">
      <div v-if="evidence" class="drawer-heading okb-evidence-head">
        <span class="muted">实体 {{ evidence.entity_type }} / {{ evidence.entity_id }}</span>
        <el-tag size="small">revision {{ evidence.revision_id?.slice(0, 8) }}</el-tag>
      </div>
      <el-empty v-if="evidence && evidence.items.length === 0" description="无证据" :image-size="60" />
      <div v-for="item in evidence?.items ?? []" :key="item.evidence_id" class="okb-evidence-item">
        <div class="okb-evidence-meta">
          <el-tag :type="levelTag(item.level)" size="small">{{ levelLabel(item.level) }}</el-tag>
          <span v-if="item.source_kind" class="muted">{{ sourceLabel(item.source_kind) }}</span>
          <span class="muted">{{ item.created_by === 'model' ? '模型提取' : '专家' }}</span>
        </div>
        <div class="table-secondary">{{ item.summary }}</div>
        <pre class="okb-evidence-content">{{ item.content }}</pre>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref, watch, nextTick, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import { Warning, Link, Bell, CircleCheck, ArrowRight, Refresh } from '@element-plus/icons-vue'
import * as echarts from 'echarts'
import {
  okbApi, type LookupResult, type TraceResult, type EvidenceView,
  type IngestOutput, type ReportResult, type SourceKind, type Verdict, type GraphView,
  type KnowledgeList, type KnowledgeRow,
} from '../api/okb'

// page 由 App.vue 侧边栏驱动（okb:list / okb:graph / okb:query / okb:review / okb:ingest / okb:report）
const props = defineProps<{ page: string }>()
const emit = defineEmits<{ (e: 'navigate', page: string): void }>()

const loading = reactive({ lookup: false, trace: false, ingest: false, report: false, review: false, graph: false, list: false })
const connectionError = ref('')

// 统一错误提示：网络失败（fetch 抛 TypeError）标记连接错误，其余弹消息
function notifyError(err: unknown) {
  const msg = err instanceof Error ? err.message : String(err)
  if (err instanceof TypeError) connectionError.value = msg
  ElMessage.error(msg)
}

function wrap<T>(key: keyof typeof loading, fn: () => Promise<T>): Promise<T | null> {
  loading[key] = true
  connectionError.value = ''
  return fn().catch((err: unknown) => { notifyError(err); return null }).finally(() => { loading[key] = false })
}

// 跨页跳转：先记上下文再上抛导航；目标数据为响应式状态，不依赖目标页 DOM
function goto(page: string, code?: string, doTraceNow = false) {
  if (code) traceCode.value = code
  emit('navigate', page)
  if (doTraceNow) doTrace()
}

// ---------- 知识清单 ----------
const listOpen = ref<string[]>(['anomaly', 'rule', 'mapping', 'review'])
const listFilter = reactive({ q: '', status: '' })
const listAnomaly = ref<KnowledgeList | null>(null)
const listRule = ref<KnowledgeList | null>(null)
const listMapping = ref<KnowledgeList | null>(null)
const reviewEdges = ref<KnowledgeRow[] | null>(null)

async function loadAllLists() {
  loading.list = true
  connectionError.value = ''
  const params = { q: listFilter.q || undefined, status: listFilter.status || undefined, limit: 200 }
  try {
    const [a, r, m, q] = await Promise.all([
      okbApi.knowledge('anomaly', params),
      okbApi.knowledge('rule', params),
      okbApi.knowledge('mapping', params),
      okbApi.reviewQueue().catch(() => ({ edges: [] as KnowledgeRow[] })),
    ])
    listAnomaly.value = a
    listRule.value = r
    listMapping.value = m
    reviewEdges.value = q.edges
  } catch (err: unknown) {
    notifyError(err)
  } finally {
    loading.list = false
  }
}

// ---------- 查询 ----------
const alertSignature = ref('')
const traceCode = ref('')
const traceHops = ref(3)
const traceIncludeDraft = ref(false)
const lookup = ref<LookupResult | null>(null)
const trace = ref<TraceResult | null>(null)

async function doLookup() {
  if (!alertSignature.value.trim()) return void ElMessage.warning('请输入告警 signature')
  lookup.value = await wrap('lookup', () => okbApi.lookup(alertSignature.value.trim(), traceIncludeDraft.value))
}

async function doTrace() {
  if (!traceCode.value.trim()) return void ElMessage.warning('请输入异常 stable_code')
  trace.value = await wrap('trace', () => okbApi.trace(traceCode.value.trim(), traceHops.value, traceIncludeDraft.value))
}

// ---------- 证据 ----------
const evidenceVisible = ref(false)
const evidence = ref<EvidenceView | null>(null)

async function doEvidence(ref: string) {
  const view = await wrap('trace', () => okbApi.evidence(ref))
  if (view) { evidence.value = view; evidenceVisible.value = true }
}

// ---------- 图谱 ----------
const graphContainer = ref<HTMLDivElement>()
const graphIncludeDraft = ref(false)
const graphLoaded = ref(false)
const graphEmpty = ref(false)
const graphLegend = ref<Array<{ label: string; color: string }>>([])
const nodeDialogVisible = ref(false)
const nodeDialogNode = ref<GraphView['nodes'][number] | null>(null)
let chart: echarts.ECharts | null = null

const LEVEL_COLORS: Record<string, string> = {
  case_verified: '#38a078',
  doc_explicit: '#d79b35',
  inferred: '#8b98aa',
  expert_only: '#8e7cc3',
}

async function doGraph() {
  const view = await wrap('graph', () => okbApi.graph())
  if (!view) return
  graphEmpty.value = view.nodes.length === 0
  if (graphEmpty.value) return
  graphLoaded.value = true
  // v-show 已挂载容器；等 DOM 更新完成后初始化 echarts 并渲染
  await nextTick()
  renderGraph(view)
}

function ensureChart(): echarts.ECharts | null {
  if (!graphContainer.value) return null
  if (chart && chart.isDisposed()) chart = null
  if (!chart) chart = echarts.init(graphContainer.value)
  return chart
}

function renderGraph(view: GraphView) {
  const instance = ensureChart()
  if (!instance) return
  const edges = view.edges.filter(e => graphIncludeDraft.value || e.status === 'curated')
  const presentLevels = new Set(edges.map(e => e.evidence_level))
  graphLegend.value = Array.from(presentLevels).map(level => ({
    label: levelLabel(level), color: LEVEL_COLORS[level] || '#8b98aa',
  }))
  instance.setOption({
    tooltip: {
      formatter: (params: { dataType: string; data: Record<string, unknown> }) => {
        const d = params.data
        if (params.dataType === 'edge') {
          return `<b>${d.source} → ${d.target}</b><br/>证据：${d.evidence_level} · 置信度 ${Number(d.confidence).toFixed(2)}<br/>状态：${d.status} · 印证 ${d.case_support_count} 工单`
        }
        return `<b>${d.name}</b><br/>${d.stable_code}<br/>状态：${d.status}`
      },
    },
    series: [{
      type: 'graph', layout: 'force', roam: true, draggable: true,
      force: { repulsion: 320, edgeLength: [90, 180], gravity: 0.08 },
      label: { show: true, position: 'right', fontSize: 11, formatter: (p: { data: { name: string } }) => p.data.name },
      data: view.nodes.map(n => ({
        id: n.stable_code, name: n.name, stable_code: n.stable_code, status: n.status,
        symbolSize: 34,
        itemStyle: { color: n.status === 'curated' ? '#3479df' : '#a0cfff' },
      })),
      edges: edges.map(e => ({
        source: e.source, target: e.target, evidence_level: e.evidence_level,
        confidence: e.confidence, status: e.status, case_support_count: e.case_support_count,
        lineStyle: {
          color: LEVEL_COLORS[e.evidence_level] || '#8b98aa',
          width: 1 + Math.round(e.confidence * 3),
          type: e.status === 'curated' ? 'solid' : 'dashed',
          curveness: 0.15,
        },
      })),
      emphasis: { focus: 'adjacency' },
    }],
  })
  instance.off('click')
  instance.on('click', (params: echarts.ECElementEvent) => {
    const data = (params.data ?? {}) as { stable_code?: string }
    if (params.dataType === 'node' && data.stable_code) {
      nodeDialogNode.value = view.nodes.find(n => n.stable_code === data.stable_code) ?? null
      nodeDialogVisible.value = true
    }
  })
}

// ---------- 摄取 ----------
const ingest = reactive<{ kind: SourceKind; title: string; content: string }>({
  kind: 'internal_case', title: '', content: '',
})
const ingestResult = ref<IngestOutput | null>(null)

async function doIngest() {
  if (!ingest.title.trim() || !ingest.content.trim()) return void ElMessage.warning('标题与正文必填')
  const out = await wrap('ingest', () => okbApi.ingest({ ...ingest }))
  if (out) {
    ingestResult.value = out
    ElMessage.success('摄取成功')
  }
}

// ---------- 反馈 ----------
const report = reactive<{ diagnosisRef: string; edgeId: string; verdict: Verdict; notes: string }>({
  diagnosisRef: '', edgeId: '', verdict: 'confirmed', notes: '',
})
const reportResult = ref<ReportResult | null>(null)

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

// ---------- 复核与治理 ----------
async function doReviewQueue() {
  const res = await wrap('review', () => okbApi.reviewQueue())
  if (res) reviewEdges.value = res.edges
}

// 治理操作后同时刷新复核队列与已加载的清单，避免页面间数据滞留
async function refreshAfterGovernance() {
  await Promise.all([doReviewQueue(), listRule.value ? loadAllLists() : Promise.resolve()])
}

async function doConfirmEdge(id: string) {
  if (await wrap('review', () => okbApi.confirmEdge(id))) {
    ElMessage.success('已确认')
    void refreshAfterGovernance()
  }
}

async function doRejectEdge(id: string) {
  if (await wrap('review', () => okbApi.rejectEdge(id))) {
    ElMessage.success('已维持否决')
    void refreshAfterGovernance()
  }
}

// ---------- 展示辅助 ----------
function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

function levelTag(level?: string): 'success' | 'warning' | 'info' | 'danger' {
  if (level === 'case_verified') return 'success'
  if (level === 'doc_explicit') return 'warning'
  if (level === 'expert_only') return 'danger'
  return 'info'
}

function levelLabel(level?: string): string {
  return ({ case_verified: '案例实证', doc_explicit: '文档明示', inferred: '模型推导', expert_only: '专家经验' } as Record<string, string>)[level ?? ''] ?? level ?? '—'
}

function sourceLabel(kind: SourceKind): string {
  if (kind === 'internal_case') return '工单 / 复盘'
  if (kind === 'vendor_doc') return '厂商文档'
  return '专家经验'
}

function evalSummary(hop: { evaluations?: Array<{ result: string }> }): string {
  const evals = hop.evaluations || []
  if (evals.length === 0) return '无条件'
  const unknown = evals.filter(e => e.result === 'unknown').length
  if (unknown > 0) return `${unknown} 项待验证`
  return evals.every(e => e.result === 'passed') ? '条件满足' : '条件不满足'
}

function hopClass(hop: { evaluations?: Array<{ result: string }> }): string {
  const evals = hop.evaluations || []
  if (evals.some(e => e.result === 'unknown')) return 'unknown'
  if (evals.length > 0 && evals.every(e => e.result === 'passed')) return 'passed'
  return ''
}

// ---------- 页面切换按需加载 ----------
watch(() => props.page, page => {
  if (page !== 'okb:graph' && chart) { chart.dispose(); chart = null } // 容器随 v-show 常驻，但离开仍释放实例
  if (page === 'okb:list' && !listAnomaly.value) loadAllLists()
  if (page === 'okb:review' && !reviewEdges.value) doReviewQueue()
  if (page === 'okb:graph' && !graphLoaded.value) doGraph()
}, { immediate: true })

watch(graphIncludeDraft, () => { if (graphLoaded.value) doGraph() })

onBeforeUnmount(() => { chart?.dispose(); chart = null })
</script>

<style scoped>
.okb-toolbar { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
.okb-toolbar-row { margin: 18px 0; }
.okb-search { width: 240px; } .okb-search-lg { width: 320px; } .okb-select { width: 140px; }
.okb-collapse { margin-top: 18px; border-top: 0; }
.okb-collapse :deep(.el-collapse-item__header) { font-size: 13px; }
.okb-collapse :deep(.el-collapse-item__wrap) { border-bottom: 1px solid #eef2f7; }
.okb-section-name { display: inline-flex; align-items: center; gap: 7px; font-weight: 600; margin-right: 10px; }
.okb-section-name .el-icon { color: #6c8bb1; }
.okb-arrow { color: #b8c4d4; margin: 0 6px; vertical-align: middle; }
.okb-graph-area { height: 520px; border: 1px solid #e5ebf2; border-radius: 10px;
  background: radial-gradient(circle, #d7e2f0 1px, transparent 1px) 0 0 / 22px 22px, #fafcff; }
.okb-chain { border: 1px solid #e5ebf2; border-radius: 10px; padding: 14px 16px; margin-bottom: 10px; background: #fbfcff; }
.okb-chain-head { display: flex; gap: 10px; align-items: center; margin-bottom: 10px; }
.okb-chain-hops { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.okb-hop { display: inline-flex; flex-direction: column; gap: 3px; border: 1px solid #d5e3f5; background: #f0f6ff; border-radius: 8px; padding: 7px 12px; }
.okb-hop.unknown { border-color: #f3d19e; background: #fffaf0; }
.okb-hop.passed { border-color: #bce3cd; background: #f0faf4; }
.okb-hop-name { font-size: 12px; font-weight: 600; color: #203249; }
.okb-hop-eval { font-size: 11px; color: #7b899d; }
.okb-chain-note { margin-top: 8px; color: #b26205; font-size: 11px; }
.okb-form { max-width: 760px; margin-top: 16px; }
.okb-success { margin-left: 12px; color: #38a078; font-size: 12px; }
.okb-evidence-head { display: flex; justify-content: space-between; align-items: center; margin-top: 0; }
.okb-evidence-item { border-top: 1px solid #eef2f7; padding: 12px 0; }
.okb-evidence-item:first-of-type { border-top: 0; }
.okb-evidence-meta { display: flex; gap: 8px; align-items: center; margin-bottom: 6px; }
.okb-evidence-content { background: #f6f8fc; padding: 10px; border-radius: 6px; font: 11px/1.7 "SFMono-Regular", Consolas, monospace; white-space: pre-wrap; overflow-wrap: anywhere; margin: 6px 0 0; }
</style>
