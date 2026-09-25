import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SpeedNeedsResponse } from '../api/types'
import { en } from '../copy/en'
import { glossary } from '../copy/glossary'

// Term's tokens_per_sec extra content caches GET /api/speed-needs at module
// scope (one fetch no matter how many <Term> instances a page has), so each
// test resets the module registry and imports Term fresh — otherwise a
// later test would see the first test's cached response.
async function freshTerm() {
  vi.resetModules()
  const mod = await import('./Term')
  return mod.Term
}

function json(v: unknown, status = 200) {
  return new Response(JSON.stringify(v), { status })
}

const fixture: SpeedNeedsResponse = {
  words_per_token: 0.75,
  purposes: [
    { purpose: 'chat', mode: 'read_along', stream: { excellent: 14, good: 7, usable: 4 }, wait_s: { excellent: 1, good: 4, usable: 10 } },
    { purpose: 'agentic', mode: 'per_step', wait_s: { excellent: 10, good: 30, usable: 120 } },
  ],
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Term', () => {
  it('shows the one-line explainer for an ordinary term without fetching anything', async () => {
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        calls.push(url)
        return json({})
      }),
    )
    const Term = await freshTerm()
    const user = userEvent.setup()
    render(<Term id="vram" />)
    await user.click(screen.getByRole('button', { name: /what is vram/i }))
    expect(await screen.findByRole('note')).toHaveTextContent(glossary.vram.explain)
    expect(calls).not.toContain('/api/speed-needs')
  })

  it('does not read the speed table until the tokens_per_sec explainer is opened', async () => {
    const calls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        calls.push(url)
        return json(fixture)
      }),
    )
    const Term = await freshTerm()
    render(<Term id="tokens_per_sec" />)
    expect(calls).not.toContain('/api/speed-needs')
  })

  it('shows what a speed is good for, per purpose, once opened — numbers from the API, not the copy', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(fixture)))
    const Term = await freshTerm()
    const user = userEvent.setup()
    render(<Term id="tokens_per_sec" />)
    await user.click(screen.getByRole('button', { name: /what is tok\/s/i }))

    const panel = await screen.findByTestId('speed-needs')
    expect(panel).toHaveTextContent(en.speedNeeds.heading)
    expect(panel).toHaveTextContent(en.speedNeeds.intro)

    const chatLabel = en.screens.recommend.purposes.chat
    expect(panel).toHaveTextContent(en.speedNeeds.streamLine(chatLabel, 14 * 0.75, 7 * 0.75, 4 * 0.75))
    expect(panel).toHaveTextContent(en.speedNeeds.waitLine(chatLabel, 1, 4, 10))

    const agenticLabel = en.screens.recommend.purposes.agentic
    expect(panel).toHaveTextContent(en.speedNeeds.stepLine(agenticLabel, 10, 30, 120))
    // agentic has no stream bar (per_step): its wait line reads as a whole
    // step, never as an answer streaming in.
    expect(panel).not.toHaveTextContent(en.speedNeeds.waitLine(agenticLabel, 10, 30, 120))
  })

  it('says the table could not be read, rather than showing nothing', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: { code: 'speed_needs', message: 'boom' } }, 500)))
    const Term = await freshTerm()
    const user = userEvent.setup()
    render(<Term id="tokens_per_sec" />)
    await user.click(screen.getByRole('button', { name: /what is tok\/s/i }))
    expect(await screen.findByText(en.speedNeeds.failed)).toBeInTheDocument()
  })
})
