import type { Rate, Source, SpeedVerdict as Verdict } from '../api/types'
import { en } from '../copy/en'
import { Figure } from './Figure'
import { Term } from './Term'

/**
 * SpeedVerdict shows what a speed is good for, per purpose (backlog (j)),
 * beside the speed itself — the same line on Benchmarks, Recommend and a
 * model's detail page:
 *
 *   Measured: excellent for everyday chat · good for coding — about 4 seconds to read a pasted file
 *
 * The verdicts' words come from the API (internal/recommend/verdict.go);
 * this adds the label and the joins, not claims. A verdict inherits its
 * speed's source, so it takes the same two treatments as <Figure>
 * (`.figure--estimated` / `.figure--measured`, product rule 4); verdicts
 * of different sources get a line each. The tokens_per_sec explainer —
 * the table of bars, and that they are provisional — is one tap away on
 * every line (product rule 2).
 */
export function SpeedVerdict({ verdicts, compact }: { verdicts?: Verdict[]; compact?: boolean }) {
  if (!verdicts?.length) return null
  const groups: [Source, Verdict[]][] = (['measured', 'estimated'] as Source[])
    .map((src): [Source, Verdict[]] => [src, verdicts.filter((v) => v.source === src)])
    .filter(([, vs]) => vs.length > 0)
  return (
    <span className="verdicts">
      {groups.map(([src, vs]) => {
        const waits = vs.filter((v) => v.wait_text).map((v) => v.wait_text!)
        const notes = compact ? [] : vs.filter((v) => v.note).map((v) => v.note!)
        return (
          <span key={src} className={`verdict verdict--${src}`} data-source={src} data-testid="speed-verdict">
            <span className="verdict__label">{src === 'measured' ? en.verdict.measured : en.verdict.estimated}: </span>
            {vs.map((v, i) => (
              <span key={v.purpose}>
                {i > 0 ? ' · ' : null}
                <span
                  className={`verdict__chip figure figure--${src} verdict__chip--${v.known ? v.low : 'unknown'}`}
                  data-source={src}
                  data-grade={v.known ? v.low : 'unknown'}
                >
                  {v.text}
                </span>
              </span>
            ))}
            {waits.length ? <span className="verdict__wait"> — {waits.join('; ')}</span> : null}{' '}
            <Term id="tokens_per_sec">{en.verdict.explain}</Term>
            {notes.map((n) => (
              <span key={n} className="verdict__note">
                {n}
              </span>
            ))}
          </span>
        )
      })}
    </span>
  )
}

/**
 * SpeedWithVerdict is an answering speed as every screen shows it: the
 * <Figure>, then its verdict line — or, where there is no verdict, the
 * tokens_per_sec explainer on its own, so the explainer is always one tap
 * from the number.
 */
export function SpeedWithVerdict({ rate, verdicts, compact }: { rate: Rate; verdicts?: Verdict[]; compact?: boolean }) {
  return (
    <>
      <Figure rate={rate} />
      {verdicts?.length ? (
        <SpeedVerdict verdicts={verdicts} compact={compact} />
      ) : (
        <>
          {' '}
          <Term id="tokens_per_sec" />
        </>
      )}
    </>
  )
}
