import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { Estimate, Recommendation, RecommendResult } from '../api/types'
import { en } from '../copy/en'
import { publicEntry } from '../test/publicFixtures'

const c = en.screens.recommend

function estimate(over: Partial<Estimate> = {}): Estimate {
  return {
    request: { catalog_file_id: 41, num_ctx: 16384, kv_cache_type: 'f16', runtime_path: 'cuda' },
    memory: {
      weights: { value: 7.1 * 1024 ** 3, source: 'estimated' },
      kv_cache: { value: 0.5 * 1024 ** 3, source: 'estimated' },
      overhead: { value: 250 * 1024 ** 2, source: 'estimated' },
      total: { value: 7.9 * 1024 ** 3, source: 'estimated' },
      gpu_resident: { value: 7.9 * 1024 ** 3, source: 'estimated' },
      cpu_offload: { value: 0, source: 'estimated' },
      effective_ctx: 16384,
    },
    speed: {
      known: true,
      generation: { value: 96, low: 79, high: 113, unit: 'tok/s', source: 'estimated' },
      prompt: { value: 4000, low: 2000, high: 6000, unit: 'tok/s', source: 'estimated' },
      basis: 'NVIDIA GeForce RTX 5070 Ti: 896 GB/s of memory bandwidth, of which cuda reaches 60–78%',
    },
    category: 'fits_with_headroom',
    threshold: '7.9 GB needed of 15.9 GB of graphics memory (50%): at or under 80% fits with headroom',
    budget_bytes: 15.9 * 1024 ** 3,
    budget_known: true,
    budget_kind: 'graphics_memory',
    basis: { memory_model: 'modelled', path_source: 'expected', budget_known: true, speed_source: 'estimated' },
    notes: ['hybrid attention: one layer in 4 is an attention layer; the others keep a small fixed state'],
    ...over,
  }
}

function card(over: Partial<Recommendation> = {}): Recommendation {
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
        expert_used_count: 0, has_vision: true, complete: false,
      },
      layout: { groups: [], recurrent_layers: 24, recurrent_state_elements: 0, stateless_layers: 1, basis: 'stated' },
      fetched_at: '2026-09-19T09:00:00Z',
    },
    pull_name: 'qwen3.5:9b',
    num_ctx: 16384,
    estimate: estimate(),
    download_bytes: 7.1e9,
    installed: false,
    speed: { value: 96, low: 79, high: 113, unit: 'tok/s', source: 'estimated' },
    reasons: [
      { kind: 'fit', text: 'Fits your 16 GB graphics card with room to spare.' },
      { kind: 'purpose', text: 'Qwen3.5 handles coding too, though it is best known for everyday chat.' },
      { kind: 'speed', text: 'Estimated to answer at roughly 59 to 85 words a second — much faster than you can read.' },
      { kind: 'size', text: 'About 7 GB to download.' },
    ],
    confidence: 'medium',
    confidence_why: 'Ollama has not run a model on this computer yet, so that it will use the graphics is expected, not seen.',
    score: 0.513,
    factors: { purpose: 0.95, fit: 1, speed: 1, size: 0.54, public: 1 },
    ...over,
  }
}

function result(over: Partial<RecommendResult> = {}): RecommendResult {
  return { purposes: ['chat'], recommendations: [card()], runtime_path: 'cuda', path_source: 'expected', ...over }
}

function serve(answer: (url: string) => RecommendResult | { status: number; code: string; message: string }) {
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/recommend')) {
        calls.push(url)
        const a = answer(url)
        if ('status' in a) return new Response(JSON.stringify({ error: { code: a.code, message: a.message } }), { status: a.status })
        return new Response(JSON.stringify(a), { status: 200 })
      }
      if (url === '/api/health') {
        return new Response(JSON.stringify({ version: 'test', os: 'linux', arch: 'amd64', go_version: 'go1.27.1' }), { status: 200 })
      }
      return new Response('{}', { status: 404 })
    }),
  )
  return calls
}

function open(advanced = false) {
  return render(
    <MemoryRouter initialEntries={['/recommend']}>
      <App initialSettings={{ advanced }} />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Recommend', () => {
  it('asks what AI is for and shows a card with reasons, cost and an estimated range', async () => {
    const calls = serve(() => result())
    open()

    expect(screen.getByText(c.question)).toBeInTheDocument()
    expect(screen.getByLabelText(c.purposes.chat)).toBeChecked()

    const item = await screen.findByRole('article', { name: 'Qwen3.5 9B' })
    expect(calls[0]).toBe('/api/recommend?purposes=chat')
    for (const r of card().reasons) expect(within(item).getByText(r.text)).toBeInTheDocument()
    expect(within(item).getByText('qwen3.5:9b')).toBeInTheDocument()
    expect(within(item).getByText('7.1 GB')).toBeInTheDocument()

    // Product rule 4: the speed is a RANGE, in the estimated treatment.
    const speed = within(item).getByText(/79–113 tok\/s/)
    expect(speed.closest('.figure')).toHaveAttribute('data-source', 'estimated')
    expect(within(item).getAllByText(/7\.9 GB/)[0].closest('.figure')).toHaveAttribute('data-source', 'estimated')

    // The technical columns stay behind the Advanced toggle.
    expect(screen.queryByTestId('recommend-advanced')).not.toBeInTheDocument()
  })

  it('shows the confidence on every card, with why', async () => {
    serve(() =>
      result({
        recommendations: [
          card(),
          card({ display_name: 'Devstral Small 2 24B', file: { ...card().file, id: 42 }, confidence: 'high', confidence_why: 'Checked against real measurements.' }),
          card({ display_name: 'gpt-oss 20B', file: { ...card().file, id: 43 }, confidence: 'low', confidence_why: 'There is no speed estimate for this computer yet.' }),
        ],
      }),
    )
    open()
    const badges = await screen.findAllByTestId('confidence')
    expect(badges.map((b) => b.textContent)).toEqual([c.confidence.medium, c.confidence.high, c.confidence.low])
    expect(badges[2]).toHaveClass('confidence--low')
    expect(screen.getByText('There is no speed estimate for this computer yet.')).toBeInTheDocument()
  })

  it('says there is no speed estimate in words, never as a number', async () => {
    const noSpeed = card({
      speed: undefined,
      estimate: estimate({ speed: { known: false, unknown: 'no speed estimate: the NVIDIA GeForce RTX 9999 is not in the advisor\'s list of graphics cards yet' } }),
      reasons: [
        { kind: 'fit', text: 'Fits your 24 GB graphics card with room to spare.' },
        { kind: 'speed', text: 'No speed estimate: the NVIDIA GeForce RTX 9999 is not in the advisor\'s list of graphics cards yet.' },
        { kind: 'size', text: 'About 7 GB to download.' },
      ],
      confidence: 'low',
    })
    serve(() => result({ recommendations: [noSpeed] }))
    open()
    const item = await screen.findByRole('article', { name: 'Qwen3.5 9B' })
    expect(within(item).getByText(c.noSpeed)).toBeInTheDocument()
    expect(within(item).queryByText(/tok\/s/)).not.toBeInTheDocument()
    expect(within(item).getByText(/No speed estimate: the NVIDIA GeForce RTX 9999/)).toBeInTheDocument()
  })

  it('puts the graphics-card-not-used reason first, with its explainer', async () => {
    const why = 'Ollama reaches this card through Vulkan, and Vulkan is switched off in Ollama\'s settings on this computer.'
    const first = { kind: 'warning' as const, text: 'Your graphics card is not being used by Ollama; these are the numbers without it.', explainer: 'gpu_not_used', detail: why }
    serve(() =>
      result({
        runtime_path: 'cpu',
        path_source: 'established',
        warning: 'Your graphics card (AMD Radeon RX 6700 XT) is not being used by Ollama; these are the numbers without it.',
        gpu_not_used: { name: 'AMD Radeon RX 6700 XT', kind: 'not_used', why },
        recommendations: [card({ reasons: [first, ...card().reasons] })],
      }),
    )
    open()
    expect(await screen.findByTestId('recommend-warning')).toHaveTextContent('AMD Radeon RX 6700 XT')
    const item = screen.getByRole('article', { name: 'Qwen3.5 9B' })
    const reasons = within(item).getAllByRole('listitem')
    expect(reasons[0]).toHaveTextContent(first.text)
    expect(reasons[0].querySelector('details[data-explainer="gpu_not_used"]')).toHaveTextContent(why)
    expect(within(screen.getByTestId('recommend-warning')).getByText(c.whyNotUsed)).toBeInTheDocument()
  })

  it('asks again when the purposes change, and shows what the model they have is compared with', async () => {
    const calls = serve((url) =>
      url.includes('coding')
        ? result({ purposes: ['chat', 'coding'], current: { name: 'llama3.1:8b', in_catalogue: true, verdict: 'llama3.1:8b runs well on this computer. It is not a model made for coding.' } })
        : result(),
    )
    open()
    await screen.findByRole('article', { name: 'Qwen3.5 9B' })
    await userEvent.click(screen.getByLabelText(c.purposes.coding))
    expect(await screen.findByTestId('recommend-current')).toHaveTextContent('You have llama3.1:8b. llama3.1:8b runs well on this computer.')
    expect(calls.at(-1)).toBe('/api/recommend?purposes=chat%2Ccoding')
  })

  it('says why the list is empty instead of showing nothing', async () => {
    serve(() => result({ recommendations: [], empty: 'The list of models has not been fetched yet, so there is nothing to recommend from.' }))
    open()
    expect(await screen.findByTestId('recommend-empty')).toHaveTextContent('has not been fetched yet')
    expect(screen.queryByRole('article')).not.toBeInTheDocument()
  })

  it('offers to fetch the model list when it has never been fetched, and asks again afterwards', async () => {
    let fetched = false
    const calls = serve(() =>
      fetched
        ? result()
        : result({ recommendations: [], empty: 'The list of models has not been fetched yet, so there is nothing to recommend from.', empty_code: 'catalogue_empty' }),
    )
    const base = globalThis.fetch as unknown as (url: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url === '/api/catalog/status')
          return new Response(JSON.stringify({ fetched, running: false, public_fetched: fetched, public_updated: '' }), { status: 200 })
        if (url === '/api/catalog/refresh' && init?.method === 'POST') {
          fetched = true
          return new Response(JSON.stringify({ sizes: 1, resolved: 1 }), { status: 200 })
        }
        return base(url, init)
      }),
    )
    open()
    // The button says what it will do and what it costs (product rule 5).
    await userEvent.click(await screen.findByRole('button', { name: en.modelList.fetch }))
    expect(await screen.findByRole('article', { name: 'Qwen3.5 9B' })).toBeInTheDocument()
    expect(calls.length).toBe(2)
    // Once fetched: one quiet line, with a way to fetch it again.
    expect(await screen.findByTestId('model-list-fetched')).toHaveTextContent(en.modelList.refetch)
  })

  it('offers the fetch even when the empty answer is about something else (no dead end, whatever the machine)', async () => {
    serve(() => result({ recommendations: [], empty: 'The advisor could not read how much memory this computer can give a model.', empty_code: 'budget_unknown' }))
    const base = globalThis.fetch as unknown as (url: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) =>
        url === '/api/catalog/status'
          ? new Response(JSON.stringify({ fetched: false, running: false, public_fetched: false, public_updated: '' }), { status: 200 })
          : base(url, init),
      ),
    )
    open()
    expect(await screen.findByTestId('model-list-missing')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: en.modelList.fetch })).toBeInTheDocument()
  })

  it('shows the public scores downloading in the background, then asks for the recommendations again', async () => {
    let publicRunning = true
    const calls = serve(() => result())
    const base = globalThis.fetch as unknown as (url: string, init?: RequestInit) => Promise<Response>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string, init?: RequestInit) =>
        url === '/api/catalog/status'
          ? new Response(
              JSON.stringify({
                fetched: true, running: false, public_fetched: false, public_updated: '', public_running: publicRunning,
                public_progress: publicRunning
                  ? { phase: 'public', message: 'Reading public scores from Arena leaderboard dataset (the text leaderboard)', done: 2, total: 3, started_at: new Date().toISOString() }
                  : undefined,
              }),
              { status: 200 },
            )
          : base(url, init),
      ),
    )
    open()
    const note = await screen.findByTestId('public-background')
    expect(note).toHaveTextContent(en.modelList.publicBackground)
    expect(note).toHaveTextContent('Arena leaderboard dataset (the text leaderboard)')
    // The screen is usable meanwhile: the cards are there.
    expect(await screen.findByRole('article', { name: 'Qwen3.5 9B' })).toBeInTheDocument()
    const before = calls.length
    publicRunning = false
    await waitFor(() => expect(calls.length).toBeGreaterThan(before), { timeout: 3000 })
    await waitFor(() => expect(screen.queryByTestId('public-background')).not.toBeInTheDocument())
  })

  it('links each card to a test of that model', async () => {
    serve(() => result())
    open()
    const item = await screen.findByRole('article', { name: 'Qwen3.5 9B' })
    expect(within(item).getByRole('link', { name: c.test })).toHaveAttribute('href', '/benchmarks?model=qwen3.5%3A9b')
  })

  it('shows the daemon\'s message when the request fails', async () => {
    serve(() => ({ status: 503, code: 'detecting', message: 'still reading this computer; try again in a moment' }))
    open()
    expect(await screen.findByRole('alert')).toHaveTextContent('still reading this computer')
  })

  it('shows the memory arithmetic, each term with its explainer, under Advanced', async () => {
    serve(() => result({ path_source: 'established' }))
    open(true)
    const tech = await screen.findByTestId('recommend-advanced')
    const a = c.advanced
    for (const term of [a.weights, a.kvCache, a.overhead, a.total, a.context, a.quantization, a.generation, a.prompt]) {
      expect(within(tech).getByText(term.label)).toBeInTheDocument()
      expect(within(tech).getByText(term.explain)).toBeInTheDocument()
    }
    expect(within(tech).getByText(/at or under 80% fits with headroom/)).toBeInTheDocument()
    expect(within(tech).getByText(`cuda (${a.pathEstablished})`)).toBeInTheDocument()
    expect(within(tech).getByText(/hybrid attention/)).toBeInTheDocument()
    // Every number in the table that has provenance shows it.
    for (const fig of tech.querySelectorAll('.figure')) expect(fig).toHaveAttribute('data-source', 'estimated')
  })
  it('shows a public line below and apart from the reasons, with its source and date, and a link to the details', async () => {
    serve(() => result({ recommendations: [card({ public: publicEntry() })] }))
    open()
    const item = await screen.findByRole('article', { name: 'Qwen3.5 9B' })
    const line = within(item).getByTestId('public-line')
    expect(line).toHaveTextContent(en.publicData.cardTitle)
    expect(line).toHaveTextContent('Among the strongest for everyday chat')
    expect(line).toHaveTextContent('Arena (arena.ai), 15 September 2026.')
    // Not a reason, and not a local figure.
    expect(item.querySelector('.card__reasons')).not.toHaveTextContent('Arena')
    expect(line.querySelector('.figure')).toBeNull()
    expect(within(item).getByRole('link', { name: c.details })).toHaveAttribute('href', '/models/4')
  })

  it('shows no public line when there is no public value', async () => {
    serve(() => result())
    open()
    const item = await screen.findByRole('article', { name: 'Qwen3.5 9B' })
    expect(within(item).queryByTestId('public-line')).toBeNull()
  })
})
