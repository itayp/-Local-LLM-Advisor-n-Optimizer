import { useEffect, useState, type ReactNode } from 'react'
import { api } from '../api/client'
import type { SpeedNeedsResponse } from '../api/types'
import type { GlossaryTermId } from '../copy/glossary'
import { glossary } from '../copy/glossary'
import { en } from '../copy/en'

// The tokens_per_sec explainer's extra content (backlog item (b)) is read
// once and shared by every <Term id="tokens_per_sec">, however many the
// page has open — GET /api/speed-needs is the same curated table on every
// call, so there is nothing to invalidate.
let speedNeedsCache: Promise<SpeedNeedsResponse> | null = null
function loadSpeedNeeds(): Promise<SpeedNeedsResponse> {
  if (!speedNeedsCache) speedNeedsCache = api.speedNeeds()
  return speedNeedsCache
}

/**
 * Term shows a technical word with its one-line explainer a tap away
 * (CLAUDE.md rule 2: no term from the glossary list appears without one).
 * Written once per term in copy/glossary.ts; a screen using a glossary
 * term renders it through this component instead of restating the
 * explanation itself.
 *
 * A toggle button, not <details>/<summary>: Term appears inline inside
 * ordinary sentences (a <p>), and <details> is not valid content there —
 * a button and a span are. For the same reason every node this renders,
 * including tokens_per_sec's extra table, stays phrasing content (<span>,
 * never <p>/<div>/<ul>): CSS gives the rows their own line, so the markup
 * remains valid wherever a screen drops <Term> into a sentence.
 */
export function Term({ id, children }: { id: GlossaryTermId; children?: ReactNode }) {
  const g = glossary[id]
  const [open, setOpen] = useState(false)
  const [speedNeeds, setSpeedNeeds] = useState<SpeedNeedsResponse | null>(null)
  const [speedNeedsFailed, setSpeedNeedsFailed] = useState(false)

  useEffect(() => {
    if (id !== 'tokens_per_sec' || !open || speedNeeds || speedNeedsFailed) return
    let cancelled = false
    loadSpeedNeeds()
      .then((r) => {
        if (!cancelled) setSpeedNeeds(r)
      })
      .catch(() => {
        if (!cancelled) setSpeedNeedsFailed(true)
      })
    return () => {
      cancelled = true
    }
  }, [id, open, speedNeeds, speedNeedsFailed])

  return (
    <span className="term">
      {children ?? g.term}
      <button
        type="button"
        className="term__toggle"
        aria-label={`What is ${g.term}?`}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        ?
      </button>
      {open ? (
        <span className="term__explain" role="note">
          {g.explain}
          {id === 'tokens_per_sec' ? <SpeedNeedsExplainer data={speedNeeds} failed={speedNeedsFailed} /> : null}
        </span>
      ) : null}
    </span>
  )
}

/** What a speed is good for, per purpose (GET /api/speed-needs, D-58). */
function SpeedNeedsExplainer({ data, failed }: { data: SpeedNeedsResponse | null; failed: boolean }) {
  const c = en.speedNeeds
  const purposeLabel = en.screens.recommend.purposes as Record<string, string>
  if (failed) return <span className="term__speedneeds-note">{c.failed}</span>
  if (!data) return <span className="term__speedneeds-note">{c.loading}</span>
  return (
    <span className="term__speedneeds" data-testid="speed-needs">
      <span className="term__speedneeds-heading">{c.heading}</span>
      <span className="term__speedneeds-intro">{c.intro}</span>
      {data.purposes.map((row) => {
        const label = purposeLabel[row.purpose] ?? row.purpose
        return (
          <span className="term__speedneeds-row" key={row.purpose}>
            {row.stream ? (
              <>
                <span className="term__speedneeds-line">
                  {c.streamLine(
                    label,
                    row.stream.excellent * data.words_per_token,
                    row.stream.good * data.words_per_token,
                    row.stream.usable * data.words_per_token,
                  )}
                </span>
                <span className="term__speedneeds-line">{c.waitLine(label, row.wait_s.excellent, row.wait_s.good, row.wait_s.usable)}</span>
              </>
            ) : (
              <span className="term__speedneeds-line">{c.stepLine(label, row.wait_s.excellent, row.wait_s.good, row.wait_s.usable)}</span>
            )}
          </span>
        )
      })}
    </span>
  )
}
