import type { Diagnostic, DiagnosticQuery, Page, PageQuery, Round, RoundQuery, Source } from './types'

export class ApiError extends Error {
  constructor(public status: number, public code: number, message: string, public requestId: string) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/v1/inventory${path}`, {
    ...init, credentials: 'omit', headers: { 'Content-Type': 'application/json', ...init.headers },
  })
  const requestId = response.headers.get('X-Request-ID') || ''
  let envelope: { code: number; message: string; data: T }
  try {
    envelope = await response.json()
  } catch {
    throw new ApiError(response.status, -1, '服务响应无法解析，请检查后端服务与代理配置。', requestId)
  }
  if (!response.ok || envelope.code !== 0) {
    throw new ApiError(response.status, envelope.code, envelope.message, requestId)
  }
  return envelope.data
}

function query(values: PageQuery | RoundQuery): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(values)) {
    if (value !== undefined && value !== '') params.set(key, String(value))
  }
  return params.size ? `?${params}` : ''
}

export const inventoryApi = {
  sources: (q: PageQuery = {}, signal?: AbortSignal) => request<Page<Source>>(`/sources${query(q)}`, { signal }),
  rounds: (q: RoundQuery = {}, signal?: AbortSignal) => request<Page<Round>>(`/sync-rounds${query(q)}`, { signal }),
  detail: (id: string, signal?: AbortSignal) => request<Round>(`/sync-rounds/${encodeURIComponent(id)}`, { signal }),
  create: (sourceId: string, idempotencyKey: string) => request<Round>('/sync-rounds', {
    method: 'POST', headers: { 'Idempotency-Key': idempotencyKey },
    body: JSON.stringify({ source_id: sourceId, mode: 'full' }),
  }),
  cancel: (id: string) => request<Round>(`/sync-rounds/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
  diagnostics: (id: string, q: DiagnosticQuery = {}, isRound = true, signal?: AbortSignal) =>
    request<Page<Diagnostic>>(`/${isRound ? 'sync-rounds' : 'sync-runs'}/${encodeURIComponent(id)}/diagnostics${query(q)}`, { signal }),
}
