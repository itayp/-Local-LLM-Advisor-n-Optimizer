import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { BenchModel, BenchModelsResponse, BenchPlan, BenchProgress, BenchRun, Estimate, PullStatus } from '../api/types'
import { en } from '../copy/en'

const c = en.screens.benchmarks

const estimated = { value: 45, low: 35, high: 55, unit: 'tok/s', source: 'estimated' as const }

function estimate(): Estimate {
  const b = (v: number) => ({ value: v, source: 'estimated' as const })
  return {
    request: { catalog_file_id: 3, num_ctx: 4096, kv_cache_type: 'f16', runtime_path: 'metal' },
    memory: { weights: b(2e9), kv_cache: b(4e8), overhead: b(0), total: b(2.4e9), gpu_resident: b(2.4e9), cpu_offload: b(0), effective_ctx: 4096 },
    speed: { known: true, generation: estimated, prompt: { value: 500, low: 300, high: 700, unit: 'tok/s', source: 'estimated' } },
    category: 'fits_with_headroom',
    threshold: '2.2 GB needed of 11.8 GB the graphics can use (19%)',
    budget_bytes: 11.8 * 1024 ** 3,
    budget_known: true,
    budget_kind: 'unified_memory',
    basis: { memory_model: 'validated', path_source: 'established', budget_known: true, speed_source: 'estimated' },
  }
}

// As GET /api/bench/models orders them: curated first, then smallest first.
const installed: BenchModel[] = [
  { name: 'llama3.2:3b', display_name: 'Llama 3.2 3B', model_id: 3, installed: true },
  { name: 'llama3.1:8b', display_name: 'Llama 3.1 8B', model_id: 5, installed: true },
  { name: 'minicpm-v4.6:latest', installed: true },
]
const gemma: BenchModel = { name: 'gemma4:e4b', display_name: 'Gemma 4 E4B', model_id: 9, installed: false, download_bytes: 6.4e9, fit: 'fits_with_headroom' }

function benchModels(over: Partial<BenchModelsResponse> = {}): BenchModelsResponse {
  return { installed, available: [gemma], catalogue_fetched: true, too_big: 2, ...over }
}

function plan(over: Partial<BenchPlan> = {}): BenchPlan {
  return {
    model: 'llama3.2:3b',
    model_source: 'catalogue',
    num_ctx: 4096,
    num_ctx_source: 'ollama_default',
    prompts: [
      { id: '500', tokens: 475, runs: 3 },
      { id: '2000', tokens: 2047, runs: 3 },
      { id: '8000', tokens: 7408, runs: 0, skip: 'needs a context of at least 7,728 tokens to hold the prompt and its answer' },
    ],
    requests: 7,
    estimate: estimate(),
    duration: { value: 150, low: 70, high: 230, unit: 's', source: 'estimated' },
    suite: { version: '1', digest: 'e8fe8f89b46d9a09', completion_tokens: 256, warmups: 1, repeats: 3, temperature: 0, seed: 42 },
    ...over,
  }
}

const measured = (v: number, unit = 'tok/s') => ({ value: v, low: v, high: v, unit, source: 'measured' as const })

function run(over: Partial<BenchRun> = {}): BenchRun {
  return {
    id: 7,
    status: 'done',
    phase: 'finished',
    request: { model: 'llama3.2:3b' },
    config: {
      hardware_profile_id: 1, hardware_fingerprint: 'fp', backend: 'ollama', backend_version: '0.34.2', runtime_path: 'metal',
      model: 'llama3.2:3b', model_digest: 'sha256:a', quantization: 'Q4_K_M', weights_bytes: 2e9, catalog_file_id: 3, num_ctx: 4096,
      effective_ctx: 4096, kv_cache_type: 'f16', flash_attention: true, flash_attention_known: true, parallel: 1, suite_version: '1',
      suite_digest: 'e8fe8f89b46d9a09', completion_tokens: 256, repeats: 3, daemon_version: 'test',
    },
    started_at: '2026-09-19T12:00:00Z',
    finished_at: '2026-09-19T12:02:10Z',
    results: [
      { prompt: '500', prompt_tokens: 481, gen_tokens: 256, prompt_tps: measured(812.4), generation_tps: measured(41.3), ttft: measured(612, 'ms'),
        spread_pct: 1.2, prompt_spread_pct: 0.8, runs: 3, timings: [] },
    ],
    headline: '500',
    generation_tps: measured(41.3),
    resident: 'gpu',
    peak_vram: { value: 2.9 * 1024 ** 3, source: 'measured' },
    estimate: estimate(),
    replaced: true,
    unloaded: true,
    sampler_note: 'temperature and power are not read on a Mac: the tool that reads them needs an administrator’s password',
    ...over,
  }
}

/** FakeEventSource stands in for the browser's: a test pushes events. */
class FakeEventSource {
  static last: FakeEventSource | null = null
  url: string
  closed = false
  onerror: (() => void) | null = null
  private listeners: ((ev: MessageEvent<string>) => void)[] = []
  constructor(url: string) {
    this.url = url
    FakeEventSource.last = this
  }
  addEventListener(_type: string, fn: (ev: MessageEvent<string>) => void) {
    this.listeners.push(fn)
  }
  push(p: BenchProgress) {
    for (const fn of this.listeners) fn({ data: JSON.stringify(p) } as MessageEvent<string>)
  }
  close() {
    this.closed = true
  }
}

function serve(
  opts: {
    plan?: (url: string) => BenchPlan
    start?: () => Response
    history?: BenchRun[]
    models?: () => BenchModelsResponse
    pull?: () => PullStatus
    status?: () => object
  } = {},
) {
  const calls: { url: string; method: string; body?: string }[] = []
  vi.stubGlobal('EventSource', FakeEventSource)
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body as string | undefined })
      const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status })
      if (url === '/api/health') return json({ version: 'test', os: 'darwin', arch: 'arm64', go_version: 'go1.27.1' })
      if (url === '/api/bench/models') return json(opts.models ? opts.models() : benchModels())
      if (url === '/api/catalog/status')
        return json(opts.status ? opts.status() : { fetched: true, running: false, public_fetched: true, public_updated: '' })
      if (url.startsWith('/api/bench/plan')) return json(opts.plan ? opts.plan(url) : plan())
      if (url === '/api/bench/history') return json({ runs: opts.history ?? [] })
      if (url === '/api/bench' && method === 'POST') return opts.start ? opts.start() : json(run({ status: 'running', phase: 'preparing', results: [] }), 202)
      if (url === '/api/bench/7/cancel') return json(run({ status: 'cancelled', results: [], generation_tps: undefined, replaced: false }))
      if (url === '/api/models/pull' && method === 'POST') return json(opts.pull ? opts.pull() : { status: 'running', completed_bytes: 0 }, 202)
      if (url === '/api/models/pull') return json(opts.pull ? opts.pull() : { status: 'idle', completed_bytes: 0 })
      return json({ error: { code: 'not_found', message: 'no' } }, 404)
    }),
  )
  return calls
}

function open(advanced = false, path = '/benchmarks') {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App initialSettings={{ advanced }} />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  FakeEventSource.last = null
})

describe('Benchmarks', () => {
  it('plans a test before offering it: how long, what is left out, and a button that says so', async () => {
    const calls = serve()
    open()
    // The smallest model the catalogue knows is picked first.
    const button = await screen.findByRole('button', { name: 'Run a 1–4 minute test' })
    expect(screen.getByRole('combobox', { name: c.model })).toHaveValue('llama3.2:3b')
    expect(calls.some((x) => x.url === '/api/bench/plan?model=llama3.2%3A3b')).toBe(true)
    const planBox = screen.getByTestId('bench-plan')
    // Product rule 4: the duration and the estimate before are estimates.
    expect(within(planBox).getByText('1–4 minutes').closest('.figure')).toHaveAttribute('data-source', 'estimated')
    expect(within(planBox).getByText(/35–55 tok\/s/).closest('.figure')).toHaveAttribute('data-source', 'estimated')
    expect(within(planBox).getByText(/7,728 tokens/)).toBeInTheDocument()
    expect(button).toBeEnabled()
    expect(calls.some((x) => x.method === 'POST')).toBe(false)
  })

  it('runs the test, follows its progress, and shows the result as measured', async () => {
    const calls = serve()
    open()
    await userEvent.click(await screen.findByRole('button', { name: 'Run a 1–4 minute test' }))
    const post = calls.find((x) => x.method === 'POST')
    expect(JSON.parse(post?.body ?? '{}')).toEqual({ model: 'llama3.2:3b', measure_anyway: false })

    const es = FakeEventSource.last
    expect(es?.url).toBe('/api/bench/7')
    const live = run({ status: 'running', phase: 'measuring', results: [] })
    es?.push({ run_id: 7, status: 'running', phase: 'measuring', message: 'Timing the 475-token prompt, 2 of 3', step: 2, steps: 7,
      elapsed_seconds: 31, remaining: { value: 80, low: 40, high: 120, unit: 's', source: 'estimated' }, run: live })
    expect(await screen.findByText('Timing the 475-token prompt, 2 of 3')).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: c.progressLabel })).toHaveAttribute('value', '2')
    expect(screen.getByRole('button', { name: c.cancel })).toBeInTheDocument()

    const plans = () => calls.filter((x) => x.url.startsWith('/api/bench/plan')).length
    const plansBefore = plans()
    es?.push({ run_id: 7, status: 'done', phase: 'finished', message: 'Finished', step: 7, steps: 7, elapsed_seconds: 130, run: run() })
    const result = await screen.findByTestId('bench-run')
    // The plan is asked for again: the run has just replaced its estimate.
    await vi.waitFor(() => expect(plans()).toBeGreaterThan(plansBefore))
    expect(within(result).getByText('41.3 tok/s').closest('.figure')).toHaveAttribute('data-source', 'measured')
    expect(within(result).getByText(/35–55 tok\/s/).closest('.figure')).toHaveAttribute('data-source', 'estimated')
    expect(within(result).getByText(c.replaced)).toBeInTheDocument()
    expect(within(result).getByText(c.unloaded)).toBeInTheDocument()
    expect(within(result).getByText(/administrator/)).toBeInTheDocument()
    expect(screen.queryByTestId('bench-running')).not.toBeInTheDocument()
    expect(es?.closed).toBe(true)
    // The technical columns stay behind the Advanced toggle.
    expect(screen.queryByTestId('bench-advanced')).not.toBeInTheDocument()
  })

  it('stops a running test, and says the memory was freed', async () => {
    serve()
    open()
    await userEvent.click(await screen.findByRole('button', { name: 'Run a 1–4 minute test' }))
    FakeEventSource.last?.push({ run_id: 7, status: 'running', phase: 'loading', message: 'Loading llama3.2:3b and warming it up', step: 0,
      steps: 7, elapsed_seconds: 2, run: run({ status: 'running', results: [] }) })
    await userEvent.click(await screen.findByRole('button', { name: c.cancel }))
    const result = await screen.findByTestId('bench-run')
    expect(within(result).getByText(c.status.cancelled)).toBeInTheDocument()
    expect(within(result).getByText(c.unloaded)).toBeInTheDocument()
  })

  it('shows the refusal in words when the model would spill, and offers the slow test only on request', async () => {
    const refusal = 'At a context of 4,096 tokens this model does not fit in the graphics memory: part of it would run on the processor, several times slower.'
    const calls = serve({ plan: () => plan({ refusal, refusal_code: 'would_spill' }) })
    open()
    expect(await screen.findByTestId('bench-refusal')).toHaveTextContent(refusal)
    expect(screen.queryByRole('button', { name: /Run a/ })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: c.runAnyway }))
    expect(JSON.parse(calls.find((x) => x.method === 'POST')?.body ?? '{}').measure_anyway).toBe(true)
  })

  it('asks for a new plan when the context changes', async () => {
    const calls = serve()
    open()
    await screen.findByTestId('bench-plan')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: c.context }), '8192')
    await screen.findByTestId('bench-plan')
    expect(calls.some((x) => x.url === '/api/bench/plan?model=llama3.2%3A3b&num_ctx=8192')).toBe(true)
  })

  it('lists earlier tests and shows one, with its technical details under Advanced', async () => {
    serve({ history: [run(), run({ id: 6, generation_tps: measured(40.9), comparison: undefined })] })
    open(true)
    const table = await screen.findByTestId('bench-history')
    expect(within(table).getAllByText(/tok\/s/)[0].closest('.figure')).toHaveAttribute('data-source', 'measured')
    await userEvent.click(within(table).getAllByRole('button', { name: c.show })[0])
    const tech = await screen.findByTestId('bench-advanced')
    for (const term of [c.advanced.path, c.advanced.kvCache, c.advanced.flash, c.advanced.quantization]) {
      expect(within(tech).getByText(term.label)).toBeInTheDocument()
      expect(within(tech).getByText(term.explain)).toBeInTheDocument()
    }
    for (const fig of tech.querySelectorAll('.figure')) expect(fig).toHaveAttribute('data-source', 'measured')
  })

  it('shows the last measurement of this setting in the estimate’s place', async () => {
    serve({ plan: () => plan({ measured: { run_id: 6, at: '2026-09-19T17:06:56Z', generation_tps: measured(53.2) } }) })
    open()
    const planBox = await screen.findByTestId('bench-plan')
    expect(within(planBox).getByText(c.lastMeasured)).toBeInTheDocument()
    expect(within(planBox).getByText('53.2 tok/s').closest('.figure')).toHaveAttribute('data-source', 'measured')
    expect(within(planBox).queryByText(c.estimateBefore)).not.toBeInTheDocument()
  })

  it('shows a passage whose answers were too short to time: its reading speed, and why the answering speed is missing', async () => {
    const why = 'the model stopped on its own after 30 tokens, too few to time how fast it answers (that takes 64); the reading speed is still measured'
    serve({
      history: [
        run({
          results: [
            { prompt: '500', prompt_tokens: 481, gen_tokens: 30, prompt_tps: measured(812.4), generation_unknown: why, ttft: measured(612, 'ms'),
              spread_pct: 0, prompt_spread_pct: 0.8, runs: 3, timings: [] },
            { prompt: '2000', prompt_tokens: 1975, gen_tokens: 256, prompt_tps: measured(705.1), generation_tps: measured(38.2), ttft: measured(2900, 'ms'),
              spread_pct: 1.1, prompt_spread_pct: 0.5, runs: 3, timings: [] },
          ],
          headline: '2000',
          generation_tps: measured(38.2),
        }),
      ],
    })
    open(true)
    await userEvent.click(within(await screen.findByTestId('bench-history')).getByRole('button', { name: c.show }))
    const result = await screen.findByTestId('bench-run')
    // The headline is the passage that timed an answer: its speeds, not the first passage's.
    expect(within(result).getAllByText('38.2 tok/s')[0].closest('.figure')).toHaveAttribute('data-source', 'measured')
    expect(within(result).getAllByText('705 tok/s').length).toBeGreaterThan(0)
    expect(within(result).getByText(c.promptNote('500', why))).toBeInTheDocument()
    const rows = within(screen.getByTestId('bench-advanced')).getAllByRole('row')
    expect(rows.some((r) => r.textContent?.startsWith('481—'))).toBe(true)
  })

  it('compares two history runs side by side, and keeps their configuration under Advanced', async () => {
    serve({
      history: [
        run({ id: 7, generation_tps: measured(41.3) }),
        run({ id: 6, config: { ...run().config, model: 'llama3.1:8b', quantization: 'Q8_0' }, generation_tps: measured(20.0), comparison: undefined }),
      ],
    })
    open(true)
    const table = await screen.findByTestId('bench-history')
    const boxes = within(table).getAllByRole('checkbox')
    expect(boxes).toHaveLength(2)

    await userEvent.click(boxes[0])
    expect(screen.getByText(c.comparePick)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: c.compareButton })).not.toBeInTheDocument()

    await userEvent.click(boxes[1])
    await userEvent.click(screen.getByRole('button', { name: c.compareButton }))

    const compare = await screen.findByTestId('bench-compare')
    expect(within(compare).getByText(c.compareDiff('llama3.2:3b', '+107%', 'llama3.1:8b'))).toBeInTheDocument()
    expect(within(compare).getAllByText('41.3 tok/s').length).toBeGreaterThan(0)
    expect(within(compare).getAllByText('20.0 tok/s').length).toBeGreaterThan(0)

    const tech = await screen.findByTestId('bench-compare-advanced')
    expect(within(tech).getByText('Q4_K_M')).toBeInTheDocument()
    expect(within(tech).getByText('Q8_0')).toBeInTheDocument()

    await userEvent.click(within(compare).getByRole('button', { name: c.compareClose }))
    expect(screen.queryByTestId('bench-compare')).not.toBeInTheDocument()
  })

  it('caps the comparison picker at two, and keeps the technical configuration out of it when Advanced is off', async () => {
    serve({ history: [run({ id: 7 }), run({ id: 6, comparison: undefined }), run({ id: 5, comparison: undefined })] })
    open()
    const table = await screen.findByTestId('bench-history')
    const boxes = within(table).getAllByRole('checkbox')
    await userEvent.click(boxes[0])
    await userEvent.click(boxes[1])
    expect(boxes[2]).toBeDisabled()

    await userEvent.click(screen.getByRole('button', { name: c.compareButton }))
    const compare = await screen.findByTestId('bench-compare')
    expect(within(compare).queryByTestId('bench-compare-advanced')).not.toBeInTheDocument()
  })

  it('says so when nothing is installed and the list has nothing that would run here', async () => {
    serve({ models: () => benchModels({ installed: [], available: [], too_big: 0 }) })
    open()
    expect(await screen.findByText(c.noModels)).toBeInTheDocument()
  })

  it('lists the models not downloaded yet apart, and opens on the one a link named', async () => {
    const calls = serve()
    open(false, '/benchmarks?model=gemma4%3Ae4b')
    const picker = await screen.findByRole('combobox', { name: c.model })
    await waitFor(() => expect(picker).toHaveValue('gemma4:e4b'))
    const groups = picker.querySelectorAll('optgroup')
    expect([...groups].map((g) => g.label)).toEqual([c.groupInstalled, c.groupAvailable])
    expect(within(groups[1] as HTMLElement).getByRole('option', { name: 'Gemma 4 E4B — 6.4 GB download' })).toBeInTheDocument()
    expect(screen.getByText(c.tooBig(2))).toBeInTheDocument()
    // Not installed: no plan is asked for; the button says both things it will do, and the cost.
    const box = screen.getByTestId('bench-download')
    expect(box).toHaveTextContent(c.needsDownload('6.4 GB'))
    expect(within(box).getByRole('button', { name: c.downloadAndRun('6.4 GB') })).toBeInTheDocument()
    expect(calls.some((x) => x.url.startsWith('/api/bench/plan?model=gemma4'))).toBe(false)
    expect(calls.some((x) => x.method === 'POST')).toBe(false)
  })

  it('downloads a model that is not installed, shows the download, then runs the test by itself', async () => {
    // Idle until the button is clicked (the screen asks on arrival, to pick up a download already running).
    let pulled: PullStatus = { status: 'idle', completed_bytes: 0 }
    let done = false
    const calls = serve({
      pull: () => pulled,
      models: () =>
        done
          ? benchModels({ installed: [...installed, { ...gemma, installed: true, download_bytes: undefined, fit: undefined }], available: [] })
          : benchModels(),
      plan: (url) => plan({ model: url.includes('gemma4') ? 'gemma4:e4b' : 'llama3.2:3b' }),
    })
    open(false, '/benchmarks?model=gemma4%3Ae4b')
    const button = await screen.findByRole('button', { name: c.downloadAndRun('6.4 GB') })
    pulled = { status: 'running', model: 'gemma4:e4b', completed_bytes: 2e9, total_bytes: 6.4e9 }
    await userEvent.click(button)
    const post = calls.find((x) => x.url === '/api/models/pull' && x.method === 'POST')
    expect(JSON.parse(post?.body ?? '{}')).toEqual({ ollama_tag: 'gemma4:e4b' })
    expect(await screen.findByText('2 GB / 6.4 GB')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: c.downloadCancel })).toBeInTheDocument()

    pulled = { status: 'done', model: 'gemma4:e4b', completed_bytes: 6.4e9, total_bytes: 6.4e9 }
    done = true
    await waitFor(() => expect(calls.some((x) => x.url === '/api/bench' && x.method === 'POST')).toBe(true), { timeout: 4000 })
    const start = calls.find((x) => x.url === '/api/bench' && x.method === 'POST')
    expect(JSON.parse(start?.body ?? '{}')).toEqual({ model: 'gemma4:e4b', measure_anyway: false })
    expect(await screen.findByTestId('bench-running')).toBeInTheDocument()
  })

  it('shows the test starting from the click, before the first progress arrives', async () => {
    serve()
    open()
    await userEvent.click(await screen.findByRole('button', { name: 'Run a 1–4 minute test' }))
    const running = await screen.findByTestId('bench-running')
    expect(running).toHaveTextContent(c.startingTest)
    // A moving bar (no value) until the first passage is timed.
    expect(within(running).getByRole('progressbar', { name: c.progressLabel })).not.toHaveAttribute('value')
    expect(screen.queryByTestId('bench-plan')).not.toBeInTheDocument()
  })

  it('offers to fetch the model list here too when it was never fetched', async () => {
    let fetched = false
    const calls = serve({
      status: () => ({ fetched, running: false, public_fetched: fetched, public_updated: '' }),
      models: () => (fetched ? benchModels() : benchModels({ available: [], catalogue_fetched: false, too_big: 0 })),
    })
    const base = globalThis.fetch as unknown as (url: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url === '/api/catalog/refresh' && init?.method === 'POST') {
          fetched = true
          return new Response(JSON.stringify({ sizes: 9, resolved: 9 }), { status: 200 })
        }
        return base(url, init)
      }),
    )
    open()
    await userEvent.click(await screen.findByRole('button', { name: en.modelList.fetch }))
    // Afterwards the list is asked for again, and what it offers appears.
    expect(await screen.findByRole('option', { name: 'Gemma 4 E4B — 6.4 GB download' })).toBeInTheDocument()
    expect(calls.filter((x) => x.url === '/api/bench/models').length).toBeGreaterThan(1)
  })
})
