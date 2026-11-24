<template>
  <div class="diag-console">
    <!-- 测试数据概览：样本集 + 机柜连接 -->
    <div class="diag-testdata-grid">
      <div class="diag-section">
        <div class="diag-section-title">测试样本集（{{ samples.length }} 条，来自 samples.py）</div>
        <el-table :data="samples" size="small" class="diag-sample-table" @row-click="applySample">
          <el-table-column label="故障场景（设备 · 故障）" min-width="230">
            <template #default="{ row }">
              <div class="diag-sample-label">{{ row.label }}</div>
              <div class="muted diag-sample-sub mono">{{ deviceOf(row) }} · {{ faultOf(row) }}</div>
            </template>
          </el-table-column>
          <el-table-column label="归一 signature" min-width="190">
            <template #default="{ row }"><span class="mono">{{ row.signature }}</span></template>
          </el-table-column>
          <el-table-column label="期望分支" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="expectedTone(row.expected)">{{ expectedLabel(row.expected) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column width="60" align="center">
            <template #default><el-button link type="primary" size="small">诊断</el-button></template>
          </el-table-column>
        </el-table>
      </div>
      <div class="diag-section">
        <div class="diag-section-title">机柜连接关系（fixture 拓扑，{{ topoEntityCount }} 实体）</div>
        <div v-for="group in topoGroups" :key="group.label" class="diag-topo-group">
          <div class="diag-topo-label">
            <el-tag size="small" :type="group.tag">{{ group.label }}</el-tag>
            <span class="muted">{{ group.count }} 条</span>
          </div>
          <div class="diag-topo-edges">
            <div v-for="(e, i) in group.edges" :key="i" class="diag-topo-edge mono">
              {{ e[0] }} <el-icon class="diag-topo-arrow"><ArrowRight /></el-icon> {{ e[1] }}
            </div>
          </div>
        </div>
        <div class="muted diag-topo-note">模拟 POD：双机柜（A12/B13 各 6 服务器，双 PSU 双路），每柜 2 TOR，TOR 双上联 spine-1/2；将来换 UnifiedInfraTopology 真实数据，接口不变。</div>
      </div>
    </div>

    <!-- 拓扑可视化（诊断联动高亮：红=疑似根因 / 橙=受影响） -->
    <div class="diag-section">
      <div class="diag-section-title">拓扑视图（{{ topoEntityCount }} 实体；供电实线 / 链路虚线；诊断后红色=根因、橙色=受影响）</div>
      <TopologyGraph v-if="topo" :entities="topo.entities" :power-edges="topo.power_edges"
        :link-edges="topo.link_edges" :containment-edges="topo.containment_edges"
        :root="topoHighlight.root" :affected="topoHighlight.affected" />
      <el-empty v-else description="拓扑加载中" :image-size="48" />
    </div>

    <!-- 告警风暴 → 根因反推（同柜多服务器 PSU 告警 → 上层故障） -->
    <div class="diag-section">
      <div class="diag-section-title">告警风暴 → 根因反推（多台服务器 PSU 告警 ⇒ 上层 PDU/母线故障）</div>
      <div class="diag-storm-btns">
        <el-button size="small" type="danger" plain :loading="stormLoading === 'pdu'" @click="runStorm('pdu')">
          模拟 PDU-A1 路故障（6 台 psu1 告警）
        </el-button>
        <el-button size="small" type="danger" :loading="stormLoading === 'busbar'" @click="runStorm('busbar')">
          模拟母线 busbar-A 故障（12 台全告）
        </el-button>
        <el-button size="small" type="info" plain :loading="stormLoading === 'cross'" @click="runStorm('cross')">
          模拟跨柜独立告警（不硬凑根因）
        </el-button>
      </div>
      <div v-if="stormResult" class="diag-storm-result">
        <template v-if="stormResult.root_cause">
          <div class="diag-storm-root">
            <el-tag type="danger">疑似根因</el-tag>
            <span class="mono diag-storm-root-entity">{{ stormResult.root_cause.entity }}</span>
            <span class="mono">{{ stormResult.root_cause.stable_code }}</span>
            <el-tag size="small" :type="stormResult.root_cause.verify === 'knowledge_backed' ? 'success' : 'warning'">
              {{ stormResult.root_cause.verify === 'knowledge_backed' ? 'OKB 验证通过' : '拓扑推断（知识空白）' }}
            </el-tag>
          </div>
          <div class="diag-storm-affected">
            <span class="muted">{{ stormResult.alert_count }} 条告警实体：</span>
            <el-tag v-for="e in stormResult.affected_entities" :key="e" size="small" class="mono">{{ e }}</el-tag>
          </div>
          <p class="diag-storm-rationale">{{ stormResult.rationale }}</p>
        </template>
        <el-alert v-else :title="stormResult.rationale" type="info" :closable="false" />
      </div>
    </div>

    <!-- 输入区 -->
    <div class="diag-input-row">
      <el-input v-model="alertSignature" placeholder="告警 signature，如 SERVER-PSU-INPUT-LOST / T30alertPsuFault"
        class="diag-input" @keyup.enter="run" clearable />
      <el-input v-model="entityId" placeholder="实体 ID（可选，填了算影响半径）"
        class="diag-input-entity" @keyup.enter="run" clearable />
      <el-button type="primary" :loading="running" @click="run">开始诊断</el-button>
    </div>

    <!-- 判定序执行流（SSE 逐节点点亮） -->
    <div v-if="started" class="diag-steps">
      <div v-for="step in STEPS" :key="step.key" class="diag-step" :class="stepState(step.key)">
        <i class="diag-step-dot" />
        <div>
          <div class="diag-step-name">{{ step.label }}</div>
          <div class="diag-step-note">{{ stepNote(step.key) }}</div>
        </div>
      </div>
    </div>

    <el-empty v-if="started && !running && !result && !errorMsg" description="无结果" :image-size="60" />

    <el-alert v-if="errorMsg" :title="errorMsg" type="error" :closable="false" class="diag-error" />

    <!-- 结果区 -->
    <div v-if="result" class="diag-result">
      <!-- 一句话结论（规则版总结） -->
      <el-alert v-if="conclusion.summary" :title="conclusion.summary" type="warning" show-icon :closable="false" />

      <!-- 归一候选 -->
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
            </span>
          </template>
        </div>
        <div class="muted diag-chain-hint">点击节点可跳转知识图谱；证据可回链至 OKB 出处。</div>
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
      <div class="diag-section" v-if="radiusGroups.length">
        <div class="diag-section-title">
          影响半径（{{ result.impact_radius?.expected_alerts?.length ?? 0 }} 条预期告警，起点实体 {{ result.impact_radius?.start_entity }}）
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
      <div class="diag-section">
        <div class="diag-section-title">飞轮回写</div>
        <el-tag :type="result.reported ? 'success' : 'info'">
          {{ result.reported ? '已回写 confirmed（置信度已更新）' : '未回写（无链或知识空白）' }}
        </el-tag>
      </div>
    </div>
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

// 拓扑高亮联动：风暴根因（红）优先，否则正向诊断的起点实体；受影响 = 影响半径/风暴告警实体
const topoHighlight = computed<{ root: string; affected: string[] }>(() => {
  if (stormResult.value?.root_cause) {
    return { root: stormResult.value.root_cause.entity, affected: stormResult.value.affected_entities }
  }
  const radius = result.value?.impact_radius
  const root = result.value && radius && radius.expected_alerts.length ? entityId.value.trim() : ''
  return { root, affected: radius?.expected_alerts.map(a => a.entity) ?? [] }
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

function applySample(row: TestSample) {
  alertSignature.value = row.signature
  entityId.value = row.entity ?? ''
  // 点样本行即跑：载入并立即诊断（旧结果清空，避免误读为"点什么都一样"）
  void run()
}

// ---------------------------------------------------------------- 告警风暴根因反推
interface StormResult {
  independent: boolean
  root_cause: { entity: string; entity_type: string; stable_code: string; verify: string } | null
  affected_entities: string[]
  alert_count?: number
  rationale: string
}
const stormResult = ref<StormResult | null>(null)
const stormLoading = ref('')

async function runStorm(kind: 'pdu' | 'busbar' | 'cross') {
  stormLoading.value = kind
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
  impact_radius?: { start_entity: string; expected_alerts: Array<{ entity: string; stable_code: string; edge_id: string }>; unbound: Array<{ stable_code: string; reason: string }> }
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
.diag-testdata-grid { display: grid; grid-template-columns: 1.4fr 1fr; gap: 14px; align-items: start; }
.diag-input-row { display: flex; gap: 10px; align-items: center; }
.diag-input { max-width: 320px; }
.diag-input-entity { max-width: 220px; }
.diag-sample-table { cursor: pointer; }
.diag-sample-label { font-weight: 600; font-size: 13px; }
.diag-sample-sub { font-size: 11px; }
.diag-topo-group { margin-bottom: 8px; }
.diag-topo-label { display: flex; gap: 8px; align-items: center; margin-bottom: 4px; }
.diag-topo-edges { display: flex; flex-direction: column; gap: 2px; max-height: 180px; overflow-y: auto; }
.diag-topo-edge { font-size: 11px; color: #4a5568; display: flex; align-items: center; gap: 4px; }
.diag-topo-arrow { color: #8a94a6; font-size: 10px; }
.diag-topo-note { font-size: 11px; margin-top: 6px; }
.diag-storm-btns { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 10px; }
.diag-storm-root { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-bottom: 8px; }
.diag-storm-root-entity { font-weight: 700; color: #c45656; }
.diag-storm-affected { display: flex; gap: 4px; flex-wrap: wrap; align-items: center; margin-bottom: 6px; }
.diag-storm-rationale { font-size: 12px; color: #8a94a6; margin: 0; }
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
