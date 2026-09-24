import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { Estimate, FileFit, ModelDetailResponse } from '../api/types'
import { Figure } from '../components/Figure'
import { en } from '../copy/en'
import { publicEntry } from '../test/publicFixtures'

const p = en.publicData
const c = en.screens.modelDetail

function estimate(source: 'estimated' | 'measured'): Estimate {
  const b = (gb: number) => ({ value: gb * 1024 ** 3, source })
  return {
    request: { catalog_file_id: 41, num_ctx: 8192, kv_cache_type: 'f16', runtime_path: 'metal' },
    memory: { weights: b(5.6), kv_cache: b(0.3), overhead: b(0.2), total: b(6.1), gpu_resident: b(6.1), cpu_offload: b(0), effective_ctx: 8192 },
    speed: {
      known: true,
      generation:
        source === 'measured'
          ? { value: 22.4, low: 22.4, high: 22.4, unit: 'tok/s', source }
          : { value: 20, low: 16, high: 24, unit: 'tok/s', source },
    },
    category: 'fits',
    threshold: '',
    budget_bytes: 10.7 * 1024 ** 3,
    budget_known: true,
    budget_kind: 'unified_memory',
    basis: { memory_model: 'modelled', path_source: 'expected', budget_known: true, speed_source: source },
  }
}

function fit(id: number, quant: string, def: boolean, source: 'estimated' | 'measured'): FileFit {
  return {
    default: def,
    estimate: estimate(source),
    file: {
      id, model_id: 4, filename: `x-${quant}.gguf`, role: 'model', quant, parts: 1, bytes: 5.7e9, bits_per_weight: 5, present: true,
      header: {
        architecture: 'qwen35', gguf_version: 3, tensor_count: 0, block_count: 33, head_count: 16, head_count_kv: 4,
        head_count_kv_stated: true, key_length: 256, value_length: 256, embedding_length: 4096, context_length: 262144,
        sliding_window: 0, full_attention_interval: 4, file_type: -1, file_type_name: '', expert_count: 0,
        expert_used_count: 0, has_vision: false, complete: false,
      },
      layout: { groups: [], recurrent_layers: 24, recurrent_state_elements: 0, stateless_layers: 0, basis: 'stated' },
      fetched_at: '2026-09-19T09:00:00Z',
    },
  }
}

function detail(over: Partial<ModelDetailResponse> = {}): ModelDetailResponse {
  return {
    id: 4,
    family_id: 'qwen3.5',
    name: 'Qwen3.5 9B',
    maintainer: 'Qwen (Alibaba Cloud)',
    purposes: ['chat', 'coding'],
    released_at: '2026-03-02',
    pull_name: 'qwen3.5:9b',
    public: {
      entries: [
        publicEntry(),
        publicEntry({
          source_id: 'hf_evals', source_name: 'Hugging Face Eval Results', metric: 'hf:Idavidrein/gpqa/diamond',
          tests: 'a graduate-level science exam (GPQA Diamond)', provenance_words: "reported by the model's maker",
          position: '3rd for reasoning of the 4 models here with this score on Hugging Face.', scored: false,
          value: { value: 81.7, scale: 'percent', origin: { publisher: 'Qwen on Hugging Face', date: '2026-03-02', licence: 'HF-ToS; repo:apache-2.0', attribution: 'Reported by Qwen (Alibaba Cloud) on Hugging Face', provenance: 'maker' } },
          detail: [{ label: 'Value as published', text: '81.7 (percent of questions answered correctly)' }],
        }),
      ],
      updated: 'Public scores last updated 24 September 2026.',
    },
    local: {
      family_id: 'qwen3.5', display_name: 'Qwen3.5',
      model: { id: 4, family_id: 'qwen3.5', size: { parameters: 9e9, context_length: 262144, ollama_tag: 'qwen3.5:9b', hf_repo: 'b/x', hf_base_repo: 'Qwen/Qwen3.5-9B' }, present: true, parameters_counted: 0, files: [] },
      num_ctx: 8192, num_ctx_source: 'ollama_default', kv_cache_type: 'f16', runtime_path: 'metal', path_source: 'expected',
      fits: [fit(41, 'Q4_K_M', true, 'measured'), fit(42, 'Q8_0', false, 'estimated')],
    },
    ...over,
  }
}

function serve(d: ModelDetailResponse, status?: () => object, onRefresh?: () => void) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/models/4/detail') return new Response(JSON.stringify(d), { status: 200 })
      if (url === '/api/catalog/status' && status) return new Response(JSON.stringify(status()), { status: 200 })
      if (url === '/api/catalog/refresh' && init?.method === 'POST' && onRefresh) {
        onRefresh()
        return new Response(JSON.stringify({ sizes: 1, resolved: 1 }), { status: 200 })
      }
      if (url === '/api/health') return new Response(JSON.stringify({ version: 'test', os: 'darwin', arch: 'arm64', go_version: 'go' }), { status: 200 })
      return new Response('{}', { status: 404 })
    }),
  )
}

function open(advanced = false) {
  return render(
    <MemoryRouter initialEntries={['/models/4']}>
      <App initialSettings={{ advanced }} />
    </MemoryRouter>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('the model detail view', () => {
  it('shows public data and this computer in two blocks titled apart, never mixed', async () => {
    serve(detail())
    open()
    expect(await screen.findByRole('heading', { level: 1, name: 'Qwen3.5 9B' })).toBeInTheDocument()
    const pub = screen.getByTestId('public-block')
    const mine = screen.getByTestId('machine-block')
    expect(within(pub).getByRole('heading', { level: 2 })).toHaveTextContent(p.title)
    expect(within(mine).getByRole('heading', { level: 2 })).toHaveTextContent(c.machineTitle)

    // P-1/P-2: no local figure in the public block, no public value in the machine's.
    expect(pub.querySelector('.figure')).toBeNull()
    expect(mine.querySelector('.public-figure')).toBeNull()
    expect(pub.querySelectorAll('.public-figure')).toHaveLength(2)

    // The machine's numbers keep their two treatments: measured for the default file.
    expect(within(mine).getByText('22.4 tok/s').closest('.figure')).toHaveAttribute('data-source', 'measured')

    // P-4: under every public value, what it tested, who, and the source's own date — not behind a tap.
    const values = pub.querySelectorAll('.public-figure')
    expect(values[0]).toHaveTextContent('Among the strongest for everyday chat')
    expect(values[0]).toHaveTextContent("People's votes comparing answers to everyday questions — rated by people comparing answers on Arena.")
    expect(values[0]).toHaveTextContent('Arena (arena.ai), 15 September 2026.')
    expect(values[0]).toHaveTextContent('CC BY 4.0 — position computed by the advisor')
    expect(values[1]).toHaveTextContent("reported by the model's maker")
    expect(values[1]).toHaveTextContent(p.notScored)

    // P-5: words first; the raw value only under Advanced.
    expect(pub).not.toHaveTextContent('1391')
    expect(screen.queryByTestId('public-detail')).toBeNull()
    expect(within(pub).getByText(/compressed copy/)).toBeInTheDocument()
    expect(screen.getByTestId('public-updated')).toHaveTextContent('last updated 24 September 2026')
  })

  it('shows the raw values under Advanced', async () => {
    serve(detail())
    open(true)
    const pub = await screen.findByTestId('public-block')
    expect(within(pub).getAllByTestId('public-detail')).toHaveLength(2)
    expect(pub).toHaveTextContent('1391 (a relative rating')
    expect(screen.getByTestId('machine-block').querySelector('.public-figure')).toBeNull()
  })

  it('says a size has no public scores, and borrows none', async () => {
    serve(detail({ public: { entries: [], updated: 'Public scores have not been fetched yet.' } }))
    open()
    expect(await screen.findByTestId('public-none')).toHaveTextContent('No public scores for this size yet.')
    expect(screen.getByTestId('public-block').querySelector('.public-figure')).toBeNull()
  })

  it('offers to fetch the public scores when none has ever been read here, then shows them', async () => {
    let fetched = false
    const none = detail({ public: { entries: [], updated: 'Public scores have not been fetched yet.' } })
    const calls: string[] = []
    serve(
      none,
      () => ({ fetched: true, running: false, public_fetched: fetched, public_updated: '' }),
      () => {
        fetched = true
        calls.push('refresh')
      },
    )
    open()
    const pub = await screen.findByTestId('public-block')
    const offer = await within(pub).findByTestId('public-fetch')
    expect(offer).toHaveTextContent(en.modelList.publicMissing)
    serve(
      detail(),
      () => ({ fetched: true, running: false, public_fetched: fetched, public_updated: '' }),
      () => {
        fetched = true
        calls.push('refresh')
      },
    )
    await userEvent.click(within(offer).getByRole('button', { name: en.modelList.fetchPublic }))
    expect(calls).toEqual(['refresh'])
    // The model is read again: its public values are there now, and the offer is gone.
    await waitFor(() => expect(screen.getByTestId('public-block').querySelectorAll('.public-figure')).toHaveLength(2))
    expect(screen.queryByTestId('public-fetch')).toBeNull()
  })

  it('links to a test of this very model', async () => {
    serve(detail())
    open()
    const mine = await screen.findByTestId('machine-block')
    expect(within(mine).getByRole('link', { name: c.test })).toHaveAttribute('href', '/benchmarks?model=qwen3.5%3A9b')
  })

  it('cannot hand a public value to <Figure>', () => {
    const v = publicEntry().value
    // @ts-expect-error — a public value has no `source`: <Figure> must refuse it at compile time.
    const el = <Figure bytes={v} />
    expect(el).toBeTruthy()
  })
})
