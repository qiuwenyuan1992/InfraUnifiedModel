// OKB 故障知识图谱 API：独立后端（/api/v1，经 Vite 代理到 OKB_SERVER）。
// 响应为裸 JSON（无 inventory 信封），故独立请求封装。
export class OkbApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'OkbApiError'
  }
}

async function okbRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    ...init, headers: { 'Content-Type': 'application/json', ...init.headers },
  })
  let body: unknown
  try {
    body = await response.json()
  } catch {
    throw new OkbApiError(response.status, 'OKB 服务响应无法解析，请检查服务与代理配置。')
  }
  if (!response.ok) {
    const message = (body as { error?: string }).error || `请求失败（HTTP ${response.status}）`
    throw new OkbApiError(response.status, message)
  }
  return body as T
}

// ---------- 类型（与 UnifiedInfraOKB docs/design/api-boundaries.md 对齐） ----------

export type EvidenceLevel = 'case_verified' | 'runtime_verified' | 'doc_explicit' | 'inferred' | 'expert_only'
export type KnowledgeStatus = 'draft' | 'curated'
export type Verdict = 'confirmed' | 'rejected'
export type SourceKind = 'internal_case' | 'vendor_doc' | 'expert_input' | 'alert_strategy' | 'alert_event' | 'system_code' | 'industry_report'
export type EdgeType = 'trigger' | 'indicate' | 'mitigate' | 'prevent'
// v2.1 边语义三分：causal 参与因果遍历；same_event/same_family 不参与遍历、以关联标注返回
export type EdgeKind = 'causal' | 'same_event' | 'same_family'

export interface LookupCandidate {
  anomaly_id: string
  stable_code: string
  name: string
  confidence: number
  evidence_level: EvidenceLevel
  revision_id: string
  status: KnowledgeStatus
}

export interface LookupResult {
  alert_signature: string
  candidates: LookupCandidate[]
}

export interface ConditionEval {
  fact_key: string
  op: string
  result: 'passed' | 'failed' | 'unknown'
}

export interface TraceHop {
  edge_id: string
  target_anomaly_id: string
  target_stable_code: string
  target_name: string
  conditions?: Array<{ fact_key: string; op: string; value: unknown }>
  evaluations?: ConditionEval[]
  time_constraints?: string
  evidence_level: EvidenceLevel
  confidence: number
  status: KnowledgeStatus
  revision_id: string
}

export interface TraceChain {
  hops: TraceHop[]
  fully_curated: boolean
  min_confidence: number
  combined_note?: string
}

export interface TraceResult {
  start_anomaly_id: string
  max_hops: number
  chains: TraceChain[]
}

export interface EvidenceItem {
  evidence_id: string
  kind: string
  level: EvidenceLevel
  fragment_id: string
  summary: string
  content: string
  source_kind?: SourceKind
  created_by: string
}

export interface EvidenceView {
  entity_type: string
  entity_id: string
  revision_id: string
  items: EvidenceItem[]
}

export interface IngestOutput {
  source_id: string
  source_version_id: string
  parsed_version_id: string
  extracted_model: string
  new_anomalies: number
  new_rules: number
  new_mappings: number
  curated_rules: number
  duplicate_content?: boolean // 同内容已入库：本次未新增证据（不虚增印证）
}

export interface ReportResult {
  recorded: boolean
  new_confidence: number
  new_status: KnowledgeStatus
  review_flagged: boolean
}

// 整图视图（/api/v1/graph，可视化消费）
export interface GraphNode {
  id: string
  stable_code: string
  name: string
  category: string
  // v2.1 字段（着色/等级编码用）
  domain?: string
  severity_online?: string
  node_kind?: 'atomic' | 'aggregate' | 'placeholder' | 'event'
  status: KnowledgeStatus
  revision_id: string
}

export interface GraphEdge {
  id: string
  source: string
  target: string
  edge_kind?: EdgeKind // v2.1 三态：causal / same_event / same_family
  edge_type: EdgeType
  evidence_level: EvidenceLevel
  confidence: number
  status: KnowledgeStatus
  review_flagged: boolean
  case_support_count: number
}

export interface GraphView {
  nodes: GraphNode[]
  edges: GraphEdge[]
}

// 知识清单（/api/v1/knowledge）
export interface KnowledgeRow {
  id: string
  kind: 'anomaly' | 'rule' | 'mapping'
  stable_code?: string
  name?: string
  category?: string
  // v2.1 anomaly 字段（场景关系图着色与 tooltip 用）
  domain?: string
  severity_online?: string
  node_kind?: 'atomic' | 'aggregate' | 'placeholder' | 'event'
  clock_hint?: string
  source_code?: string
  target_code?: string
  alert_signature?: string
  edge_kind?: EdgeKind
  edge_type?: EdgeType
  evidence_level?: EvidenceLevel
  confidence?: number
  case_support_count?: number
  status: KnowledgeStatus
  review_flagged?: boolean
  revision_id: string
  updated_at: string
}

export interface KnowledgeList {
  kind: string
  total: number
  rows: KnowledgeRow[]
}

// ---------- API ----------

// 处置建议（/api/v1/mitigations）
export interface MitigationRow {
  mitigation_code: string
  mitigation_name: string
  edge_id: string
  via_root_cause?: string // 根因挂靠：非空时仅当诊断根因为此才建议
  evidence_level: EvidenceLevel
  confidence: number
  status: KnowledgeStatus
  case_support_count: number
  fragment_ref: string
}

// 症状指示（/api/v1/indications）
export interface IndicationRow {
  edge_id: string
  other_code: string
  other_name: string
  direction: 'outgoing' | 'incoming'
  evidence_level: EvidenceLevel
  confidence: number
  status: KnowledgeStatus
}

// 场景（/api/v1/scenarios）
export interface ScenarioCard {
  code: string
  name: string
  domain: string
  description: string
  node_count: number
  edge_count: number
  curated_edges: number
  draft_edges: number
  alert_count: number
  mitigation_count: number
}

export interface ScenarioDetail {
  scenario: ScenarioCard
  nodes: KnowledgeRow[]
  edges: KnowledgeRow[]
}

export const okbApi = {
  graph: (signal?: AbortSignal) => okbRequest<GraphView>('/graph', { signal }),
  knowledge: (kind: 'anomaly' | 'rule' | 'mapping', params: { status?: string; q?: string; limit?: number; offset?: number } = {}, signal?: AbortSignal) => {
    const search = new URLSearchParams({ kind })
    if (params.status) search.set('status', params.status)
    if (params.q) search.set('q', params.q)
    if (params.limit) search.set('limit', String(params.limit))
    if (params.offset) search.set('offset', String(params.offset))
    return okbRequest<KnowledgeList>(`/knowledge?${search}`, { signal })
  },
  lookup: (alertSignature: string, includeDraft = false, signal?: AbortSignal) =>
    okbRequest<LookupResult>(`/lookup?alert_signature=${encodeURIComponent(alertSignature)}${includeDraft ? '&include_draft=true' : ''}`, { signal }),
  trace: (anomalyCode: string, maxHops = 3, includeDraft = false, signal?: AbortSignal) =>
    okbRequest<TraceResult>(`/trace?anomaly_code=${encodeURIComponent(anomalyCode)}&max_hops=${maxHops}${includeDraft ? '&include_draft=true' : ''}`, { signal }),
  evidence: (ref: string, signal?: AbortSignal) =>
    okbRequest<EvidenceView>(`/evidence?ref=${encodeURIComponent(ref)}`, { signal }),
  mitigations: (anomalyCode: string, rootCauseCode?: string, includeDraft = false, signal?: AbortSignal) => {
    const params = new URLSearchParams({ anomaly_code: anomalyCode })
    if (rootCauseCode) params.set('root_cause_code', rootCauseCode)
    if (includeDraft) params.set('include_draft', 'true')
    return okbRequest<{ anomaly_code: string; mitigations: MitigationRow[] }>(`/mitigations?${params}`, { signal })
  },
  indications: (anomalyCode: string, direction: 'outgoing' | 'incoming', includeDraft = false, signal?: AbortSignal) =>
    okbRequest<{ anomaly_code: string; direction: string; indications: IndicationRow[] }>(
      `/indications?anomaly_code=${encodeURIComponent(anomalyCode)}&direction=${direction}${includeDraft ? '&include_draft=true' : ''}`, { signal }),
  report: (payload: { diagnosis_ref: string; edge_id: string; verdict: Verdict; notes?: string }) =>
    okbRequest<ReportResult>('/report', { method: 'POST', body: JSON.stringify(payload) }),
  ingest: (payload: { kind: SourceKind; title: string; content: string }) =>
    okbRequest<IngestOutput>('/sources', { method: 'POST', body: JSON.stringify(payload) }),
  reviewQueue: (limit = 50, signal?: AbortSignal) =>
    okbRequest<{ edges: KnowledgeRow[] }>(`/review/queue?limit=${limit}`, { signal }),
  scenarioCards: (signal?: AbortSignal) =>
    okbRequest<{ scenarios: ScenarioCard[] }>('/scenarios', { signal }),
  scenarioDetail: (code: string, signal?: AbortSignal) =>
    okbRequest<ScenarioDetail>(`/scenarios/${encodeURIComponent(code)}`, { signal }),
  scenarioUpsert: (payload: { code: string; name: string; domain: string; description?: string }) =>
    okbRequest<{ code: string; status: string }>('/scenarios', { method: 'POST', body: JSON.stringify(payload) }),
  scenarioAssign: (code: string, stableCodes: string[]) =>
    okbRequest<{ assigned: number }>(`/scenarios/${encodeURIComponent(code)}/nodes`, { method: 'POST', body: JSON.stringify({ stable_codes: stableCodes }) }),
  confirmEdge: (edgeId: string) =>
    okbRequest<{ status: string }>(`/edges/${encodeURIComponent(edgeId)}/confirm`, { method: 'POST' }),
  rejectEdge: (edgeId: string) =>
    okbRequest<{ status: string }>(`/edges/${encodeURIComponent(edgeId)}/reject`, { method: 'POST' }),
}
