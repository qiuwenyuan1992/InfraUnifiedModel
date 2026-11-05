export interface Page<T> { items: T[]; next_cursor: string | null }
export interface PageQuery { limit?: number; cursor?: string }
export interface DiagnosticQuery extends PageQuery { source_id?: string }
export interface RoundQuery extends DiagnosticQuery { status?: string }
export interface Source { id: string; name: string; adapter_kind: string; enabled: boolean }
export interface Round {
  id: string
  source_id: string
  status: string
  mode: string
  created_at: string
  started_at: string | null
  finished_at: string | null
  snapshot_at: string | null
  cancel_requested_at: string | null
  error_code: string
  completed_phases: number
  total_phases: number
  phases?: Phase[]
}
export interface Phase {
  name: string
  position: number
  status: string
  run_id: string | null
  started_at: string | null
  finished_at: string | null
  nodes: number
  edges: number
  objects: number
  skipped: number
  missing_endpoints: number
  error_code: string
}
export interface Diagnostic {
  id: string
  run_id: string
  source_id: string
  resource: string
  severity: string
  code: string
  object_ref: string
  field_path: string
  created_at: string
}
