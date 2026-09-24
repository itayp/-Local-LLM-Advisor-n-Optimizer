import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'
import type { BenchProgress } from './types'

/** An EventSource that never delivers anything: a stream something in between holds back. */
class SilentEventSource {
  static last: SilentEventSource | null = null
  closed = false
  onerror: (() => void) | null = null
  url: string
  constructor(url: string) {
    this.url = url
    SilentEventSource.last = this
  }
  addEventListener() {}
  close() {
    this.closed = true
  }
}

const progress = (status: BenchProgress['status'], message: string) =>
  ({ run_id: 7, status, phase: status === 'done' ? 'finished' : 'loading', message, step: 0, steps: 3, elapsed_seconds: 5, run: { id: 7 } }) as unknown as BenchProgress

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('followBench', () => {
  it('polls the progress when the stream stays silent, and stops at the end', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('EventSource', SilentEventSource)
    let status: BenchProgress['status'] = 'running'
    const urls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        urls.push(url)
        return new Response(JSON.stringify(progress(status, status === 'running' ? 'Loading llama3.2:3b and warming it up' : 'Finished')), { status: 200 })
      }),
    )
    const seen: string[] = []
    api.followBench(7, (p) => seen.push(p.message))
    await vi.advanceTimersByTimeAsync(3000)
    expect(urls).toEqual([]) // the stream is given a few seconds first
    await vi.advanceTimersByTimeAsync(1500)
    expect(urls).toContain('/api/bench/7/progress')
    expect(seen).toContain('Loading llama3.2:3b and warming it up')
    status = 'done'
    await vi.advanceTimersByTimeAsync(3000)
    expect(seen.at(-1)).toBe('Finished')
    expect(SilentEventSource.last?.closed).toBe(true)
    const n = urls.length
    await vi.advanceTimersByTimeAsync(6000)
    expect(urls.length).toBe(n) // nothing more once it has ended
  })
})
