import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type {
  BackendInfo,
  BenchProgress,
  BenchRun,
  CatalogStatus,
  ChatAppsResponse,
  HardwareResponse,
  PullStatus,
  Purpose,
  Recommendation,
  RecommendResult,
} from '../api/types'
import { en } from '../copy/en'
import { Getting } from './Getting'
import { Onboarding } from './Onboarding'
import { OnboardingGate } from './OnboardingGate'
import { OllamaStep } from './OllamaStep'
import { Purposes } from './Purposes'
import { Recommendations } from './Recommendations'
import { publicEntry } from '../test/publicFixtures'

function health() {
  return { version: 'test', os: 'darwin', arch: 'arm64', go_version: 'go1.27.1' }
}

function json(v: unknown, status = 200) {
  return new Response(JSON.stringify(v), { status })
}

function hardware(): HardwareResponse {
  return {
    profile_id: 3,
    detected_at: '2026-09-19T09:00:00Z',
    fingerprint: 'v1-abc',
    changed: false,
    profile: {
      os: 'darwin',
      os_version: 'macOS 26.6.2 (25G83)',
      arch: 'arm64',
      hostname: 'mac',
      cpu: { model: 'Apple M1 Pro', cores_physical: 10, cores_logical: 10, has_avx2: false, has_avx512: false, vector_known: true },
      ram_bytes: 16 * 1024 ** 3,
      ram_known: true,
      gpus: [
        {
          vendor: 'apple',
          name: 'Apple M1 Pro',
          vram_bytes: 0,
          vram_known: false,
          vram_source: 'not applicable',
          driver_version: 'part of macOS 26.6.2 (25G83)',
          is_integrated: true,
          integrated_known: true,
          expected_backend: 'metal',
          expected_backend_reason: 'Ollama drives the graphics of every Apple Silicon Mac with Metal.',
          expected_backend_rule: 'apple-metal',
        },
      ],
      unified_memory: true,
      gpu_usable_bytes: 12713115648,
      gpu_usable_known: true,
      gpu_usable_source: 'Metal recommendedMaxWorkingSetSize',
      storage: { models_dir: '/Users/u/.ollama/models', models_dir_source: 'default', models_dir_exists: true, free_bytes: 200 * 1024 ** 3, free_known: true },
      is_laptop: true,
      laptop_known: true,
      tier: 'gpu_medium',
      summary: 'A Mac laptop with an Apple M1 Pro chip and 16 GB of memory, of which the graphics can use 11.8 GB.',
      expectations_from: 'Ollama v0.34.2',
    },
  }
}

function backendRunning(): BackendInfo {
  return { name: 'ollama', state: 'running', version: '0.34.2', host: 'http://127.0.0.1:11434', checked_at: '2026-09-19T09:00:00Z' }
}

function catalogStatus(over: Partial<CatalogStatus> = {}): CatalogStatus {
  return { fetched: true, running: false, public_fetched: true, public_updated: 'Public scores last updated 24 September 2026.', ...over }
}

function recommendation(over: Partial<Recommendation> = {}): Recommendation {
  return {
    family_id: 'qwen3.5',
    display_name: 'Qwen3.5 9B',
    model: {
      id: 4,
      family_id: 'qwen3.5',
      size: { parameters: 9e9, context_length: 262144, ollama_tag: 'qwen3.5:9b', hf_repo: 'bartowski/Qwen_Qwen3.5-9B-GGUF', hf_base_repo: 'Qwen/Qwen3.5-9B' },
      present: true,
      parameters_counted: 0,
      files: [],
    },
    file: {
      id: 41,
      model_id: 4,
      filename: 'Qwen_Qwen3.5-9B-Q4_K_M.gguf',
      role: 'model',
      quant: 'Q4_K_M',
      parts: 1,
      bytes: 6.2e9,
      bits_per_weight: 5.5,
      present: true,
      header: {
        architecture: 'qwen35', gguf_version: 3, tensor_count: 0, block_count: 33, head_count: 16, head_count_kv: 4,
        head_count_kv_stated: true, key_length: 256, value_length: 256, embedding_length: 4096, context_length: 262144,
        sliding_window: 0, full_attention_interval: 4, file_type: -1, file_type_name: '', expert_count: 0,
        expert_used_count: 0, has_vision: false, complete: true,
      },
      layout: { groups: [], recurrent_layers: 0, recurrent_state_elements: 0, stateless_layers: 0, basis: 'stated' },
      fetched_at: '2026-09-19T09:00:00Z',
    },
    pull_name: 'qwen3.5:9b',
    num_ctx: 16384,
    estimate: {
      request: { catalog_file_id: 41, num_ctx: 16384, kv_cache_type: 'f16', runtime_path: 'metal' },
      memory: {
        weights: { value: 7.1 * 1024 ** 3, source: 'estimated' },
        kv_cache: { value: 0.5 * 1024 ** 3, source: 'estimated' },
        overhead: { value: 250 * 1024 ** 2, source: 'estimated' },
        total: { value: 7.9 * 1024 ** 3, source: 'estimated' },
        gpu_resident: { value: 7.9 * 1024 ** 3, source: 'estimated' },
        cpu_offload: { value: 0, source: 'estimated' },
        effective_ctx: 16384,
      },
      speed: { known: true, generation: { value: 45, low: 35, high: 55, unit: 'tok/s', source: 'estimated' } },
      category: 'fits_with_headroom',
      threshold: '7.9 GB needed of 11.8 GB of graphics memory (67%)',
      budget_bytes: 11.8 * 1024 ** 3,
      budget_known: true,
      budget_kind: 'unified_memory',
      basis: { memory_model: 'modelled', path_source: 'established', budget_known: true, speed_source: 'estimated' },
    },
    download_bytes: 7.1e9,
    installed: true,
    speed: { value: 45, low: 35, high: 55, unit: 'tok/s', source: 'estimated' },
    reasons: [{ kind: 'fit', text: 'Fits your graphics with room to spare.' }],
    confidence: 'medium',
    confidence_why: 'Ollama has not run a model on this computer yet.',
    score: 0.5,
    factors: { purpose: 1, fit: 1, speed: 1, size: 0.5, public: 1 },
    ...over,
  }
}

function recommendResult(over: Partial<RecommendResult> = {}): RecommendResult {
  return { purposes: ['chat'], recommendations: [recommendation()], runtime_path: 'metal', path_source: 'established', ...over }
}

function benchRun(over: Partial<BenchRun> = {}): BenchRun {
  return {
    id: 9,
    status: 'running',
    phase: 'preparing',
    request: { model: 'qwen3.5:9b', prompts: ['500'] },
    config: {
      hardware_profile_id: 1, hardware_fingerprint: 'fp', backend: 'ollama', backend_version: '0.34.2', runtime_path: 'metal',
      model: 'qwen3.5:9b', model_digest: 'sha256:a', quantization: 'Q4_K_M', weights_bytes: 6.2e9, catalog_file_id: 41, num_ctx: 16384,
      effective_ctx: 16384, kv_cache_type: 'f16', flash_attention: true, flash_attention_known: true, parallel: 1, suite_version: '1',
      suite_digest: 'e8fe8f89', completion_tokens: 256, repeats: 3, daemon_version: 'test',
    },
    started_at: '2026-09-19T12:00:00Z',
    results: [],
    resident: 'gpu',
    replaced: false,
    ...over,
  }
}

function chatApps(): ChatAppsResponse {
  return {
    apps: [
      { id: 'ollama', name: 'Ollama', found: true, path: '/Applications/Ollama.app', download_url: 'https://ollama.com/download' },
      { id: 'lmstudio', name: 'LM Studio', found: false, download_url: 'https://lmstudio.ai' },
    ],
  }
}

/** FakeEventSource stands in for the browser's: a test pushes events (same shape as Benchmarks.test.tsx's own). */
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

afterEach(() => {
  vi.unstubAllGlobals()
  FakeEventSource.last = null
})

describe('OnboardingGate', () => {
  function serveStatus(completed: boolean) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url === '/api/onboarding') return json({ completed })
        return json(health())
      }),
    )
  }

  it('shows the working app once setup has been completed', async () => {
    serveStatus(true)
    render(
      <OnboardingGate>
        <p>the working app</p>
      </OnboardingGate>,
    )
    expect(await screen.findByText('the working app')).toBeInTheDocument()
  })

  it('shows the first-run flow until setup has been completed', async () => {
    serveStatus(false)
    render(
      <OnboardingGate>
        <p>the working app</p>
      </OnboardingGate>,
    )
    expect(await screen.findByRole('heading', { name: en.onboarding.welcome.title })).toBeInTheDocument()
    expect(screen.queryByText('the working app')).not.toBeInTheDocument()
  })

  it('shows the working app rather than trap the person behind a gate that cannot open', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    render(
      <OnboardingGate>
        <p>the working app</p>
      </OnboardingGate>,
    )
    expect(await screen.findByText('the working app')).toBeInTheDocument()
  })
})

describe('Onboarding: welcome to a measured number, unhelped', () => {
  it('walks welcome → hardware → Ollama → purposes → recommend → try it → use it', async () => {
    const user = userEvent.setup()
    const pullCalls: string[] = []
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        if (url === '/api/health') return json(health())
        if (url === '/api/hardware') return json(hardware())
        if (url === '/api/backends') return json({ backends: [backendRunning()] })
        if (url === '/api/models/installed') return json({ models: [] })
        if (url.startsWith('/api/recommend')) return json(recommendResult())
        if (url === '/api/bench' && method === 'POST') return json(benchRun(), 202)
        if (url === '/api/chatapps') return json(chatApps())
        if (url === '/api/onboarding/complete' && method === 'POST') return json({ completed: true, completed_at: '2026-09-19T12:05:00Z' })
        if (url === '/api/catalog/status') return json(catalogStatus())
        pullCalls.push(url)
        return json({ error: { code: 'not_found', message: 'unexpected in this test: ' + url } }, 404)
      }),
    )

    const onFinished = vi.fn()
    render(<Onboarding onFinished={onFinished} />)

    // 1. Welcome — what it does, that it does not chat, one button.
    expect(screen.getByRole('heading', { name: en.onboarding.welcome.title })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: en.onboarding.welcome.start }))

    // 2. Checking your computer — one sentence + tier, details collapsed.
    expect(await screen.findByTestId('onboarding-hardware-summary')).toHaveTextContent(hardware().profile.summary)
    expect(screen.getByTestId('onboarding-tier')).toHaveTextContent(en.onboarding.tier.gpu_medium)
    await user.click(screen.getByRole('button', { name: en.onboarding.checking.continue }))

    // 3. Ollama — already running: no button to change anything, just "found".
    expect(await screen.findByTestId('ollama-found')).toHaveTextContent('Found Ollama 0.34.2.')
    await user.click(screen.getByRole('button', { name: en.onboarding.ollama.continue }))

    // 4. Purposes — "Not sure — general chat" checked by default.
    expect(screen.getByRole('checkbox', { name: en.onboarding.purposes.labels.chat })).toBeChecked()
    await user.click(screen.getByRole('button', { name: en.onboarding.purposes.continue }))

    // 5. Recommendations — up to three cards; this one is already installed, so its
    // button continues straight on instead of downloading it again.
    expect(await screen.findByRole('heading', { name: 'Qwen3.5 9B' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: en.onboarding.recommend.continueInstalled }))

    // 6. Getting it is skipped for an already-installed model — straight to 7. Try it.
    expect(await screen.findByRole('heading', { name: en.onboarding.tryit.title })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: en.onboarding.tryit.run }))
    // From the click on, something visibly happens — before the first event
    // arrives, and the button does not come back meanwhile.
    expect(await screen.findByText(en.screens.benchmarks.startingTest)).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: en.screens.benchmarks.progressLabel })).not.toHaveAttribute('value')
    expect(screen.queryByRole('button', { name: en.onboarding.tryit.run })).not.toBeInTheDocument()

    await waitFor(() => expect(FakeEventSource.last).not.toBeNull())
    FakeEventSource.last?.push({
      run_id: 9, status: 'running', phase: 'measuring', message: 'Timing the 500-token passage', step: 1, steps: 3, elapsed_seconds: 10,
      run: benchRun(),
    })
    expect(await screen.findByText('Timing the 500-token passage')).toBeInTheDocument()

    FakeEventSource.last?.push({
      run_id: 9, status: 'done', phase: 'finished', message: 'Finished', step: 3, steps: 3, elapsed_seconds: 40,
      run: benchRun({
        status: 'done',
        phase: 'finished',
        finished_at: '2026-09-19T12:01:00Z',
        generation_tps: { value: 41.3, low: 41.3, high: 41.3, unit: 'tok/s', source: 'measured' },
        replaced: true,
      }),
    })

    // Result shown next to the estimate it replaces — measured, not dressed as estimated.
    const measuredRow = (await screen.findByText(en.onboarding.tryit.measured)).closest('dt')?.nextElementSibling
    expect(within(measuredRow as HTMLElement).getByText('41.3 tok/s')).toHaveClass('figure--measured')
    const estimatedRow = screen.getByText(en.onboarding.tryit.estimated).closest('dt')?.nextElementSibling
    expect(within(estimatedRow as HTMLElement).getByText(/35–55 tok\/s/)).toHaveClass('figure--estimated')

    await user.click(screen.getByRole('button', { name: en.onboarding.tryit.continue }))

    // 8. Use it — the exact name, a copy button, and the chat apps found on this machine.
    expect(await screen.findByTestId('onboarding-model-name')).toHaveTextContent('qwen3.5:9b')
    expect(await screen.findByText('Ollama')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: en.onboarding.useit.finish }))

    await waitFor(() => expect(onFinished).toHaveBeenCalled())
    expect(pullCalls).toEqual([]) // never pulled: the model was already installed
  })
})

describe('OllamaStep', () => {
  it('says what installing will cost, before the button is ever clicked (product rule 5)', async () => {
    const user = userEvent.setup()
    const calls: { url: string; method: string }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        calls.push({ url, method })
        if (url === '/api/backends') return json({ backends: [{ name: 'ollama', state: 'not_installed', checked_at: 't' }] })
        if (url === '/api/backends/ollama/install-size') return json({ bytes: 45_000_000, known: true })
        if (url === '/api/backends/ollama/install' && method === 'POST')
          return json({ backend: 'ollama', status: 'running', completed_bytes: 0 }, 202)
        return json({ error: { code: 'not_found', message: url } }, 404)
      }),
    )
    render(<OllamaStep onNext={() => undefined} />)
    const button = await screen.findByRole('button', { name: /Install Ollama \(about 45 MB\)/ })
    await user.click(button)
    await waitFor(() => expect(calls.some((c) => c.url === '/api/backends/ollama/install' && c.method === 'POST')).toBe(true))
  })

  it('offers to start Ollama once it is installed but not running', async () => {
    const user = userEvent.setup()
    const calls: { url: string; method: string }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        calls.push({ url, method })
        if (url === '/api/backends') return json({ backends: [{ name: 'ollama', state: 'installed_not_running', checked_at: 't' }] })
        if (url === '/api/backends/ollama/start' && method === 'POST') return json({ backend: 'ollama', status: 'starting' }, 202)
        return json({ error: { code: 'not_found', message: url } }, 404)
      }),
    )
    render(<OllamaStep onNext={() => undefined} />)
    const button = await screen.findByRole('button', { name: en.onboarding.ollama.start })
    await user.click(button)
    await waitFor(() => expect(calls.some((c) => c.url === '/api/backends/ollama/start' && c.method === 'POST')).toBe(true))
  })
})

describe('Purposes', () => {
  it('defaults to "Not sure — general chat" and lets other purposes be added', async () => {
    const user = userEvent.setup()
    let purposes: Purpose[] = ['chat']
    const { rerender } = render(
      <Purposes purposes={purposes} onChange={(p) => (purposes = p)} onNext={() => undefined} />,
    )
    const chat = screen.getByRole('checkbox', { name: en.onboarding.purposes.labels.chat })
    expect(chat).toBeChecked()
    await user.click(screen.getByRole('checkbox', { name: en.onboarding.purposes.labels.coding }))
    expect(purposes).toEqual(['chat', 'coding'])
    rerender(<Purposes purposes={purposes} onChange={(p) => (purposes = p)} onNext={() => undefined} />)
    expect(screen.getByRole('checkbox', { name: en.onboarding.purposes.labels.coding })).toBeChecked()
  })
})

describe('Recommendations', () => {
  // Step 8's lesson: onboarding's cards and Recommend's are two components;
  // what one gains the other needs too. Step 9b's public line is on both.
  it('shows a card\'s public line, with its source and date, apart from the reasons', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) =>
        url.startsWith('/api/recommend') ? json(recommendResult({ recommendations: [recommendation({ public: publicEntry() })] })) : json({}, 404),
      ),
    )
    render(<Recommendations purposes={['chat']} onDownload={() => undefined} />)
    const line = await screen.findByTestId('public-line')
    expect(line).toHaveTextContent('Among the strongest for everyday chat')
    expect(line).toHaveTextContent('Arena (arena.ai), 15 September 2026.')
    expect(line.querySelector('.figure')).toBeNull()
  })

  it('offers to fetch the model list when it was never fetched, shows the fetch as it goes, then asks again (no dead end on first run)', async () => {
    const user = userEvent.setup()
    const calls: { url: string; method: string }[] = []
    let phase: 'never' | 'running' | 'fetched' = 'never'
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        calls.push({ url, method })
        if (url.startsWith('/api/recommend')) {
          return phase === 'fetched'
            ? json(recommendResult())
            : json(
                recommendResult({
                  recommendations: [],
                  empty: 'The list of models has not been fetched yet, so there is nothing to recommend from.',
                  empty_code: 'catalogue_empty',
                }),
              )
        }
        if (url === '/api/catalog/status') {
          if (phase === 'running')
            return json(
              catalogStatus({
                fetched: false,
                running: true,
                progress: { phase: 'models', message: 'Reading the description of Gemma 4 E4B', done: 3, total: 9, started_at: new Date().toISOString() },
              }),
            )
          return json(catalogStatus({ fetched: phase === 'fetched' }))
        }
        if (url === '/api/catalog/refresh' && method === 'POST') {
          phase = 'running'
          await new Promise((r) => setTimeout(r, 400))
          phase = 'fetched'
          return json({ sizes: 9, resolved: 9, failures: [], warnings: [], unknown_installed: [] })
        }
        return json({ error: { code: 'not_found', message: 'unexpected in this test: ' + url } }, 404)
      }),
    )
    render(<Recommendations purposes={['chat']} onDownload={() => undefined} />)

    const missing = await screen.findByTestId('model-list-missing')
    expect(missing).toHaveTextContent(en.modelList.missingTitle)
    await user.click(within(missing).getByRole('button', { name: en.modelList.fetch }))

    // While it runs: what it reads now, how far it has got, and a moving bar.
    const fetching = await screen.findByTestId('model-list-fetching')
    expect(within(fetching).getByRole('progressbar', { name: en.modelList.progressLabel })).toBeInTheDocument()
    await waitFor(() => expect(fetching).toHaveTextContent('Reading the description of Gemma 4 E4B'))
    expect(fetching).toHaveTextContent(en.modelList.phaseModels)

    expect(await screen.findByRole('heading', { name: 'Qwen3.5 9B' }, { timeout: 3000 })).toBeInTheDocument()
    expect(calls.some((c) => c.url === '/api/catalog/refresh' && c.method === 'POST')).toBe(true)
    expect(screen.queryByTestId('model-list-missing')).not.toBeInTheDocument()
  })

  it('reports a failed fetch in words and leaves the button there to try again', async () => {
    const user = userEvent.setup()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        if (url.startsWith('/api/recommend'))
          return json(
            recommendResult({
              recommendations: [],
              empty: 'The list of models has not been fetched yet.',
              empty_code: 'catalogue_empty',
            }),
          )
        if (url === '/api/catalog/status') return json(catalogStatus({ fetched: false }))
        if (url === '/api/catalog/refresh' && method === 'POST') return json({ error: { code: 'internal', message: 'no network' } }, 500)
        return json({ error: { code: 'not_found', message: 'unexpected in this test: ' + url } }, 404)
      }),
    )
    render(<Recommendations purposes={['chat']} onDownload={() => undefined} />)

    await user.click(await screen.findByRole('button', { name: en.modelList.fetch }))
    expect(await screen.findByRole('alert')).toHaveTextContent('no network')
    expect(screen.getByRole('button', { name: en.modelList.fetch })).toBeInTheDocument()
  })

  it('offers the fetch whatever else is empty — a list never fetched is never a dead end — and not once it has been', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url.startsWith('/api/recommend'))
          return json(recommendResult({ recommendations: [], empty: 'Nothing fits this computer.', empty_code: 'nothing_fits' }))
        if (url === '/api/catalog/status') return json(catalogStatus({ fetched: true }))
        return json({ error: { code: 'not_found', message: 'unexpected in this test: ' + url } }, 404)
      }),
    )
    render(<Recommendations purposes={['chat']} onDownload={() => undefined} />)
    expect(await screen.findByText('Nothing fits this computer.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: en.modelList.fetch })).not.toBeInTheDocument()
  })
})

describe('Getting', () => {
  it('shows download progress, cancellably, then hands off once it is done', async () => {
    const user = userEvent.setup()
    let status: PullStatus = { status: 'running', model: 'qwen3.5:9b', completed_bytes: 2e9, total_bytes: 7.1e9 }
    const calls: { url: string; method: string }[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        calls.push({ url, method })
        if (url === '/api/models/pull' && method === 'POST') return json(status, 202)
        if (url === '/api/models/pull' && method === 'GET') return json(status)
        if (url === '/api/models/pull/cancel' && method === 'POST') {
          status = { status: 'cancelled', completed_bytes: status.completed_bytes }
          return json(status)
        }
        return json({ error: { code: 'not_found', message: url } }, 404)
      }),
    )
    const onDone = vi.fn()
    render(<Getting recommendation={recommendation({ installed: false })} onDone={onDone} />)

    expect(await screen.findByText('2 GB / 7.1 GB')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: en.onboarding.getting.cancel }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/models/pull/cancel')).toBe(true))
    expect(await screen.findByText(en.onboarding.getting.cancelled)).toBeInTheDocument()
    expect(onDone).not.toHaveBeenCalled()
  })
})
