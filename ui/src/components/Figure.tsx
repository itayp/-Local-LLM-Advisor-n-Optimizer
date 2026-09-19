import type { Bytes, Rate, Source } from '../api/types'
import { en } from '../copy/en'

/**
 * Figure renders a number a user will see, with its provenance made
 * visible. This is the UI half of product rule 4: two visual treatments,
 * everywhere, always. The `source` prop is required by the type, and the
 * API types carry it, so a screen cannot show a bare number by accident.
 *
 * Treatments (index.css, `.figure--estimated` / `.figure--measured`):
 *   estimated  ≈ 45 tok/s   dotted underline, muted, a "≈" prefix, a range where one exists
 *   measured     51 tok/s   solid, emphasised, no prefix
 *
 * Usage:
 *   <Figure bytes={estimate.memory.total} />
 *   <Figure rate={recommendation.speed} />
 *   <Figure value="14.8 GB" source="estimated" />   // already formatted
 */
type Props =
  | { bytes: Bytes; rate?: never; value?: never; source?: never }
  | { rate: Rate; bytes?: never; value?: never; source?: never }
  | { value: string; source: Source; bytes?: never; rate?: never }

export function Figure(props: Props) {
  let source: Source
  let text: string
  if (props.bytes) {
    source = props.bytes.source
    text = formatBytes(props.bytes.value)
  } else if (props.rate) {
    source = props.rate.source
    text = formatRate(props.rate)
  } else {
    source = props.source
    text = props.value
  }
  const label = source === 'measured' ? en.figure.measuredLabel : en.figure.estimatedLabel
  return (
    <span className={`figure figure--${source}`} data-source={source} title={label}>
      {source === 'estimated' ? <span className="figure__prefix">{en.figure.estimatedPrefix} </span> : null}
      {text}
    </span>
  )
}

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '?'
  const gb = n / 1024 ** 3
  // One decimal — 14.8 GB is a different answer from 15 GB when the card
  // has 16 — but never a trailing ".0".
  if (gb >= 1) return `${gb.toFixed(1).replace(/\.0$/, '')} GB`
  const mb = n / 1024 ** 2
  if (mb >= 1) return `${mb.toFixed(0)} MB`
  return `${n} B`
}

export function formatRate(r: Rate): string {
  const one = (v: number) => (v >= 100 ? v.toFixed(0) : v.toFixed(1))
  // An estimated range is a range because it is not precise: whole numbers
  // from 10 up ("47–57", never "47.0–57.0"), one decimal only below.
  const whole = (v: number) => (v >= 10 ? v.toFixed(0) : v.toFixed(1))
  if (r.low !== r.high) {
    const f = r.source === 'estimated' ? whole : one
    return `${f(r.low)}–${f(r.high)} ${r.unit}`
  }
  return `${one(r.value)} ${r.unit}`
}
