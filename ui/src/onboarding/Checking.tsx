import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { HardwareResponse } from '../api/types'
import { formatBytes } from '../components/Figure'
import { en } from '../copy/en'

const c = en.onboarding.checking
const tierLabel = en.onboarding.tier

/**
 * Screen 2: the hardware profile as one sentence plus its tier (product
 * rule 6 — a weak machine is a tier, worded plainly, not an error), with
 * the facts behind it collapsed under "Show details". Every number here
 * is read straight from the OS, not estimated or measured, so it is
 * plain text rather than a <Figure> — same treatment as the full
 * Computer screen (step 2) this borrows its data from.
 */
export function Checking({ onNext }: { onNext: () => void }) {
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
    <section className="onboarding__step" aria-labelledby="onboarding-title">
      <h1 id="onboarding-title">{c.title}</h1>
      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : !data ? (
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      ) : (
        <>
          <p className="screen__lead" data-testid="onboarding-hardware-summary">
            {data.profile.summary}
          </p>
          <p className="badge" data-testid="onboarding-tier">
            {tierLabel[data.profile.tier]}
          </p>
          <details>
            <summary>{c.showDetails}</summary>
            <dl className="facts">
              <dt>{c.graphics}</dt>
              <dd>
                {data.profile.gpus.length === 0
                  ? c.noGraphics
                  : data.profile.gpus.map((g) => g.name).join(', ')}
              </dd>
              <dt>{c.memory}</dt>
              <dd>{data.profile.ram_known ? formatBytes(data.profile.ram_bytes) : c.unknown}</dd>
              <dt>{c.processor}</dt>
              <dd>{data.profile.cpu.model}</dd>
            </dl>
          </details>
          <button type="button" className="button" onClick={onNext}>
            {c.continue}
          </button>
        </>
      )}
    </section>
  )
}
