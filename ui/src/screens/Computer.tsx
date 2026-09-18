import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { GPU, HardwareProfile, HardwareResponse } from '../api/types'
import { formatBytes } from '../components/Figure'
import { en } from '../copy/en'
import { useAdvanced } from '../state/settings'

const c = en.screens.computer

/**
 * Your computer: the one-sentence answer the daemon derives (tier +
 * summary), the facts behind it in plain words, and — behind "Show
 * details" and the Advanced toggle — what could not be read and the
 * technical columns. Numbers here are read from the OS (not estimated, not
 * measured), so they are plain text; unknown values are words, never 0 GB.
 */
export function Computer() {
  const advanced = useAdvanced()
  const [data, setData] = useState<HardwareResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    api
      .hardware(ac.signal)
      .then(setData)
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => ac.abort()
  }, [])

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>
      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : !data ? (
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      ) : (
        <Profile data={data} advanced={advanced} />
      )}
    </section>
  )
}

function Profile({ data, advanced }: { data: HardwareResponse; advanced: boolean }) {
  const p = data.profile
  return (
    <>
      <p className="screen__lead" data-testid="hardware-summary">
        {p.summary}
      </p>
      {data.changed ? <p className="notice">{c.changed}</p> : null}
      {p.notes?.length ? (
        <ul className="notes">
          {p.notes.map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ) : null}

      <dl className="facts" aria-label={c.factsLabel}>
        <dt>{c.graphics}</dt>
        <dd>
          {p.gpus.length === 0 ? (
            c.noGraphics
          ) : (
            <ul>
              {p.gpus.map((g, i) => (
                <li key={`${g.name}-${i}`}>{gpuLine(g)}</li>
              ))}
            </ul>
          )}
        </dd>
        <dt>{c.memory}</dt>
        <dd>{memoryLine(p)}</dd>
        <dt>{c.processor}</dt>
        <dd>{processorLine(p)}</dd>
        <dt>{c.modelsSpace}</dt>
        <dd>
          {p.storage.free_known ? c.freeSpace(formatBytes(p.storage.free_bytes)) : c.unknown}
          {p.storage.free_known && !p.storage.models_dir_exists ? (
            <span className="facts__aside"> — {c.folderNotYet}</span>
          ) : null}
        </dd>
        <dt>{c.system}</dt>
        <dd>{p.os_version === 'unknown' ? c.unknown : p.os_version}</dd>
      </dl>

      {p.problems?.length || p.filtered_adapters?.length ? (
        <details className="details">
          <summary>{c.showDetails}</summary>
          {p.problems?.length ? (
            <>
              <h2>{c.problemsTitle}</h2>
              <ul>
                {p.problems.map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </>
          ) : null}
          {p.filtered_adapters?.length ? (
            <>
              <h2>{c.filteredTitle}</h2>
              <ul>
                {p.filtered_adapters.map((x) => (
                  <li key={x}>{x}</li>
                ))}
              </ul>
            </>
          ) : null}
        </details>
      ) : null}

      {advanced ? <Technical data={data} /> : null}
    </>
  )
}

function gpuLine(g: GPU): string {
  const parts = [g.name]
  if (g.is_integrated && g.vendor !== 'apple') parts.push(c.builtIn)
  else if (g.vendor !== 'apple') parts.push(g.vram_known ? c.gpuMemory(formatBytes(g.vram_bytes)) : c.gpuMemoryUnknown)
  if (g.expected_backend === 'none') parts.push(c.notUsable)
  return parts.join(' — ')
}

function memoryLine(p: HardwareProfile): string {
  if (!p.ram_known) return c.unknown
  const total = formatBytes(p.ram_bytes)
  if (!p.unified_memory) return total
  return p.gpu_usable_known ? c.unifiedShare(total, formatBytes(p.gpu_usable_bytes)) : c.unifiedShareUnknown(total)
}

function processorLine(p: HardwareProfile): string {
  const model = p.cpu.model === 'unknown' ? c.unknown : p.cpu.model
  const cores = c.cores(p.cpu.cores_physical, p.cpu.cores_logical)
  return cores ? `${model} (${cores})` : model
}

function Technical({ data }: { data: HardwareResponse }) {
  const p = data.profile
  const a = c.advanced
  const vector = !p.cpu.vector_known
    ? c.unknown
    : p.arch === 'arm64' || p.arch === 'arm'
      ? a.vectorArm
      : a.vectorValue(p.cpu.has_avx2, p.cpu.has_avx512)
  return (
    <div className="screen__advanced" data-testid="hardware-technical">
      <h2>{a.title}</h2>
      {p.gpus.length ? (
        <table className="tech">
          <thead>
            <tr>
              <th>{a.device}</th>
              <th>{a.path}</th>
              <th>{a.why}</th>
              <th>{a.driver}</th>
              <th>{a.memorySource}</th>
              <th>{a.ids}</th>
            </tr>
          </thead>
          <tbody>
            {p.gpus.map((g, i) => (
              <tr key={`${g.name}-${i}`}>
                <td>{g.name}</td>
                <td>{g.expected_backend}</td>
                <td>
                  {g.expected_backend_reason} <span className="tech__rule">({g.expected_backend_rule})</span>
                  {g.note ? <div>{g.note}</div> : null}
                </td>
                <td>{[g.driver_version, g.linux_driver].filter(Boolean).join(' · ')}</td>
                <td>{g.vram_source}</td>
                <td>{[g.pci_id, g.compute_capability && `sm ${g.compute_capability}`, g.gfx_target].filter(Boolean).join(' · ')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      <dl className="facts">
        <dt>{a.vector}</dt>
        <dd>{vector}</dd>
        <dt>{a.budget}</dt>
        <dd>{p.gpu_usable_source}</dd>
        <dt>{a.modelsFolder}</dt>
        <dd>
          {p.storage.models_dir} ({p.storage.models_dir_source})
        </dd>
        <dt>{a.expectations}</dt>
        <dd>
          {p.expectations_from} · {a.profile(data.profile_id, data.fingerprint)}
        </dd>
      </dl>
    </div>
  )
}
