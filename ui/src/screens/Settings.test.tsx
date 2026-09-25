import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { HardwareResponse, SettingsResponse } from '../api/types'
import { en } from '../copy/en'

const c = en.screens.settings

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
      gpus: [],
      unified_memory: true,
      gpu_usable_bytes: 12713115648,
      gpu_usable_known: true,
      gpu_usable_source: 'Metal recommendedMaxWorkingSetSize',
      storage: { models_dir: '/Users/u/.ollama/models', models_dir_source: 'default', models_dir_exists: true, free_bytes: 200 * 1024 ** 3, free_known: true },
      is_laptop: true,
      laptop_known: true,
      tier: 'gpu_medium',
      summary: 'A Mac laptop.',
      expectations_from: 'Ollama v0.34.2',
    },
  }
}

function settings(over: Partial<SettingsResponse> = {}): SettingsResponse {
  return {
    advanced: false,
    data_dir: '/Users/u/Library/Application Support/advisor',
    purposes: [],
    watch: { enabled: true, mode: 'on', interval: 0 },
    ...over,
  }
}

function serve(opts: { hw?: HardwareResponse | 'unreachable'; settings?: SettingsResponse; openDataFails?: string; openModelsFails?: string }) {
  const calls: { url: string; method: string; body?: string }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body as string | undefined })
      if (url === '/api/hardware') {
        if (opts.hw === 'unreachable') throw new TypeError('Failed to fetch')
        return json(opts.hw ?? hardware())
      }
      if (url === '/api/settings' && method === 'GET') return json(opts.settings ?? settings())
      if (url === '/api/settings' && method === 'PUT') return json({ ...(opts.settings ?? settings()), ...JSON.parse((init?.body as string) ?? '{}') })
      if (url === '/api/settings/open-data-dir' && method === 'POST') {
        if (opts.openDataFails) return json({ error: { code: 'open_failed', message: opts.openDataFails } }, 500)
        return json({})
      }
      if (url === '/api/settings/open-models-dir' && method === 'POST') {
        if (opts.openModelsFails) return json({ error: { code: 'open_failed', message: opts.openModelsFails } }, 500)
        return json({})
      }
      return json({ version: '1.4.0', os: 'darwin', arch: 'arm64', go_version: 'go1.27.1' })
    }),
  )
  return calls
}

function open() {
  return render(
    <MemoryRouter initialEntries={['/settings']}>
      <App />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('Settings', () => {
  it('is off by default and flips the daemon-wide setting through PUT /api/settings', async () => {
    const user = userEvent.setup()
    const calls = serve({})
    open()
    const checkbox = await screen.findByRole('checkbox', { name: c.advancedLabel })
    expect(checkbox).not.toBeChecked()
    await user.click(checkbox)
    expect(checkbox).toBeChecked()
    await waitFor(() => expect(calls.some((x) => x.url === '/api/settings' && x.method === 'PUT')).toBe(true))
    const put = calls.find((x) => x.url === '/api/settings' && x.method === 'PUT')
    expect(JSON.parse(put?.body ?? '{}')).toEqual({ advanced: true })
  })

  it('shows the models folder, its free space, and the data folder', async () => {
    serve({})
    open()
    const modelsRow = (await screen.findByText(c.modelsFolder)).closest('.settings-row') as HTMLElement
    expect(within(modelsRow).getByText(/\/Users\/u\/\.ollama\/models/)).toBeInTheDocument()
    expect(within(modelsRow).getByText(/200 GB free/)).toBeInTheDocument()
    const dataRow = screen.getByText(c.dataFolder).closest('.settings-row') as HTMLElement
    expect(within(dataRow).getByText('/Users/u/Library/Application Support/advisor')).toBeInTheDocument()
  })

  it("says the folder could not be read yet when this computer hasn't been read", async () => {
    serve({ hw: 'unreachable' })
    open()
    const modelsRow = (await screen.findByText(c.modelsFolder)).closest('.settings-row') as HTMLElement
    expect(await within(modelsRow).findByText(new RegExp(c.modelsFolderUnknown))).toBeInTheDocument()
  })

  it('shows the daemon version and platform', async () => {
    serve({})
    open()
    const dt = await screen.findByText(en.app.title, { selector: 'dt' })
    const row = dt.closest('.settings-row') as HTMLElement
    expect(within(row).getByText(`${c.version('1.4.0')} · ${c.versionPlatform('darwin', 'arm64')}`)).toBeInTheDocument()
  })

  it('opens the models folder on request, and reports failure in words', async () => {
    const user = userEvent.setup()
    const calls = serve({ openModelsFails: 'the folder no longer exists' })
    open()
    const row = (await screen.findByText(c.modelsFolder)).closest('.settings-row') as HTMLElement
    await within(row).findByText(/\/Users\/u\/\.ollama\/models/)
    await user.click(within(row).getByRole('button', { name: c.open }))
    await waitFor(() => expect(calls.some((x) => x.url === '/api/settings/open-models-dir' && x.method === 'POST')).toBe(true))
    expect(await within(row).findByText(c.openFailed('the folder no longer exists'))).toBeInTheDocument()
  })

  it('opens the data folder on request', async () => {
    const user = userEvent.setup()
    const calls = serve({})
    open()
    await screen.findByText('/Users/u/Library/Application Support/advisor')
    const row = screen.getByText(c.dataFolder).closest('.settings-row') as HTMLElement
    await user.click(within(row).getByRole('button', { name: c.open }))
    await waitFor(() => expect(calls.some((x) => x.url === '/api/settings/open-data-dir' && x.method === 'POST')).toBe(true))
    expect(within(row).queryByRole('alert')).not.toBeInTheDocument()
  })

  it('writes what updates will be, rather than leaving it blank', async () => {
    serve({})
    open()
    expect(await screen.findByText(en.placeholder.comingIn(c.updatesStep))).toBeInTheDocument()
  })

  it('shows the watch settings and persists a change to each', async () => {
    const user = userEvent.setup()
    const calls = serve({})
    open()
    await screen.findByText('/Users/u/Library/Application Support/advisor')

    const enabled = screen.getByRole('checkbox', { name: c.watchEnabledLabel }) as HTMLInputElement
    expect(enabled.checked).toBe(true)
    const onMode = screen.getByRole('radio', { name: c.watchMode.on }) as HTMLInputElement
    expect(onMode.checked).toBe(true)

    await user.click(screen.getByRole('radio', { name: c.watchMode.quiet }))
    await waitFor(() =>
      expect(
        calls.some((x) => x.url === '/api/settings' && x.method === 'PUT' && JSON.parse(x.body ?? '{}').watch?.mode === 'quiet'),
      ).toBe(true),
    )

    await user.click(enabled)
    await waitFor(() =>
      expect(
        calls.some((x) => x.url === '/api/settings' && x.method === 'PUT' && JSON.parse(x.body ?? '{}').watch?.enabled === false),
      ).toBe(true),
    )
  })
})
