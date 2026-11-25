<template>
  <div class="agui-console">
    <div class="agui-layout">
      <!-- 左：运行与事件流 -->
      <div class="agui-main">
        <div class="agui-section">
          <div class="agui-section-title">AG-UI 协议试点：探索式诊断 → 标准事件流</div>
          <p class="muted agui-intro">
            后端 <span class="mono">POST /api/diagnosis/agui</span> 输出 AG-UI 1.0 标准事件（RunStarted →
            ToolCall 四件套 → Custom 拓扑动作 → TextMessage 结论 → RunFinished）；
            前端用 <span class="mono">@ag-ui/client</span> 的 HttpAgent 订阅——换任何支持 AG-UI 的
            前端组件（CopilotKit 等）即可直接复用同一后端。
          </p>
          <div class="agui-input-row">
            <el-select v-model="pickedSample" placeholder="选择测试场景" class="agui-sample-select"
              @change="onPickSample" clearable>
              <el-option v-for="s in samples" :key="s.id" :label="`${s.label}（${s.entity ?? s.site}）`" :value="s.id" />
            </el-select>
            <el-input v-model="signature" placeholder="告警 signature" class="agui-input" clearable />
            <el-input v-model="entityId" placeholder="实体 ID（可选）" class="agui-input-entity" clearable />
            <el-button type="primary" :loading="running" @click="run">Run Agent</el-button>
          </div>
        </div>

        <!-- Run 生命周期 -->
        <div class="agui-section" v-if="runState">
          <div class="agui-section-title">
            Run 状态：
            <el-tag size="small" :type="runState === 'RUN_FINISHED' ? 'success' : 'primary'">{{ runState }}</el-tag>
            <span class="muted mono agui-ids">thread {{ threadId.slice(0, 8) }}… / run {{ runId.slice(0, 8) }}…</span>
          </div>
        </div>

        <!-- 工具调用时间线 -->
        <div class="agui-section" v-if="toolCalls.length">
          <div class="agui-section-title">Agent 工具调用（TOOL_CALL 事件族）</div>
          <div v-for="tc in toolCalls" :key="tc.id" class="agui-toolcall" :class="{ running: !tc.result }">
            <span class="agui-idx">{{ tc.order }}</span>
            <el-tag size="small" :type="toolTone(tc.name)">{{ tc.name }}</el-tag>
            <span class="mono agui-args">{{ tc.args }}</span>
            <span v-if="tc.result" class="muted agui-result">{{ tc.result }}</span>
            <span v-else class="agui-spin">▍</span>
          </div>
        </div>

        <!-- 剪枝（CUSTOM pruned） -->
        <div class="agui-section" v-if="pruned.length">
          <div class="agui-section-title">剪枝（CUSTOM 事件）</div>
          <el-tag v-for="p in pruned" :key="p" type="danger" class="mono agui-pruned">{{ p }} ✕已剪枝</el-tag>
        </div>

        <!-- 结论（TEXT_MESSAGE 事件族） -->
        <div class="agui-section" v-if="answer || streamingAnswer">
          <div class="agui-section-title">结论（TEXT_MESSAGE 事件族）</div>
          <div class="agui-answer">{{ answer || streamingAnswer }}<span v-if="streamingAnswer" class="agui-cursor" /></div>
        </div>

        <el-alert v-if="error" :title="error" type="error" :closable="false" />
      </div>

      <!-- 右：拓扑联动（CUSTOM topo_action 驱动） -->
      <div class="agui-side">
        <div class="agui-section">
          <div class="agui-section-title">拓扑联动（CUSTOM topo_action → 前端渲染）</div>
          <TopologyGraph v-if="topo" :entities="topo.entities" :power-edges="topo.power_edges"
            :link-edges="topo.link_edges" :containment-edges="topo.containment_edges"
            :root="highlight.root" :affected="[]"
            :examined="highlight.examined" :evidence="highlight.evidence" />
          <el-empty v-else description="拓扑加载中" :image-size="48" />
          <div class="muted agui-topo-note">蓝框脉冲 = Agent 正在查询；绿框 = 取证证据实体（TOOL_CALL verify_fact 的 CUSTOM 附带）。</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { HttpAgent } from '@ag-ui/client'
import TopologyGraph from './TopologyGraph.vue'

// AG-UI 试点页：HttpAgent 订阅标准事件族，渲染探索式诊断全过程
interface TestSample {
  id: string; label: string; site: string; signature: string; entity?: string
}
const samples = ref<TestSample[]>([])
const pickedSample = ref('')
const signature = ref('SERVER-PSU-INPUT-LOST')
const entityId = ref('srv-001')

function onPickSample(id: string) {
  const s = samples.value.find(x => x.id === id)
  if (!s) return
  signature.value = s.signature
  entityId.value = s.entity ?? ''
}

const running = ref(false)
const error = ref('')
const runState = ref('')
const threadId = ref('')
const runId = ref('')

interface ToolCallUi { id: string; order: number; name: string; args: string; result: string }
const toolCalls = ref<ToolCallUi[]>([])
const pruned = ref<string[]>([])
const answer = ref('')
const streamingAnswer = ref('')

const topo = ref<{ entities: Record<string, string>; power_edges: string[][]; link_edges: string[][]; containment_edges: string[][] } | null>(null)
const highlight = ref<{ root: string; examined: string[]; evidence: string[] }>({ root: '', examined: [], evidence: [] })

onMounted(async () => {
  try {
    const [t, s] = await Promise.all([
      fetch('/api/diagnosis/topology').then(r => r.json()),
      fetch('/api/diagnosis/samples').then(r => r.json()),
    ])
    topo.value = t
    samples.value = s.samples ?? []
  } catch { /* 加载失败如实展示空态 */ }
})

function asText(content: string | Array<{ type: string; text?: string }>): string {
  return typeof content === 'string' ? content : JSON.stringify(content)
}

function brief(content: string | Array<{ type: string; text?: string }>): string {
  const text = asText(content)
  try {
    const d = JSON.parse(text)
    if (d.candidates) return `→ ${(d.candidates[0]?.stable_code ?? '?')}`
    if (d.chains) return `→ ${d.chains.length} 条链${d.pending_checks?.length ? '，待取证：' + d.pending_checks.join('、') : ''}`
    if ('value' in d && 'fact_key' in d) return `→ ${d.fact_key} = ${d.value}`
    if (d.accepted) return '→ 已提交'
    return `→ ${text.slice(0, 60)}`
  } catch {
    return `→ ${text.slice(0, 60)}`
  }
}

function toolTone(name: string): 'success' | 'primary' | 'warning' | 'info' {
  if (name === 'verify_fact') return 'success'
  if (name === 'submit_conclusion') return 'warning'
  if (name === 'lookup_alert') return 'primary'
  return 'info'
}

async function run() {
  if (!signature.value.trim() || running.value) return
  running.value = true
  error.value = ''
  runState.value = ''
  toolCalls.value = []
  pruned.value = []
  answer.value = ''
  streamingAnswer.value = ''
  highlight.value = { root: entityId.value.trim(), examined: [], evidence: [] }
  try {
    // HttpAgent 参数不含 state——每次 run 用 initialState 新建（AG-UI 状态在 agent 侧）
    const runAgent = new HttpAgent({
      url: '/api/diagnosis/agui',
      initialState: { alert_signature: signature.value.trim(), entity_id: entityId.value.trim() } as Record<string, unknown>,
    })
    runAgent.addMessage({
      id: crypto.randomUUID(), role: 'user', content: signature.value.trim(),
    })
    await runAgent.runAgent({}, {
        onRunStartedEvent: ({ event }) => {
          runState.value = 'RUN_STARTED'
          threadId.value = event.threadId
          runId.value = event.runId
        },
        onToolCallStartEvent: ({ event }) => {
          toolCalls.value = [...toolCalls.value, {
            id: event.toolCallId, order: toolCalls.value.length + 1,
            name: event.toolCallName, args: '', result: '',
          }]
        },
        onToolCallArgsEvent: ({ event }) => {
          const tc = toolCalls.value.find(t => t.id === event.toolCallId)
          if (tc) tc.args = event.delta
        },
        onToolCallResultEvent: ({ event }) => {
          const tc = toolCalls.value.find(t => t.id === event.toolCallId)
          if (tc) tc.result = brief(event.content)
        },
        onCustomEvent: ({ event }) => {
          if (event.name === 'topo_action') {
            const v = event.value as { examined?: string[]; evidence?: string[] }
            highlight.value = {
              ...highlight.value,
              examined: v.examined ?? [],
              evidence: v.evidence ?? [],
            }
          } else if (event.name === 'pruned') {
            pruned.value = event.value as string[]
          }
        },
        onTextMessageContentEvent: ({ event }) => {
          streamingAnswer.value += event.delta
        },
        onTextMessageEndEvent: () => {
          answer.value = streamingAnswer.value
          streamingAnswer.value = ''
        },
        onRunFinishedEvent: () => {
          runState.value = 'RUN_FINISHED'
        },
        onRunErrorEvent: ({ event }) => {
          error.value = event.message
        },
      },
    )
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    running.value = false
  }
}
</script>

<style scoped>
.agui-console { display: flex; flex-direction: column; gap: 16px; }
.agui-layout { display: grid; grid-template-columns: minmax(440px, 1.1fr) minmax(360px, 0.9fr); gap: 16px; align-items: start; }
.agui-main { display: flex; flex-direction: column; gap: 12px; min-width: 0; }
.agui-side { position: sticky; top: 12px; }
.agui-section { border: 1px solid #e4e9f0; border-radius: 10px; padding: 12px 14px; background: #fff; }
.agui-section-title { font-weight: 600; font-size: 13px; margin-bottom: 8px; display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.agui-intro { font-size: 12px; margin: 0 0 10px; }
.agui-ids { font-size: 11px; }
.agui-input-row { display: flex; gap: 10px; }
.agui-sample-select { width: 220px; }
.agui-input { flex: 1; max-width: 260px; }
.agui-input-entity { max-width: 220px; }
.agui-toolcall { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; padding: 5px 8px;
  border: 1px solid #e4e9f0; border-radius: 6px; margin-bottom: 4px; }
.agui-toolcall.running { border-color: #67c23a; }
.agui-idx { font-size: 11px; color: #8a94a6; min-width: 12px; }
.agui-args { font-size: 11px; color: #4a5568; }
.agui-result { font-size: 11px; }
.agui-spin { color: #67c23a; animation: agui-blink 1s steps(2) infinite; }
@keyframes agui-blink { 50% { opacity: 0; } }
.agui-pruned { margin: 2px; }
.agui-answer { font-size: 13px; line-height: 1.6; padding: 8px 10px; background: #f5f7fa; border-radius: 8px; }
.agui-cursor { display: inline-block; width: 6px; height: 14px; background: #409eff; margin-left: 2px;
  vertical-align: text-bottom; animation: agui-blink 1s steps(2) infinite; }
.agui-topo-note { font-size: 11px; margin-top: 6px; }
</style>
