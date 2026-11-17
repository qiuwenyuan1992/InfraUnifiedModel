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

export type EvidenceLevel = 'case_verified' | 'doc_explicit' | 'inferred' | 'expert_only'
export type KnowledgeStatus = 'draft' | 'curated'
export type Verdict = 'confirmed' | 'rejected'
export type SourceKind = 'internal_case' | 'vendor_doc' | 'expert_input'

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
  status: KnowledgeStatus
  revision_id: string
}

export interface GraphEdge {
  id: string
  source: string
  target: string
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
  source_code?: string
  target_code?: string
  alert_signature?: string
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
  report: (payload: { diagnosis_ref: string; edge_id: string; verdict: Verdict; notes?: string }) =>
    okbRequest<ReportResult>('/report', { method: 'POST', body: JSON.stringify(payload) }),
  ingest: (payload: { kind: SourceKind; title: string; content: string }) =>
    okbRequest<IngestOutput>('/sources', { method: 'POST', body: JSON.stringify(payload) }),
  reviewQueue: (limit = 50, signal?: AbortSignal) =>
    okbRequest<{ edges: KnowledgeRow[] }>(`/review/queue?limit=${limit}`, { signal }),
  confirmEdge: (edgeId: string) =>
    okbRequest<{ status: string }>(`/edges/${encodeURIComponent(edgeId)}/confirm`, { method: 'POST' }),
  rejectEdge: (edgeId: string) =>
    okbRequest<{ status: string }>(`/edges/${encodeURIComponent(edgeId)}/reject`, { method: 'POST' }),
}
