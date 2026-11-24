<template>
  <div class="diag-console">
    <div class="diag-layout">
      <!-- ============ 左栏：主工作流（选场景 → 诊断 → 结果） ============ -->
      <div class="diag-main">
        <!-- ① 选择测试场景（单告警样本 / 告警风暴，点选即诊断） -->
        <div class="diag-section">
          <div class="diag-section-title">① 选择测试场景（点选即诊断）</div>
          <div class="diag-scene-group-label">单告警（正向：已知故障 → 推影响）</div>
          <div class="diag-scene-grid">
            <button v-for="s in samples" :key="s.id" class="diag-scene-btn"
              :class="{ active: alertSignature === s.signature }" @click="applySample(s)">
              <span class="diag-scene-btn-label">{{ s.label }}</span>
              <span class="muted diag-scene-btn-sub mono">{{ deviceOf(s) }}</span>
              <el-tag size="small" :type="expectedTone(s.expected)" class="diag-scene-btn-tag">{{ expectedLabel(s.expected) }}</el-tag>
            </button>
          </div>
          <div class="diag-scene-group-label">告警风暴（反向：多台告警 → 反推上层根因）</div>
          <div class="diag-scene-grid">
            <button v-for="sc in STORM_SCENES" :key="sc.kind" class="diag-scene-btn diag-scene-btn-storm"
              :class="{ active: activeStorm === sc.kind }" @click="runStorm(sc.kind)">
              <span class="diag-scene-btn-label">{{ sc.label }}</span>
              <span class="muted diag-scene-btn-sub">{{ sc.note }}</span>
              <el-tag size="small" type="danger" class="diag-scene-btn-tag">风暴反推</el-tag>
            </button>
          </div>
        </div>

        <!-- ② 当前输入（选中场景载入，可手改） -->
        <div class="diag-section">
          <div class="diag-section-title">② 诊断输入</div>
          <div class="diag-input-row">
            <el-input v-model="alertSignature" placeholder="告警 signature" class="diag-input" @keyup.enter="run" clearable />
            <el-input v-model="entityId" placeholder="实体 ID（影响半径用）" class="diag-input-entity" @keyup.enter="run" clearable />
            <el-button type="primary" :loading="running" @click="run">开始诊断</el-button>
          </div>
        </div>

        <!-- ③ 判定序执行流（SSE 逐节点点亮） -->
        <div v-if="started" class="diag-steps">
          <div v-for="step in STEPS" :key="step.key" class="diag-step" :class="stepState(step.key)">
            <i class="diag-step-dot" />
            <div>
              <div class="diag-step-name">{{ step.label }}</div>
              <div class="diag-step-note">{{ stepNote(step.key) }}</div>
            </div>
          </div>
        </div>

        <el-empty v-if="started && !running && !result && !errorMsg && !stormResult" description="无结果" :image-size="60" />
        <el-alert v-if="errorMsg" :title="errorMsg" type="error" :closable="false" class="diag-error" />

        <!-- ④ 结果（正向诊断） -->
        <div v-if="result" class="diag-result">
          <el-alert v-if="conclusion.summary" :title="conclusion.summary" type="warning" show-icon :closable="false" />

          <div class="diag-section">
            <div class="diag-section-title">告警归一（{{ result.candidates.length }} 候选）</div>
            <div v-for="c in result.candidates" :key="c.anomaly_id" class="diag-candidate">
              <el-tag size="small" :type="c.status === 'curated' ? 'success' : 'info'">{{ c.status }}</el-tag>
              <span class="mono">{{ c.stable_code }}</span>
              <span>{{ c.name }}</span>
              <el-tag v-if="c.severity_online" size="small" type="danger">线上 {{ c.severity_online }}</el-tag>
              <el-tag v-if="c.clock_hint" size="small" type="warning">时钟 {{ c.clock_hint }}</el-tag>
            </div>
          </div>

      <!-- 主链（证据回链 + 跳转图谱/证据） -->
      <div class="diag-section" v-if="conclusion.primary_chain?.length">
        <div class="diag-section-title">主因果链（{{ conclusion.start_code }} 起）</div>
        <div class="diag-chain">
          <span class="mono diag-chain-node">{{ conclusion.start_code }}</span>
          <template v-for="(hop, i) in conclusion.primary_chain" :key="i">
            <el-icon class="diag-arrow"><ArrowRight /></el-icon>
            <span class="mono diag-chain-node">
              {{ hop.target }}
              <el-tag size="small" :type="hop.evidence_level === 'runtime_verified' ? 'success' : 'info'">
                {{ hop.evidence_level }}
              </el-tag>
              <el-button link type="primary" size="small" @click="showEvidence(hop.target)">证据</el-button>
            </span>
          </template>
        </div>
        <div class="muted diag-chain-hint">点击节点可跳转知识图谱；点"证据"回链 OKB 出处片段。</div>
      </div>

      <!-- 待验证检查项（evidence-driven） -->
      <div class="diag-section" v-if="result.pending_checks?.length">
        <div class="diag-section-title">待验证检查项（{{ result.pending_checks.length }}）</div>
        <div v-for="(pc, i) in result.pending_checks" :key="i" class="diag-check">
          <el-tag size="small" type="warning">取证</el-tag>
          <span class="mono">{{ pc.fact_key }} {{ pc.op }}</span>
          <span class="muted">edge {{ pc.edge_id?.slice(0, 8) }}…</span>
        </div>
      </div>

      <!-- 跨域取证提示（same_event 对端） -->
      <div class="diag-section" v-if="result.cross_domain_peers?.length">
        <div class="diag-section-title">跨域取证提示（同事件对端）</div>
        <div v-for="(p, i) in result.cross_domain_peers" :key="i" class="diag-check">
          <el-tag size="small" type="warning">{{ p.kind === 'same_event' ? '同事件' : p.kind }}</el-tag>
          <span class="mono">{{ p.other_code }}</span>
          <el-tag v-if="p.other_severity" size="small" type="danger">对方等级 {{ p.other_severity }}</el-tag>
          <span class="muted">建议查同柜/同链路对端是否同时告警</span>
        </div>
      </div>

      <!-- 影响半径（因果图×拓扑合遍历：预期告警集） -->
      <div class="diag-section" v-if="radiusGroups.length || (result.impact_radius?.surface ?? []).length">
        <div class="diag-section-title">
          影响半径（{{ result.impact_radius?.expected_alerts?.length ?? 0 }} 条预期告警，起点实体 {{ result.impact_radius?.start_entity }}）
        </div>
        <div v-if="(result.impact_radius?.surface ?? []).length" class="diag-check">
          <el-tag size="small" type="warning">观测面</el-tag>
          <el-tag v-for="p in result.impact_radius?.surface ?? []" :key="p" size="small" class="mono">{{ p }}</el-tag>
          <span class="muted">逻辑会话解析到的承载端口（本端 + 对端）</span>
        </div>
        <div v-for="(group, code) in radiusGroups" :key="code" class="diag-radius-group">
          <div class="diag-radius-code">
            <span class="mono">{{ code }}</span>
            <el-tag size="small" type="info">{{ group.entities.length }} 实体</el-tag>
            <span class="muted">edge {{ group.edgeId?.slice(0, 8) }}…</span>
          </div>
          <div class="diag-radius-entities">
            <el-tag v-for="e in group.entities" :key="e" size="small" class="mono diag-radius-entity">{{ e }}</el-tag>
          </div>
        </div>
        <div v-for="u in result.impact_radius?.unbound ?? []" :key="u.stable_code" class="diag-check">
          <el-tag size="small" type="warning">绑定不可用</el-tag>
          <span class="mono">{{ u.stable_code }}</span>
          <span class="muted">拓扑实体未同步（如制冷域），实体清单暂不承诺</span>
        </div>
      </div>

      <!-- 飞轮回写 -->
      <div class="diag-section" v-if="result">
        <div class="diag-section-title">飞轮回写</div>
        <el-tag :type="result.reported ? 'success' : 'info'">
          {{ result.reported ? '已回写 confirmed（置信度已更新）' : '测试模式：不回写（飞轮只吃真实告警流的诊断，测试跑不抬升置信度）' }}
        </el-tag>
      </div>
      </div><!-- /diag-result -->

      <!-- ④' 风暴反推结果 -->
      <div v-if="stormResult" class="diag-result">
        <el-alert :title="stormResult.conclusion?.summary || stormResult.rationale"
          :type="stormResult.root_cause ? 'error' : 'info'" show-icon :closable="false" />
        <div v-if="stormResult.root_cause" class="diag-section">
          <div class="diag-section-title">疑似根因</div>
          <div class="diag-storm-root">
            <el-tag type="danger">{{ stormResult.root_cause.entity }}</el-tag>
            <span class="mono">{{ stormResult.root_cause.stable_code }}</span>
            <el-tag size="small" :type="stormResult.root_cause.verify === 'knowledge_backed' ? 'success' : 'warning'">
              {{ stormResult.root_cause.verify === 'knowledge_backed' ? 'OKB 验证通过' : '拓扑推断（知识空白）' }}
            </el-tag>
            <span class="muted">{{ stormResult.alert_count }} 条告警的共同供电祖先</span>
          </div>
        </div>
        <p class="diag-storm-rationale">{{ stormResult.rationale }}</p>
      </div>

      <!-- ⑤ 对账（正向诊断有半径时可用） -->
      <div class="diag-section" v-if="result?.impact_radius?.expected_alerts?.length">
        <div class="diag-section-title">⑤ 对账（预期告警集 vs 实际观测——缺失即信号）</div>
        <div class="diag-reconcile-row">
          <el-input v-model="observedText" type="textarea" :rows="3"
            placeholder="实际观测到的告警，每行一条：实体, 异常类型（如 pdu-A1, power.rack.pdu.output_lost_dual）"
            class="diag-reconcile-input" />
          <el-button type="primary" plain :loading="reconciling" @click="runReconcile">对账</el-button>
        </div>
        <div v-if="reconcileResult" class="diag-reconcile-result">
          <el-alert :title="reconcileResult.summary" :type="reconcileTone" show-icon :closable="false" />
          <div class="diag-reconcile-groups">
            <div class="diag-reconcile-group">
              <el-tag size="small" type="success">命中 {{ reconcileResult.matched.length }}</el-tag>
              <div v-for="(m, i) in reconcileResult.matched" :key="'m' + i" class="mono diag-reconcile-item">{{ m[0] }} · {{ m[1] }}</div>
            </div>
            <div class="diag-reconcile-group">
              <el-tag size="small" type="danger">缺失 {{ reconcileResult.missing.length }}</el-tag>
              <div v-for="(m, i) in reconcileResult.missing" :key="'g' + i" class="mono diag-reconcile-item">{{ m[0] }} · {{ m[1] }}</div>
            </div>
            <div class="diag-reconcile-group">
              <el-tag size="small" type="warning">预期外 {{ reconcileResult.unexpected.length }}</el-tag>
              <div v-for="(m, i) in reconcileResult.unexpected" :key="'u' + i" class="mono diag-reconcile-item">{{ m[0] }} · {{ m[1] }}</div>
            </div>
          </div>
        </div>
      </div>
      </div><!-- /diag-main -->

      <!-- ============ 右栏：拓扑常驻（诊断联动高亮） + 诊断对话 ============ -->
      <div class="diag-side">
        <div class="diag-section diag-topo-panel">
          <div class="diag-section-title">拓扑视图（{{ topoEntityCount }} 实体）</div>
          <TopologyGraph v-if="topo" :entities="topo.entities" :power-edges="topo.power_edges"
            :link-edges="topo.link_edges" :containment-edges="topo.containment_edges"
            :root="topoHighlight.root" :affected="topoHighlight.affected" />
          <el-empty v-else description="拓扑加载中" :image-size="48" />
          <div class="muted diag-topo-note">供电实线 / 链路虚线 / 包含点线；红色=疑似根因、橙色=受影响。模拟 POD（双机柜各 6 服务器双 PSU、2 TOR、双上联 spine）；将来换 UnifiedInfraTopology 真实数据，接口不变。</div>
        </div>

        <!-- 诊断对话（LLM 座位：拿诊断终态回答追问；未配置时如实提示） -->
        <div class="diag-section">
          <div class="diag-section-title">诊断对话（对最近一次诊断追问）</div>
          <div class="diag-chat">
            <div class="diag-chat-messages">
              <div v-for="(m, i) in chatMessages" :key="i" class="diag-chat-msg" :class="`diag-chat-msg-${m.role}`">
                <div class="diag-chat-bubble">{{ m.content }}</div>
              </div>
              <el-empty v-if="!chatMessages.length && !chatError" description="先跑一次诊断，然后问它：为什么是这个结论？证据是什么？下一步该查什么？"
                :image-size="48" />
              <el-alert v-if="chatError" :title="chatError" type="warning" :closable="false" />
            </div>
            <div class="diag-chat-input-row">
              <el-input v-model="chatInput" placeholder="如：为什么判定会宕机？" class="diag-chat-input"
                :disabled="chatLoading" @keyup.enter="sendChat" clearable />
              <el-button type="primary" :loading="chatLoading" :disabled="!chatInput.trim()" @click="sendChat">发送</el-button>
            </div>
            <div v-if="chatContextInjected" class="muted diag-chat-ctx">本次注入的诊断上下文（LLM 只见这些，不编造）：{{ chatContextInjected.slice(0, 120) }}…</div>
          </div>
        </div>
      </div>
    </div><!-- /diag-layout -->

    <!-- 证据出处弹窗 -->
    <el-dialog v-model="evidenceDialog" :title="`证据出处：${evidenceRef}`" width="560px">
      <div v-if="evidenceLoading" class="muted">回链查询中…</div>
      <el-alert v-else-if="evidenceError" :title="evidenceError" type="warning" :closable="false" />
      <div v-else>
        <div class="muted diag-evidence-rev">revision {{ evidenceData?.revision_id }}</div>
        <div v-for="it in evidenceData?.items ?? []" :key="it.evidence_id" class="diag-evidence-item">
          <el-tag size="small">{{ it.level }}</el-tag>
          <span class="muted">{{ it.source_kind || it.kind }}</span>
          <div class="diag-evidence-text">{{ it.summary }}</div>
        </div>
        <el-empty v-if="!evidenceData?.items?.length" description="无出处片段（知识空白如实展示）" :image-size="48" />
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ArrowRight } from '@element-plus/icons-vue'
import TopologyGraph from './TopologyGraph.vue'

// 判定序六节点（与 diagnosis/graphs/rack_power.py 对齐）
const STEPS = [
  { key: 'normalize_alert', label: '告警归一', note: 'signature → OKB lookup → 候选异常' },
  { key: 'propagate_trace', label: '传播推演', note: '沿因果链 trace，收集 unknown 条件为待验证项' },
  { key: 'cross_domain_hints', label: '跨域取证提示', note: 'same_event 对端（同柜/同链路另一面）' },
  { key: 'impact_radius', label: '影响半径', note: '因果图 × 拓扑合遍历：预期告警集（需实体 ID）' },
  { key: 'conclude', label: '结论生成', note: '主链 + 证据回链 + 待验证项' },
  { key: 'report_feedback', label: '飞轮回写', note: '判定边 report confirmed，幂等' },
] as const

// 测试样本（后端 /api/diagnosis/samples 实时读 samples.py）
interface TestSample {
  id: string
  label: string
  site: string
  equipment: string
  fault_type: string
  timestamp: string
  raw_text: string
  signature: string
  entity?: string
  expected: Record<string, unknown>
}
const samples = ref<TestSample[]>([])
const topo = ref<{ entities: Record<string, string>; power_edges: string[][]; link_edges: string[][]; containment_edges: string[][] } | null>(null)

// 风暴场景（与后端 runStorm 构造对齐）
const STORM_SCENES = [
  { kind: 'pdu', label: 'PDU-A1 路故障', note: '6 台服务器 psu1 告警' },
  { kind: 'busbar', label: '母线 busbar-A 故障', note: '12 台全告（psu1+psu2）' },
  { kind: 'cross', label: '跨柜独立告警', note: 'A 柜 + B 柜各 1 条，不硬凑根因' },
] as const
const activeStorm = ref('')

onMounted(async () => {
  try {
    const [s, t] = await Promise.all([
      fetch('/api/diagnosis/samples').then(r => r.json()),
      fetch('/api/diagnosis/topology').then(r => r.json()),
    ])
    samples.value = s.samples ?? []
    topo.value = t
  } catch {
    samples.value = []
  }
})

const topoEntityCount = computed(() => Object.keys(topo.value?.entities ?? {}).length)
const topoGroups = computed(() => [
  { label: '供电', tag: 'danger', edges: topo.value?.power_edges ?? [] },
  { label: '链路', tag: 'primary', edges: topo.value?.link_edges ?? [] },
  { label: '包含', tag: 'info', edges: topo.value?.containment_edges ?? [] },
].map(g => ({ ...g, count: g.edges.length })))

// 拓扑高亮联动：风暴根因（红）优先，否则正向诊断的起点实体；
// 受影响 = 影响半径实体 + 逻辑会话观测面端口（BGP 本端+对端）
const topoHighlight = computed<{ root: string; affected: string[] }>(() => {
  if (stormResult.value?.root_cause) {
    return { root: stormResult.value.root_cause.entity, affected: stormResult.value.affected_entities }
  }
  const radius = result.value?.impact_radius
  const root = result.value && radius && (radius.expected_alerts.length || (radius.surface ?? []).length)
    ? entityId.value.trim() : ''
  const affected = [
    ...(radius?.expected_alerts.map(a => a.entity) ?? []),
    ...(radius?.surface ?? []),
  ]
  return { root, affected }
})

function expectedLabel(exp: Record<string, unknown>): string {
  if (exp.unmapped) return '未登记'
  if (exp.knowledge_gap) return '知识空白'
  if (exp.empty_chain) return '叶子节点'
  if (exp.radius_entities) return '影响半径'
  return '金路径'
}

function expectedTone(exp: Record<string, unknown>): 'success' | 'info' | 'warning' | 'danger' {
  if (exp.unmapped) return 'info'
  if (exp.knowledge_gap || exp.empty_chain) return 'warning'
  if (exp.radius_entities) return 'danger'
  return 'success'
}

// ---------------------------------------------------------------- 对账（预期 vs 观测）
interface ReconcileResult {
  matched: [string, string][]
  missing: [string, string][]
  unexpected: [string, string][]
  summary: string
}
const observedText = ref('')
const reconcileResult = ref<ReconcileResult | null>(null)
const reconciling = ref(false)
const reconcileTone = computed(() =>
  reconcileResult.value?.missing.length || reconcileResult.value?.unexpected.length ? 'warning' : 'success')

async function runReconcile() {
  if (reconciling.value || !result.value) return
  const observed = observedText.value.split('\n').map(l => l.trim()).filter(Boolean)
    .map(l => {
      const [entity, stable_code] = l.split(',').map(s => s.trim())
      return { entity, stable_code: stable_code ?? '' }
    })
  if (!observed.length) {
    errorMsg.value = '请先填写观测告警（每行：实体, 异常类型）'
    return
  }
  reconciling.value = true
  try {
    const resp = await fetch('/api/diagnosis/reconcile', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        alert_signature: alertSignature.value.trim(),
        entity_id: entityId.value.trim(),
        observed,
      }),
    })
    if (!resp.ok) throw new Error(`对账服务异常（HTTP ${resp.status}）`)
    reconcileResult.value = await resp.json()
  } catch (e) {
    reconcileResult.value = null
    errorMsg.value = e instanceof Error ? e.message : String(e)
  } finally {
    reconciling.value = false
  }
}

// ---------------------------------------------------------------- 证据回链（evidence 原语出口）
interface EvidenceItem {
  evidence_id: string
  kind: string
  level: string
  source_kind?: string
  summary: string
}
interface EvidenceResponse {
  revision_id: string
  items: EvidenceItem[]
}
const evidenceDialog = ref(false)
const evidenceRef = ref('')
const evidenceLoading = ref(false)
const evidenceError = ref('')
const evidenceData = ref<EvidenceResponse | null>(null)

async function showEvidence(ref: string) {
  evidenceRef.value = ref
  evidenceDialog.value = true
  evidenceLoading.value = true
  evidenceError.value = ''
  evidenceData.value = null
  try {
    const resp = await fetch(`/api/diagnosis/evidence?ref=${encodeURIComponent(ref)}`)
    if (!resp.ok) throw new Error(`证据回链失败（HTTP ${resp.status}）`)
    evidenceData.value = await resp.json()
  } catch (e) {
    evidenceError.value = e instanceof Error ? e.message : String(e)
  } finally {
    evidenceLoading.value = false
  }
}

// ---------------------------------------------------------------- 诊断对话（LLM 座位）
interface ChatMessage { role: 'user' | 'assistant'; content: string }
const chatMessages = ref<ChatMessage[]>([])
const chatInput = ref('')
const chatLoading = ref(false)
const chatError = ref('')
const chatContextInjected = ref('')

async function sendChat() {
  const question = chatInput.value.trim()
  if (!question || chatLoading.value) return
  chatError.value = ''
  chatInput.value = ''
  chatMessages.value = [...chatMessages.value, { role: 'user', content: question }]
  chatLoading.value = true
  try {
    // 历史取最近 8 条（控上下文长度）
    const history = chatMessages.value.slice(-9, -1)
      .map(m => ({ role: m.role, content: m.content }))
    const resp = await fetch('/api/diagnosis/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question, history }),
    })
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({ detail: `HTTP ${resp.status}` }))
      throw new Error(err.detail ?? `HTTP ${resp.status}`)
    }
    const data = await resp.json()
    chatMessages.value = [...chatMessages.value, { role: 'assistant', content: data.answer }]
    chatContextInjected.value = data.context_injected ?? ''
  } catch (e) {
    chatError.value = e instanceof Error ? e.message : String(e)
    // 失败的提问不留在历史里，用户可重发
    chatMessages.value = chatMessages.value.slice(0, -1)
    chatInput.value = question
  } finally {
    chatLoading.value = false
  }
}

function applySample(row: TestSample) {
  activeStorm.value = ''            // 正反向场景互斥：切单告警清风暴态
  stormResult.value = null
  alertSignature.value = row.signature
  entityId.value = row.entity ?? ''
  // 点场景即跑：载入并立即诊断（旧结果清空，避免误读为"点什么都一样"）
  void run()
}

// ---------------------------------------------------------------- 告警风暴根因反推
interface StormResult {
  independent: boolean
  root_cause: { entity: string; entity_type: string; stable_code: string; verify: string } | null
  affected_entities: string[]
  alert_count?: number
  rationale: string
  conclusion?: { summary?: string; rationale?: string }
}
const stormResult = ref<StormResult | null>(null)
const stormLoading = ref('')

async function runStorm(kind: 'pdu' | 'busbar' | 'cross') {
  stormLoading.value = kind
  result.value = null               // 互斥：切风暴清正向结果
  started.value = false
  reconcileResult.value = null
  activeStorm.value = kind
  try {
    // 按场景构造告警风暴（真实接入时这就是告警流推送的形态）
    let entities: string[]
    if (kind === 'pdu') {
      entities = [1, 2, 3, 4, 5, 6].map(i => `srv-${String(i).padStart(3, '0')}:psu1`)
    } else if (kind === 'busbar') {
      entities = [1, 2, 3, 4, 5, 6].flatMap(i => [`srv-${String(i).padStart(3, '0')}:psu1`, `srv-${String(i).padStart(3, '0')}:psu2`])
    } else {
      entities = ['srv-001:psu1', 'srv-101:psu1']
    }
    const resp = await fetch('/api/diagnosis/storm', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ alerts: entities.map(e => ({ entity: e, stable_code: 'server.psu.input_lost' })) }),
    })
    if (!resp.ok) throw new Error(`风暴诊断服务异常（HTTP ${resp.status}）`)
    stormResult.value = await resp.json()
  } catch (e) {
    stormResult.value = null
    errorMsg.value = e instanceof Error ? e.message : String(e)
  } finally {
    stormLoading.value = ''
  }
}

// 设备的可读位置：fixture 实体（具体设备）优先，否则站点
function deviceOf(row: TestSample): string {
  return row.entity ?? row.site
}

// 部件的中文可读名（编码 → 领域语言）
const EQUIPMENT_NAMES: Record<string, string> = {
  'server.psu': '服务器电源',
  'power.psu': '动环电源',
  'power.battery': 'UPS 电池',
  'power.busbar': '机柜母线',
  'cooling.chilled_water_pump': '冷冻水泵',
  'net.optical': '光模块',
  'net.bgp': 'BGP 会话',
  'net.link': '网络链路',
  'net.port': '网络端口',
  'gpu': 'GPU',
  'storage.disk': '硬盘',
}

function faultOf(row: TestSample): string {
  return EQUIPMENT_NAMES[row.equipment] ?? row.equipment
}

// 诊断结果结构（与 diagnosis/api.py done 事件对齐）
interface ChainHop { target: string; evidence_level: string; revision_id: string }
interface DiagnosisConclusion {
  status: string
  start_code?: string
  severity?: string
  clock_hint?: string
  summary?: string
  primary_chain?: ChainHop[]
  error?: string
}
interface DiagnosisResult {
  conclusion: DiagnosisConclusion
  reported: boolean
  candidates: Array<{ anomaly_id: string; stable_code: string; name: string; status: string; severity_online?: string; clock_hint?: string }>
  pending_checks: Array<{ fact_key: string; op: string; edge_id: string }>
  cross_domain_peers: Array<{ kind: string; other_code: string; other_severity?: string }>
  impact_radius?: { start_entity: string; surface?: string[]; expected_alerts: Array<{ entity: string; stable_code: string; edge_id: string }>; unbound: Array<{ stable_code: string; reason: string }> }
}

const alertSignature = ref('SERVER-PSU-INPUT-LOST')
const entityId = ref('srv-001')
const running = ref(false)
const started = ref(false)
const errorMsg = ref('')
const doneNodes = ref<string[]>([])
const result = ref<DiagnosisResult | null>(null)

const conclusion = computed<DiagnosisConclusion>(() => result.value?.conclusion ?? { status: 'none' })

// 影响半径按异常类型分组展示（每组 = 因果图一个目标类型 × 拓扑展开的实体集）
const radiusGroups = computed<Record<string, { entities: string[]; edgeId: string }>>(() => {
  const alerts = result.value?.impact_radius?.expected_alerts ?? []
  const groups: Record<string, { entities: string[]; edgeId: string }> = {}
  for (const a of alerts) {
    if (!groups[a.stable_code]) groups[a.stable_code] = { entities: [], edgeId: a.edge_id }
    if (!groups[a.stable_code].entities.includes(a.entity)) groups[a.stable_code].entities.push(a.entity)
  }
  return groups
})

function stepState(key: string): string {
  if (doneNodes.value.includes(key)) return 'done'
  if (running.value) return 'pending'
  return 'idle'
}

function stepNote(key: string): string {
  const step = STEPS.find(s => s.key === key)
  return step?.note ?? ''
}

async function run() {
  if (!alertSignature.value.trim() || running.value) return
  running.value = true
  started.value = true
  errorMsg.value = ''
  doneNodes.value = []
  result.value = null
  try {
    // SSE：fetch 流式读取 event/data 帧
    const resp = await fetch('/api/diagnosis/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        alert_signature: alertSignature.value.trim(),
        entity_id: entityId.value.trim() || null,
      }),
    })
    if (!resp.ok || !resp.body) throw new Error(`诊断服务响应异常（HTTP ${resp.status}）`)
    const reader = resp.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      // SSE 帧以空行分隔
      let idx: number
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const frame = buf.slice(0, idx)
        buf = buf.slice(idx + 2)
        const evName = /^event: (.+)$/m.exec(frame)?.[1]
        const dataRaw = /^data: (.+)$/m.exec(frame)?.[1]
        if (!evName || !dataRaw) continue
        const payload = JSON.parse(dataRaw)
        if (evName === 'node_update') {
          doneNodes.value = [...doneNodes.value, payload.node]
        } else if (evName === 'done') {
          result.value = payload as DiagnosisResult
        } else if (evName === 'error') {
          errorMsg.value = payload.message
        }
      }
    }
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e)
  } finally {
    running.value = false
  }
}
</script>

<style scoped>
.diag-console { display: flex; flex-direction: column; gap: 16px; }
/* 左右分栏：左=主工作流（选场景/诊断/结果），右=拓扑常驻联动 */
.diag-layout { display: grid; grid-template-columns: minmax(480px, 1.1fr) minmax(380px, 0.9fr); gap: 16px; align-items: start; }
.diag-main { display: flex; flex-direction: column; gap: 14px; min-width: 0; }
.diag-side { position: sticky; top: 12px; }
.diag-topo-panel { margin-bottom: 0; }

/* 场景选择：卡片按钮网格 */
.diag-scene-group-label { font-size: 12px; color: #8a94a6; margin: 10px 0 6px; }
.diag-scene-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 8px; }
.diag-scene-btn { display: flex; flex-direction: column; align-items: flex-start; gap: 3px;
  padding: 8px 10px; border: 1px solid #dfe6ef; border-radius: 8px; background: #fff;
  cursor: pointer; text-align: left; transition: border-color .15s, box-shadow .15s; }
.diag-scene-btn:hover { border-color: #8fb7e8; }
.diag-scene-btn.active { border-color: #409eff; box-shadow: 0 0 0 2px rgba(64,158,255,.15); background: #f0f7ff; }
.diag-scene-btn-storm.active { border-color: #f56c6c; box-shadow: 0 0 0 2px rgba(245,108,108,.15); background: #fef4f4; }
.diag-scene-btn-label { font-size: 13px; font-weight: 600; color: #303133; }
.diag-scene-btn-sub { font-size: 11px; }
.diag-scene-btn-tag { align-self: flex-start; }

.diag-input-row { display: flex; gap: 10px; align-items: center; }
.diag-input { flex: 1; max-width: 340px; }
.diag-input-entity { max-width: 240px; }
.diag-topo-note { font-size: 11px; margin-top: 6px; }
.diag-storm-btns { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 10px; }
.diag-storm-root { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-bottom: 8px; }
.diag-storm-root-entity { font-weight: 700; color: #c45656; }
.diag-storm-affected { display: flex; gap: 4px; flex-wrap: wrap; align-items: center; margin-bottom: 6px; }
.diag-storm-rationale { font-size: 12px; color: #8a94a6; margin: 0; }
.diag-reconcile-row { display: flex; gap: 10px; align-items: flex-start; }
.diag-reconcile-input { flex: 1; }
.diag-reconcile-groups { display: flex; gap: 14px; margin-top: 8px; }
.diag-reconcile-group { flex: 1; min-width: 0; }
.diag-reconcile-item { font-size: 11px; color: #4a5568; margin: 2px 0; }
.diag-evidence-item { margin: 8px 0; }
.diag-evidence-text { font-size: 12px; color: #4a5568; margin-top: 2px; word-break: break-all; }
.diag-evidence-rev { margin-bottom: 6px; }

/* 诊断对话 */
.diag-chat { display: flex; flex-direction: column; gap: 8px; }
.diag-chat-messages { display: flex; flex-direction: column; gap: 6px; max-height: 260px; overflow-y: auto; padding: 4px 0; }
.diag-chat-msg { display: flex; }
.diag-chat-msg-user { justify-content: flex-end; }
.diag-chat-bubble { max-width: 85%; padding: 6px 10px; border-radius: 8px; font-size: 12px; line-height: 1.5; word-break: break-word; white-space: pre-wrap; }
.diag-chat-msg-user .diag-chat-bubble { background: #ecf5ff; color: #303133; border: 1px solid #d9ecff; }
.diag-chat-msg-assistant .diag-chat-bubble { background: #f5f7fa; color: #303133; border: 1px solid #e4e7ed; }
.diag-chat-input-row { display: flex; gap: 8px; }
.diag-chat-input { flex: 1; }
.diag-chat-ctx { font-size: 11px; }
.diag-radius-group { margin-bottom: 8px; padding: 6px 8px; border: 1px dashed #dfe6ef; border-radius: 6px; }
.diag-radius-code { display: flex; gap: 8px; align-items: center; margin-bottom: 4px; }
.diag-radius-entities { display: flex; gap: 4px; flex-wrap: wrap; }
.diag-radius-entity { font-family: var(--el-font-family-mono, monospace); }
.diag-steps { display: flex; flex-wrap: wrap; gap: 10px; }
.diag-step { display: flex; gap: 8px; align-items: flex-start; padding: 8px 12px; border: 1px solid #e4e9f0; border-radius: 8px; background: #fafbfd; min-width: 200px; }
.diag-step.done { border-color: #b3d8c5; background: #f0f9f4; }
.diag-step.pending { border-color: #d79b35; background: #fdf8ef; }
.diag-step-dot { width: 8px; height: 8px; border-radius: 50%; background: #c8d0dc; margin-top: 6px; }
.diag-step.done .diag-step-dot { background: #38a078; }
.diag-step.pending .diag-step-dot { background: #d79b35; }
.diag-step-name { font-weight: 600; font-size: 13px; }
.diag-step-note { font-size: 11px; color: #8a94a6; }
.diag-result { display: flex; flex-direction: column; gap: 14px; }
.diag-section { border: 1px solid #e4e9f0; border-radius: 10px; padding: 12px 14px; background: #fff; }
.diag-section-title { font-weight: 600; margin-bottom: 8px; }
.diag-candidate, .diag-check { display: flex; gap: 8px; align-items: center; margin-bottom: 6px; flex-wrap: wrap; }
.diag-chain { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.diag-chain-node { padding: 4px 8px; border: 1px solid #dfe6ef; border-radius: 6px; background: #fafbfd; cursor: pointer; }
.diag-chain-node:hover { border-color: #4f9ee8; }
.diag-arrow { color: #8a94a6; }
.diag-chain-hint { font-size: 12px; margin-top: 6px; }
.diag-error { margin-top: 4px; }
</style>
