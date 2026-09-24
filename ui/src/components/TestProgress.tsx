import type { BenchProgress } from '../api/types'
import { en } from '../copy/en'
import { Figure } from './Figure'
import { useElapsed } from './Working'

const c = en.screens.benchmarks

/** An estimated duration in seconds as whole minutes, at least one. */
export function minutes(low: number, high: number): [number, number] {
  const lo = Math.max(1, Math.round(low / 60))
  return [lo, Math.max(lo, Math.round(high / 60))]
}

/**
 * TestProgress is a running test, from the click that starts it to its
 * last step: what it is doing now, a bar (moving on its own until the
 * first passage is timed, then step by step), the time so far — ticking
 * every second, so a slow load never looks like a stalled screen — and
 * the time left, an estimate (<Figure>). Benchmarks and onboarding's
 * "Try it" both show it.
 */
export function TestProgress({
  p,
  cancelling = false,
  onCancel,
}: {
  p: BenchProgress | null
  cancelling?: boolean
  onCancel?: () => void
}) {
  const s = useElapsed()
  const determinate = p !== null && p.steps > 0 && p.step > 0
  return (
    <div className="bench-running notice" data-testid="bench-running">
      <p role="status">{cancelling ? c.cancelling : (p?.message ?? c.startingTest)}</p>
      {determinate ? (
        <progress aria-label={c.progressLabel} value={p.step} max={Math.max(p.steps, 1)} />
      ) : (
        <progress aria-label={c.progressLabel} />
      )}
      <p className="screen__note">
        {p && p.steps > 0 ? `${c.step(p.step, p.steps)} · ` : ''}
        {en.working.elapsed(Math.max(s, p?.elapsed_seconds ?? 0))}
        {p?.remaining ? (
          <>
            {' · '}
            {c.timeLeft}{' '}
            <Figure
              value={p.remaining.high < 60 ? c.underAMinute : c.minutes(...minutes(p.remaining.low, p.remaining.high))}
              source="estimated"
            />
          </>
        ) : null}
      </p>
      {onCancel ? (
        <button type="button" className="button button--secondary" disabled={cancelling || p === null} onClick={onCancel}>
          {c.cancel}
        </button>
      ) : null}
    </div>
  )
}
