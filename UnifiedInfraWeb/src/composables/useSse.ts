// SSE 流式消费 composable：诊断页三个流式端点（run/storm/explore）共用。
// 统一 fetch+reader+帧解析（event:/data: 双行帧，\n\n 分隔），消除三份手写解析器。
export interface SseHandler {
  (event: string, data: Record<string, unknown>): void
}

export async function streamSse(url: string, body: unknown, onEvent: SseHandler): Promise<void> {
  const resp = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!resp.ok || !resp.body) {
    const err = await resp.json().catch(() => ({ detail: `HTTP ${resp.status}` }))
    throw new Error((err as { detail?: string }).detail ?? `HTTP ${resp.status}`)
  }
  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    let idx: number
    while ((idx = buf.indexOf('\n\n')) >= 0) {
      const frame = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      const evName = /^event: (.+)$/m.exec(frame)?.[1]
      const dataRaw = /^data: (.+)$/m.exec(frame)?.[1]
      if (!evName || !dataRaw) continue
      onEvent(evName, JSON.parse(dataRaw))
    }
  }
}
