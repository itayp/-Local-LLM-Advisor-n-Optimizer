import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { Settings } from '../state/settings'
import type { WatchLogResponse, WatchReport, WatchRunSummary } from '../api/types'
import { en } from '../copy/en'

const c = en.screens.watch

function json(v: unknown, status = 200) {
  return new Response(JSON.stringify(v), { status })
}

function report(over: Partial<WatchReport> = {}): WatchReport {
  return {
    started_at: '2026-09-25T08:00:00Z',
    finished_at: '2026-09-25T08:00:05Z',
    trigger: 'scheduler',
    checked: 1,
    notified: 0,
    suppressed: 1,
    already_seen: 0,
    flagged: 0,
    errors: 0,
    entries: [],
    ...over,
  }
}

function run(over: Partial<WatchRunSummary> = {}): WatchRunSummary {
  const rep = over.report ?? report()
  return {
    id: 1,
    created_at: rep.started_at,
    finished_at: rep.finished_at,
    trigger: rep.trigger,
    checked: rep.checked,
    notified: rep.notified,
    suppressed: rep.suppressed,
    flagged: rep.flagged,
    ...over,
    report: rep,
  }
}

function serve(opts: { log?: WatchLogResponse; runFails?: 'busy' | 'error'; settings?: Partial<Settings> }) {
  const calls: { url: string; method: string; body?: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body as string | undefined })
      if (url === '/api/watch/log' && method === 'GET') return json(opts.log ?? { runs: [] })
      if (url === '/api/watch/run' && method === 'POST') {
        if (opts.runFails === 'busy') return json({ error: { code: 'watch_running', message: 'already running' } }, 409)
        if (opts.runFails === 'error') return json({ error: { code: 'watch_failed', message: 'boom' } }, 500)
        return json(report())
      }
      if (url === '/api/settings' && method === 'GET') {
        return json({ advanced: false, data_dir: '/tmp', purposes: [], watch: { enabled: true, mode: 'on', interval: 0 }, ...opts.settings })
      }
      return json({})
    }),
  )
  return calls
}

function open(settings?: Partial<Settings>) {
  return render(
    <MemoryRouter initialEntries={['/watch']}>
      <App initialSettings={settings} />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Watch', () => {
  it('writes an empty state when nothing has run yet', async () => {
    serve({})
    open()
    expect(await screen.findByTestId('watch-empty')).toHaveTextContent(c.empty)
  })

  it('lists every entry from the most recent runs, newest first', async () => {
    const entries = [
      { at: '2026-09-25T08:00:00Z', key: 'model:1', name: 'Qwen3.5 8B', outcome: 'notified' as const, detail: 'faster than what you have' },
      { at: '2026-09-25T08:00:01Z', key: 'model:2', name: 'Qwen3.5 70B', outcome: 'suppressed' as const, detail: 'does not fit this computer' },
      { at: '2026-09-25T08:00:02Z', key: 'repo:Acme/new', name: 'Acme/new', outcome: 'flagged_for_curator' as const, detail: 'a new repo from Acme, not yet in the catalogue' },
    ]
    serve({ log: { runs: [run({ report: report({ checked: 2, notified: 1, flagged: 1, entries }) })] } })
    open()

    const table = await screen.findByTestId('watch-table')
    expect(table).toHaveTextContent('Qwen3.5 8B')
    expect(table).toHaveTextContent(c.outcome.notified)
    expect(table).toHaveTextContent('faster than what you have')
    expect(table).toHaveTextContent(c.outcome.suppressed)
    expect(table).toHaveTextContent(c.outcome.flagged_for_curator)
  })

  it('surfaces a refresh error from the latest run without hiding its entries', async () => {
    const entries = [{ at: '2026-09-25T08:00:00Z', key: 'model:1', name: 'Test 8B', outcome: 'suppressed' as const, detail: 'not resolved against Hugging Face yet' }]
    serve({ log: { runs: [run({ report: report({ entries, refresh_error: 'Hugging Face could not be reached' }) })] } })
    open()
    expect(await screen.findByText(c.refreshError('Hugging Face could not be reached'))).toBeInTheDocument()
    expect(await screen.findByTestId('watch-table')).toHaveTextContent('Test 8B')
  })

  it('runs a check on request and reloads the log', async () => {
    const user = userEvent.setup()
    const calls = serve({ log: { runs: [] } })
    open()
    await screen.findByTestId('watch-empty')

    await user.click(screen.getByRole('button', { name: c.checkNow }))
    await waitFor(() => expect(calls.some((x) => x.url === '/api/watch/run' && x.method === 'POST')).toBe(true))
    await waitFor(() => expect(calls.filter((x) => x.url === '/api/watch/log').length).toBeGreaterThan(1))
  })

  it('says a check is already running rather than failing silently', async () => {
    const user = userEvent.setup()
    serve({ log: { runs: [] }, runFails: 'busy' })
    open()
    await screen.findByTestId('watch-empty')
    await user.click(screen.getByRole('button', { name: c.checkNow }))
    expect(await screen.findByText(c.running)).toBeInTheDocument()
  })

  it('shows the pause notice when the watch is turned off in Settings', async () => {
    serve({ log: { runs: [] }, settings: { watch: { enabled: false, mode: 'never', interval: 0 } } })
    open({ watch: { enabled: false, mode: 'never', interval: 0 } })
    expect(await screen.findByText(c.pausedNotice)).toBeInTheDocument()
  })
})
