export function createPoller(job: () => Promise<void>, interval: number) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let generation = 0
  let enabled = false
  let busy = false
  const schedule = (version: number) => {
    timer = setTimeout(async () => {
      if (!enabled || version !== generation) return
      if (!busy) {
        busy = true
        try { await job() } finally { busy = false }
      }
      if (enabled && version === generation) schedule(version)
    }, interval)
  }
  return {
    start() {
      if (enabled) return
      enabled = true
      schedule(++generation)
    },
    stop() {
      enabled = false
      generation++
      clearTimeout(timer)
    },
  }
}
