import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { BackendInfo } from '../api/types'
import { en } from '../copy/en'

const c = en.screens.ollama

function serve(backend: BackendInfo) {
  const calls: { url: string; method: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method ?? 'GET' })
      const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status })
      if (url === '/api/health') return json({ version: 'test', os: 'darwin', arch: 'arm64', go_version: 'go' })
      if (url === '/api/backends') return json({ backends: [backend] })
      if (url.endsWith('/install-size')) return json({ bytes: 120e6, known: true })
      if (url.endsWith('/start')) return json({ backend: backend.name, state: 'running' })
      return json({ error: { code: 'not_found', message: 'no' } }, 404)
    }),
  )
  return calls
}

function open() {
  return render(
    <MemoryRouter initialEntries={['/ollama']}>
      <App />
    </MemoryRouter>,
  )
}

describe('Ollama', () => {
  it('shows whether Ollama is running, and its version', async () => {
    serve({ name: 'ollama', state: 'running', version: '0.34.2', checked_at: '2026-09-25T10:00:00Z' })
    open()
    const status = await screen.findByTestId('ollama-status')
    expect(within(status).getByText(c.states.running)).toBeInTheDocument()
    expect(within(status).getByText('0.34.2')).toBeInTheDocument()
    expect(screen.queryByTestId('next-start')).not.toBeInTheDocument()
    expect(screen.queryByTestId('next-install')).not.toBeInTheDocument()
  })

  it('offers the start button when it is installed but not running', async () => {
    const calls = serve({ name: 'ollama', state: 'installed_not_running', installed_version: '0.34.2', checked_at: '2026-09-25T10:00:00Z' })
    open()
    expect(await screen.findByText(c.states.installed_not_running)).toBeInTheDocument()
    await userEvent.click(within(screen.getByTestId('next-start')).getByRole('button', { name: en.screens.home.next.start }))
    expect(calls.some((x) => x.method === 'POST' && x.url.endsWith('/start'))).toBe(true)
  })

  it('offers the install button, with its size, when it is not installed', async () => {
    serve({ name: 'ollama', state: 'not_installed', checked_at: '2026-09-25T10:00:00Z' })
    open()
    expect(await screen.findByText(c.states.not_installed)).toBeInTheDocument()
    expect(await within(screen.getByTestId('next-install')).findByRole('button', { name: /120 MB/ })).toBeInTheDocument()
  })
})
