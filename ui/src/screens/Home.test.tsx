import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { BackendInfo, BackendState, BenchRun, HardwareResponse, InstalledModel } from '../api/types'
import { en } from '../copy/en'

const c = en.screens.home

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

function backend(state: BackendState, over: Partial<BackendInfo> = {}): BackendInfo {
  return { name: 'ollama', state, checked_at: '2026-09-19T09:00:00Z', ...over }
}

const models: InstalledModel[] = [{ backend_name: 'ollama', name: 'llama3.2:3b', size_bytes: 2e9, last_seen_at: '', catalog_match: 'file' }]

function benchRun(over: Partial<BenchRun> = {}): BenchRun {
  return {
    id: 7,
    status: 'done',
    phase: 'finished',
    request: { model: 'llama3.2:3b' },
    config: {
      hardware_profile_id: 1, hardware_fingerprint: 'fp', backend: 'ollama', backend_version: '0.34.2', runtime_path: 'metal',
      model: 'llama3.2:3b', model_digest: 'sha256:a', quantization: 'Q4_K_M', weights_bytes: 2e9, catalog_file_id: 3, catalog_model_id: 3, num_ctx: 4096,
      effective_ctx: 4096, kv_cache_type: 'f16', flash_attention: true, flash_attention_known: true, parallel: 1, suite_version: '1',
      suite_digest: 'e8fe8f89', completion_tokens: 256, repeats: 3, daemon_version: 'test',
    },
    started_at: '2026-09-19T12:00:00Z',
    finished_at: '2026-09-19T12:02:00Z',
    results: [],
    generation_tps: { value: 41.3, low: 41.3, high: 41.3, unit: 'tok/s', source: 'measured' },
    resident: 'gpu',
    replaced: true,
    ...over,
  }
}

function serve(opts: {
  hw?: HardwareResponse | 'unreachable'
  backend?: BackendInfo
  models?: InstalledModel[]
  history?: BenchRun[]
  installSize?: { bytes: number; known: boolean }
}) {
  const calls: { url: string; method: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method })
      if (url === '/api/hardware') {
        if (opts.hw === 'unreachable') throw new TypeError('Failed to fetch')
        return json(opts.hw ?? hardware())
      }
      if (url === '/api/backends') return json({ backends: opts.backend ? [opts.backend] : [] })
      if (url === '/api/models/installed') return json({ models: opts.models ?? [] })
      if (url === '/api/bench/history') return json({ runs: opts.history ?? [] })
      if (url === '/api/backends/ollama/install-size') return json(opts.installSize ?? { bytes: 45_000_000, known: true })
      if (url === '/api/backends/ollama/install' && method === 'POST') return json({ backend: 'ollama', status: 'running', completed_bytes: 0 }, 202)
      if (url === '/api/backends/ollama/start' && method === 'POST') return json({ backend: 'ollama', status: 'starting' }, 202)
      return json({ version: 'test', os: 'darwin', arch: 'arm64', go_version: 'go' })
    }),
  )
  return calls
}

function open() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <App />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Home', () => {
  it('leads with the hardware summary sentence', async () => {
    serve({ backend: backend('running'), models: [] })
    open()
    expect(await screen.findByTestId('home-summary')).toHaveTextContent('A Mac laptop with an Apple M1 Pro chip')
  })

  it("says so, in words, when this computer could not be read", async () => {
    serve({ hw: 'unreachable', backend: backend('running') })
    open()
    expect(await screen.findByText(c.failed('Failed to fetch'))).toBeInTheDocument()
  })

  it('shows the measured model once something has been benchmarked', async () => {
    serve({ backend: backend('running'), models, history: [benchRun()] })
    open()
    expect(await screen.findByText(/llama3.2:3b/)).toBeInTheDocument()
    expect(screen.getByText('41.3 tok/s')).toHaveClass('figure--measured')
  })

  it('offers to install Ollama, the cost stated before the button is ever clicked (product rule 5)', async () => {
    const user = userEvent.setup()
    const calls = serve({ backend: backend('not_installed') })
    open()
    const button = await screen.findByRole('button', { name: /Install Ollama \(about 45 MB\)/ })
    await user.click(button)
    await waitFor(() => expect(calls.some((x) => x.url === '/api/backends/ollama/install' && x.method === 'POST')).toBe(true))
  })

  it('offers to start Ollama once it is installed but not running', async () => {
    const user = userEvent.setup()
    const calls = serve({ backend: backend('installed_not_running') })
    open()
    const button = await screen.findByRole('button', { name: c.next.start })
    await user.click(button)
    await waitFor(() => expect(calls.some((x) => x.url === '/api/backends/ollama/start' && x.method === 'POST')).toBe(true))
  })

  it('names the reason in words when this computer cannot run Ollama at all', async () => {
    serve({ backend: backend('unsupported', { detail: 'This computer has no supported graphics driver.' }) })
    open()
    expect(await screen.findByTestId('next-unsupported')).toHaveTextContent('This computer has no supported graphics driver.')
  })

  it('sends you to Recommend when nothing is installed yet', async () => {
    serve({ backend: backend('running'), models: [] })
    open()
    const card = await screen.findByTestId('next-no-models')
    expect(card).toHaveTextContent(c.next.noModelsTitle)
    expect(screen.getByRole('link', { name: c.next.goToRecommend })).toHaveAttribute('href', '/recommend')
  })

  it('sends you to Benchmarks when something is installed but never measured', async () => {
    serve({ backend: backend('running'), models, history: [] })
    open()
    const card = await screen.findByTestId('next-not-benchmarked')
    expect(card).toHaveTextContent(c.next.notBenchmarkedTitle)
    expect(screen.getByRole('link', { name: c.next.goToBenchmarks })).toHaveAttribute('href', '/benchmarks')
  })

  it('says everything is set up once a benchmark has run', async () => {
    serve({ backend: backend('running'), models, history: [benchRun()] })
    open()
    const card = await screen.findByTestId('next-all-good')
    expect(card).toHaveTextContent(c.next.allGoodTitle)
  })
})
