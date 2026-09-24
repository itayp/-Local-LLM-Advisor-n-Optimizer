import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { BackendInfo, CatalogFile, Estimate, FitCategory, HardwareResponse, InstalledModel, ModelFitResponse, Rate } from '../api/types'
import { en } from '../copy/en'

const c = en.screens.models

function json(v: unknown, status = 200) {
  return new Response(JSON.stringify(v), { status })
}

function hardware(over: Partial<HardwareResponse['profile']['storage']> = {}): HardwareResponse {
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
      gpus: [],
      unified_memory: true,
      gpu_usable_bytes: 12713115648,
      gpu_usable_known: true,
      gpu_usable_source: 'Metal recommendedMaxWorkingSetSize',
      storage: { models_dir: '/Users/u/.ollama/models', models_dir_source: 'default', models_dir_exists: true, free_bytes: 200 * 1024 ** 3, free_known: true, ...over },
      is_laptop: true,
      laptop_known: true,
      tier: 'gpu_medium',
      summary: 'A Mac laptop.',
      expectations_from: 'Ollama v0.34.2',
    },
  }
}

function backend(): BackendInfo {
  return { name: 'ollama', state: 'running', checked_at: '2026-09-19T09:00:00Z' }
}

const header = {
  architecture: 'llama', gguf_version: 3, tensor_count: 0, block_count: 28, head_count: 24, head_count_kv: 8,
  head_count_kv_stated: true, key_length: 128, value_length: 128, embedding_length: 3072, context_length: 131072,
  sliding_window: 0, full_attention_interval: 0, file_type: -1, file_type_name: '', expert_count: 0,
  expert_used_count: 0, has_vision: false, complete: true,
}
const layout = { groups: [], recurrent_layers: 0, recurrent_state_elements: 0, stateless_layers: 0, basis: 'stated' as const }

function catalogFile(over: Partial<CatalogFile> = {}): CatalogFile {
  return {
    id: 41, model_id: 4, filename: 'llama3.2-3b-Q4_K_M.gguf', role: 'model', quant: 'Q4_K_M', parts: 1, bytes: 2e9,
    bits_per_weight: 4.5, present: true, header, layout, fetched_at: '2026-09-19T09:00:00Z', ...over,
  }
}

function estimate(category: FitCategory, speed?: Rate): Estimate {
  const b = (v: number) => ({ value: v, source: 'estimated' as const })
  return {
    request: { catalog_file_id: 41, num_ctx: 4096, kv_cache_type: 'f16', runtime_path: 'metal' },
    memory: { weights: b(2e9), kv_cache: b(4e8), overhead: b(0), total: b(2.4e9), gpu_resident: b(2.4e9), cpu_offload: b(0), effective_ctx: 4096 },
    speed: { known: !!speed, generation: speed },
    category,
    threshold: '2.4 GB needed of 11.8 GB the graphics can use (20%)',
    budget_bytes: 11.8 * 1024 ** 3,
    budget_known: true,
    budget_kind: 'unified_memory',
    basis: { memory_model: 'validated', path_source: 'established', budget_known: true, speed_source: speed ? 'estimated' : 'unknown' },
  }
}

function fitResponse(category: FitCategory, speed?: Rate): ModelFitResponse {
  return {
    family_id: 'llama3.2',
    display_name: 'Llama 3.2 3B',
    model: { id: 4, family_id: 'llama3.2', size: { parameters: 3e9, context_length: 131072, ollama_tag: 'llama3.2:3b', hf_repo: 'bartowski/x', hf_base_repo: 'base/x' }, present: true, parameters_counted: 0, files: [] },
    num_ctx: 4096,
    num_ctx_source: 'ollama_default',
    kv_cache_type: 'f16',
    runtime_path: 'metal',
    path_source: 'established',
    fits: [{ file: catalogFile(), default: true, estimate: estimate(category, speed) }],
  }
}

function model(over: Partial<InstalledModel> = {}): InstalledModel {
  return {
    backend_name: 'ollama',
    name: 'llama3.2:3b',
    size_bytes: 2e9,
    last_seen_at: '2026-09-19T09:00:00Z',
    catalog_match: 'file',
    catalog_model_id: 4,
    catalog_file_id: 41,
    family: 'llama',
    parameter_size: '3B',
    quantization: 'Q4_K_M',
    ...over,
  }
}

function serve(opts: {
  models?: InstalledModel[]
  fit?: ModelFitResponse
  backend?: BackendInfo
  hw?: HardwareResponse
  removeError?: string
}) {
  const calls: { url: string; method: string; body?: string }[] = []
  let installed = opts.models ?? [model()]
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body as string | undefined })
      if (url === '/api/models/installed') return json({ models: installed })
      if (url === '/api/backends') return json({ backends: [opts.backend ?? backend()] })
      if (url === '/api/hardware') return json(opts.hw ?? hardware())
      if (url.startsWith('/api/models/4/fit')) return json(opts.fit ?? fitResponse('fits_with_headroom', { value: 32, low: 32, high: 32, unit: 'tok/s', source: 'measured' }))
      if (url === '/api/backends/ollama/models/remove' && method === 'POST') {
        if (opts.removeError) return json({ error: { code: 'runtime_error', message: opts.removeError } }, 500)
        const body = JSON.parse((init?.body as string) ?? '{}') as { name: string }
        installed = installed.filter((m) => m.name !== body.name)
        return json({ models: installed })
      }
      return json({ version: 'test', os: 'darwin', arch: 'arm64', go_version: 'go' })
    }),
  )
  return calls
}

function open(advanced = false) {
  return render(
    <MemoryRouter initialEntries={['/models']}>
      <App initialSettings={{ advanced }} />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Models', () => {
  it('says so, written out, when nothing is installed yet', async () => {
    serve({ models: [] })
    open()
    const empty = await screen.findByTestId('models-empty')
    expect(within(empty).getByText(c.empty)).toBeInTheDocument()
    expect(within(empty).getByRole('link', { name: c.goToRecommend })).toHaveAttribute('href', '/recommend')
  })

  it('lists an installed model with its fit and speed, and the free space left', async () => {
    serve({})
    open()
    const table = await screen.findByTestId('models-table')
    expect(within(table).getByText('llama3.2:3b')).toBeInTheDocument()
    expect(await within(table).findByText(c.fitCategory.fits_with_headroom)).toBeInTheDocument()
    expect(within(table).getByText('32.0 tok/s')).toHaveClass('figure--measured')
    expect(within(table).getByText('1.9 GB')).toBeInTheDocument()
    expect(await screen.findByText(c.freeSpace('200 GB'))).toBeInTheDocument()
  })

  it("shows \"can't tell\" for a download the catalogue does not track, instead of guessing", async () => {
    serve({ models: [model({ catalog_match: 'unknown', catalog_model_id: undefined, catalog_file_id: undefined })] })
    open()
    const table = await screen.findByTestId('models-table')
    expect(within(table).getByText(c.fitCategory.unknown)).toBeInTheDocument()
    expect(within(table).getByText(c.noSpeed)).toBeInTheDocument()
  })

  it('keeps family, parameters, quantization and backend behind the Advanced toggle, off by default', async () => {
    serve({})
    open(false)
    await screen.findByTestId('models-table')
    expect(screen.queryByTestId('models-advanced')).not.toBeInTheDocument()
    expect(screen.queryByText('Q4_K_M')).not.toBeInTheDocument()
  })

  it('reveals the technical columns once Advanced is on', async () => {
    serve({})
    open(true)
    const table = await screen.findByTestId('models-table')
    expect(await within(table).findByTestId('models-advanced')).toBeInTheDocument()
    expect(within(table).getByText('llama')).toBeInTheDocument()
    expect(within(table).getByText('3B')).toBeInTheDocument()
    expect(within(table).getByText('Q4_K_M')).toBeInTheDocument()
    expect(within(table).getByText('ollama')).toBeInTheDocument()
  })

  it('asks first, naming the GB it frees, before removing anything (product rule 5)', async () => {
    const user = userEvent.setup()
    const calls = serve({})
    open()
    await screen.findByTestId('models-table')
    await user.click(screen.getByRole('button', { name: c.remove }))
    const confirm = await screen.findByTestId('remove-confirm')
    expect(within(confirm).getByText(c.removeConfirm('llama3.2:3b', '1.9 GB'))).toBeInTheDocument()
    expect(calls.some((x) => x.method === 'POST')).toBe(false)

    await user.click(within(confirm).getByRole('button', { name: c.remove }))
    await waitFor(() => expect(calls.some((x) => x.url === '/api/backends/ollama/models/remove' && x.method === 'POST')).toBe(true))
    expect(await screen.findByTestId('models-empty')).toBeInTheDocument()
  })

  it('cancels without removing anything', async () => {
    const user = userEvent.setup()
    const calls = serve({})
    open()
    await screen.findByTestId('models-table')
    await user.click(screen.getByRole('button', { name: c.remove }))
    await screen.findByTestId('remove-confirm')
    await user.click(screen.getByRole('button', { name: c.cancel }))
    expect(screen.queryByTestId('remove-confirm')).not.toBeInTheDocument()
    expect(calls.some((x) => x.method === 'POST')).toBe(false)
  })

  it('says why, in words, when removing fails, and keeps the model listed', async () => {
    const user = userEvent.setup()
    serve({ removeError: 'Ollama is not responding' })
    open()
    await screen.findByTestId('models-table')
    await user.click(screen.getByRole('button', { name: c.remove }))
    await user.click(within(await screen.findByTestId('remove-confirm')).getByRole('button', { name: c.remove }))
    expect(await screen.findByText(c.removeFailed('llama3.2:3b', 'Ollama is not responding'))).toBeInTheDocument()
    expect(screen.getByTestId('models-table')).toBeInTheDocument()
  })
})
