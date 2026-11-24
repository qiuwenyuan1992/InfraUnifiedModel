<template>
  <div class="diag-console">
    <!-- 输入区 -->
    <div class="diag-input-row">
      <el-input v-model="alertSignature" placeholder="告警 signature，如 SERVER-PSU-INPUT-LOST / T30alertPsuFault"
        class="diag-input" @keyup.enter="run" clearable />
      <el-input v-model="entityId" placeholder="实体 ID（可选，填了算影响半径）"
        class="diag-input-entity" @keyup.enter="run" clearable />
      <el-button type="primary" :loading="running" @click="run">开始诊断</el-button>
      <el-select v-model="pickedSample" placeholder="载入样本" class="diag-sample-select" @change="onPickSample">
        <el-option v-for="s in DEMO_SAMPLES" :key="s.sig" :label="s.label" :value="s.sig" />
      </el-select>
    </div>
    <div class="diag-input-row diag-samples-row">
      <span class="muted">模拟样本（告警=部位+类型+时间点；真库实时推演，样本文件在 UnifiedInfraDiagnosis/diagnosis/samples.py）：</span>
      <el-tag v-for="s in DEMO_SAMPLES" :key="s.sig" class="diag-sample-tag" :type="s.hl ? 'danger' : 'info'"
        effect="plain" @click="loadSample(s)">{{ s.label }}</el-tag>
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
import { computed, ref } from 'vue'
import { ArrowRight } from '@element-plus/icons-vue'

// 判定序五节点（与 diagnosis/graphs/rack_power.py 对齐）
const STEPS = [
  { key: 'normalize_alert', label: '告警归一', note: 'signature → OKB lookup → 候选异常' },
  { key: 'propagate_trace', label: '传播推演', note: '沿因果链 trace，收集 unknown 条件为待验证项' },
  { key: 'cross_domain_hints', label: '跨域取证提示', note: 'same_event 对端（同柜/同链路另一面）' },
  { key: 'impact_radius', label: '影响半径', note: '因果图 × 拓扑合遍历：预期告警集（需实体 ID）' },
  { key: 'conclude', label: '结论生成', note: '主链 + 证据回链 + 待验证项' },
  { key: 'report_feedback', label: '飞轮回写', note: '判定边 report confirmed，幂等' },
] as const

// 演示样本（与 samples.py 的 7 条对齐；hl=告警风暴场景）
const DEMO_SAMPLES = [
  { label: '掉电金路径', sig: 'SERVER-PSU-INPUT-LOST', entity: 'srv-001', hl: false },
  { label: '供电树风暴(busbar)', sig: 'ALERT-BUSBAR-INPUT-LOST', entity: 'busbar-A', hl: true },
  { label: '制冷5跳深级联', sig: 'ALERT-CHWP-FAULT', entity: '', hl: false },
  { label: '光衰→CRC', sig: 'ALERT-OPTICAL-RX-LOW', entity: '', hl: false },
  { label: 'GPU Xid 154', sig: 'GPU_XID_154', entity: '', hl: false },
  { label: 'BGP邻居Down(叶子)', sig: 'ALERT-BGP-PEER-DOWN', entity: '', hl: false },
  { label: 'T30 PSU(知识空白)', sig: 'T30alertPsuFault', entity: '', hl: false },
  { label: '未登记告警', sig: 'DISK-MEDIA-ERROR-UNREGISTERED', entity: '', hl: false },
] as const

// 诊断结果结构（与 diagnosis/api.py done 事件对齐）
interface ChainHop { target: string; evidence_level: string; revision_id: string }
interface DiagnosisConclusion {
  status: string
  start_code?: string
  severity?: string
  clock_hint?: string
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
const pickedSample = ref('')
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

function loadSample(s: { label: string; sig: string; entity: string }) {
  alertSignature.value = s.sig
  entityId.value = s.entity
}

function onPickSample(sig: string) {
  const s = DEMO_SAMPLES.find(x => x.sig === sig)
  if (s) loadSample(s)
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
.diag-input-row { display: flex; gap: 10px; align-items: center; }
.diag-input { max-width: 320px; }
.diag-input-entity { max-width: 220px; }
.diag-sample-select { width: 160px; }
.diag-samples-row { flex-wrap: wrap; }
.diag-sample-tag { cursor: pointer; }
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
