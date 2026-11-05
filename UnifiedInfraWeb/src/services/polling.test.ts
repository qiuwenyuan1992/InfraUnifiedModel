import { afterEach, expect, it, vi } from 'vitest'
import { createPoller } from './polling'

afterEach(() => vi.useRealTimers())

it('polls only after the previous request settles and stops cleanly', async () => {
  vi.useFakeTimers()
  let release!: () => void
  const job = vi.fn(() => new Promise<void>((resolve) => { release = resolve }))
  const poller = createPoller(job, 10000)
  poller.start()
  poller.start()
  await vi.advanceTimersByTimeAsync(10000)
  expect(job).toHaveBeenCalledTimes(1)
  await vi.advanceTimersByTimeAsync(30000)
  expect(job).toHaveBeenCalledTimes(1)
  release()
  await vi.advanceTimersByTimeAsync(10000)
  expect(job).toHaveBeenCalledTimes(2)
  poller.stop()
  release()
  await vi.advanceTimersByTimeAsync(30000)
  expect(job).toHaveBeenCalledTimes(2)
})

it('does not restart from an obsolete request after stop/start', async () => {
  vi.useFakeTimers()
  let release!: () => void
  const job = vi.fn(() => new Promise<void>((resolve) => { release = resolve }))
  const poller = createPoller(job, 10000)
  poller.start()
  await vi.advanceTimersByTimeAsync(10000)
  poller.stop()
  poller.start()
  release()
  await vi.advanceTimersByTimeAsync(10000)
  expect(job).toHaveBeenCalledTimes(2)
  poller.stop()
  release()
})
