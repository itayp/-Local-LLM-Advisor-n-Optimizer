import { useState, type ReactNode } from 'react'
import type { GlossaryTermId } from '../copy/glossary'
import { glossary } from '../copy/glossary'

/**
 * Term shows a technical word with its one-line explainer a tap away
 * (CLAUDE.md rule 2: no term from the glossary list appears without one).
 * Written once per term in copy/glossary.ts; a screen using a glossary
 * term renders it through this component instead of restating the
 * explanation itself.
 *
 * A toggle button, not <details>/<summary>: Term appears inline inside
 * ordinary sentences (a <p>), and <details> is not valid content there —
 * a button and a span are.
 */
export function Term({ id, children }: { id: GlossaryTermId; children?: ReactNode }) {
  const g = glossary[id]
  const [open, setOpen] = useState(false)
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
        </span>
      ) : null}
    </span>
  )
}
