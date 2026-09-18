import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { HardwareResponse } from '../api/types'
import { en } from '../copy/en'

const c = en.screens.computer

function response(over: Partial<HardwareResponse['profile']> = {}, changed = false): HardwareResponse {
  return {
    profile_id: 7,
    detected_at: '2026-09-18T13:00:00Z',
    fingerprint: 'v1-abc',
    changed,
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
      summary: 'A Mac laptop with an Apple M1 Pro chip and 16 GB of memory, of which the graphics can use 11.8 GB: small and medium-sized models run on the chip\'s graphics.',
      expectations_from: 'Ollama v0.34.2',
      ...over,
    },
  }
}

function serve(hw: HardwareResponse | { status: number; code: string; message: string }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/api/hardware') {
        if ('status' in hw) {
          return new Response(JSON.stringify({ error: { code: hw.code, message: hw.message } }), { status: hw.status })
        }
        return new Response(JSON.stringify(hw), { status: 200 })
      }
      return new Response(JSON.stringify({ version: 'test', os: 'darwin', arch: 'arm64', go_version: 'go' }), { status: 200 })
    }),
  )
}

function renderComputer(advanced = false) {
  return render(
    <MemoryRouter initialEntries={['/computer']}>
      <App initialSettings={{ advanced }} />
    </MemoryRouter>,
  )
}

describe('Your computer', () => {
  it('leads with the sentence the daemon wrote, then the facts in plain words', async () => {
    serve(response())
    renderComputer()
    expect(await screen.findByTestId('hardware-summary')).toHaveTextContent('Apple M1 Pro chip and 16 GB of memory')
    const facts = screen.getByLabelText(c.factsLabel)
    expect(within(facts).getByText('16 GB, of which the graphics can use 11.8 GB')).toBeInTheDocument()
    expect(within(facts).getByText('Apple M1 Pro (10 cores)')).toBeInTheDocument()
    expect(within(facts).getByText('200 GB free')).toBeInTheDocument()
    expect(screen.queryByTestId('hardware-technical')).not.toBeInTheDocument()
  })

  it('says unknown in words, never 0 GB', async () => {
    serve(
      response({
        ram_bytes: 0,
        ram_known: false,
        unified_memory: false,
        gpu_usable_bytes: 0,
        gpu_usable_known: false,
        storage: { models_dir: 'unknown', models_dir_source: '', models_dir_exists: false, free_bytes: 0, free_known: false },
        gpus: [
          {
            vendor: 'amd',
            name: 'AMD Radeon RX 7900 XTX',
            vram_bytes: 0,
            vram_known: false,
            vram_source: 'unknown',
            driver_version: 'unknown',
            is_integrated: false,
            integrated_known: true,
            expected_backend: 'rocm',
            expected_backend_reason: 'why',
            expected_backend_rule: 'amd-rocm-windows',
          },
        ],
        problems: ['the registry has no HardwareInformation.qwMemorySize for this adapter'],
      }),
    )
    renderComputer()
    const facts = await screen.findByLabelText(c.factsLabel)
    expect(facts).not.toHaveTextContent(/\b0 (GB|MB|B)\b/)
    expect(within(facts).getByText(`AMD Radeon RX 7900 XTX — ${c.gpuMemoryUnknown}`)).toBeInTheDocument()
    expect(within(facts).getAllByText(c.unknown).length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText(c.showDetails)).toBeInTheDocument()
  })

  it('tells the user when the hardware changed since last time', async () => {
    serve(response({}, true))
    renderComputer()
    expect(await screen.findByText(c.changed)).toBeInTheDocument()
  })

  it('shows the technical columns only with Advanced on', async () => {
    serve(response())
    renderComputer(true)
    const tech = await screen.findByTestId('hardware-technical')
    expect(within(tech).getByText('metal')).toBeInTheDocument()
    expect(within(tech).getByText(c.advanced.vectorArm)).toBeInTheDocument()
    expect(within(tech).getByText(/Ollama v0\.34\.2/)).toBeInTheDocument()
  })

  it('says, in words, when the computer could not be read', async () => {
    serve({ status: 503, code: 'detection_failed', message: 'this computer could not be read: context canceled' })
    renderComputer()
    expect(await screen.findByRole('alert')).toHaveTextContent('could not be read')
  })
})
