import type { PublicEntry } from '../api/types'
import { en } from '../copy/en'

const c = en.publicData

/**
 * PublicFigure renders a public value — a number someone else published
 * about a MODEL — and is the only component that does (step 9b,
 * research/EXTERNAL_SOURCES.md P-1). It is the third treatment, beside
 * <Figure>'s estimated and measured ones, and looks like neither: no "≈",
 * no range, no point figure. Words come first (P-5): the size's position
 * among the curated sizes the source has scored. Directly beneath, never
 * behind a tap (P-4): what it tested, who produced it, who published it
 * and the source's own date, and the credit its licence asks for. The raw
 * value, its scale and its detail appear only under Advanced.
 *
 * Its prop is a PublicEntry, whose value has no `source` field, so it
 * cannot be handed a local figure, and <Figure> cannot be handed it.
 */
export function PublicFigure({ entry, advanced }: { entry: PublicEntry; advanced: boolean }) {
  const o = entry.value.origin
  return (
    <div className="public-figure" data-kind="public" data-provenance={o.provenance} title={c.label}>
      <p className="public-figure__position">{entry.position}</p>
      <p className="public-figure__origin">
        {c.tests(entry.tests, entry.provenance_words)} {c.publishedBy(o.publisher, formatDay(o.date))}{' '}
        {o.url ? (
          <a href={o.url} target="_blank" rel="noreferrer">
            {c.readAtSource}
          </a>
        ) : null}
        <br />
        <span className="public-figure__attribution">{o.attribution}</span>
      </p>
      {!entry.scored ? <p className="public-figure__note">{c.notScored}</p> : null}
      {advanced && entry.detail.length > 0 ? (
        <dl className="public-figure__detail" data-testid="public-detail">
          {entry.detail.map((d) => (
            <div key={d.label}>
              <dt>{d.label}</dt>
              <dd>{d.text}</dd>
            </div>
          ))}
        </dl>
      ) : null}
    </div>
  )
}

/** A source's date, "2026-09-15", as "15 September 2026". */
export function formatDay(date: string): string {
  const d = new Date(`${date}T00:00:00Z`)
  if (Number.isNaN(d.getTime())) return date
  return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' })
}

/**
 * PublicLine is a recommendation card's one "Public data" line (P-3): below
 * and apart from the card's reasons — it is not a reason — under its own
 * heading. Nothing when the card has no public value.
 */
export function PublicLine({ entry }: { entry?: PublicEntry }) {
  if (!entry) return null
  return (
    <section className="card__public" data-testid="public-line" aria-label={c.cardTitle}>
      <h3>{c.cardTitle}</h3>
      <PublicFigure entry={entry} advanced={false} />
    </section>
  )
}
