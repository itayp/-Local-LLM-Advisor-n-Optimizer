import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { App } from './App'
import { en } from './copy/en'
import { screens } from './screens'

function renderAt(path: string, opts?: { advanced?: boolean }) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App initialSettings={opts?.advanced === undefined ? undefined : { advanced: opts.advanced }} />
    </MemoryRouter>,
  )
}

describe('the shell', () => {
  it('lists every screen of the PRD §17 workflow in the navigation', () => {
    renderAt('/')
    const nav = screen.getByRole('navigation', { name: 'Main' })
    const links = within(nav).getAllByRole('link')
    // A screen reached from another one (a model's detail view) is routed, not listed.
    const listed = screens.filter((s) => !s.hidden)
    expect(links.map((l) => l.textContent)).toEqual(listed.map((s) => s.label))
    expect(links.map((l) => l.getAttribute('href'))).toEqual(listed.map((s) => s.path))
  })

  it.each(screens.map((s) => [s.label, s.path]))('shows the %s screen at %s', (label, path) => {
    renderAt(path)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(label)
  })

  it('shows the daemon version from /api/health', async () => {
    renderAt('/')
    expect(await screen.findByTestId('daemon-version')).toHaveTextContent('version test')
    expect(fetch).toHaveBeenCalledWith('/api/health', expect.anything())
  })

  it('says so, in words, when the daemon cannot be reached', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    renderAt('/')
    expect(await screen.findByText(en.app.daemonUnreachable)).toBeInTheDocument()
  })
})

describe('the Advanced toggle', () => {
  it('is off by default (product rule 2)', () => {
    renderAt('/settings')
    expect(screen.getByRole('checkbox', { name: en.screens.settings.advancedLabel })).not.toBeChecked()
    expect(screen.queryByTestId('advanced-badge')).not.toBeInTheDocument()
  })

  it('flips the settings state for every screen and persists it', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        if (url === '/api/models/installed') {
          return new Response(
            JSON.stringify({
              models: [
                { backend_name: 'ollama', name: 'llama3:8b', size_bytes: 1e9, quantization: 'Q4_K_M', last_seen_at: '2026-01-01T00:00:00Z', catalog_match: '' },
              ],
            }),
            { status: 200 },
          )
        }
        return new Response(JSON.stringify({ version: 'test', os: 'testos', arch: 'testarch', go_version: 'go' }), { status: 200 })
      }),
    )
    const user = userEvent.setup()
    renderAt('/settings')
    await user.click(screen.getByRole('checkbox', { name: en.screens.settings.advancedLabel }))
    expect(screen.getByTestId('advanced-badge')).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: en.nav.models }))
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(en.nav.models)
    expect(await screen.findByTestId('models-advanced')).toBeInTheDocument()

    expect(JSON.parse(localStorage.getItem('advisor.settings.v1') ?? '{}')).toEqual({
      advanced: true,
      purposes: [],
      watch: { enabled: true, mode: 'quiet', interval: 0 },
    })
  })

  it('reads a saved choice on start', () => {
    localStorage.setItem('advisor.settings.v1', JSON.stringify({ advanced: true }))
    renderAt('/models')
    expect(screen.getByTestId('advanced-badge')).toBeInTheDocument()
  })
})
