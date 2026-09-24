import { useEffect, useState } from 'react'
import { en } from '../copy/en'

/**
 * useElapsed counts whole seconds since the component mounted (or since
 * `since`, an RFC 3339 time the daemon gave), ticking once a second — so a
 * wait always shows something moving, even when the daemon has nothing new
 * to say yet.
 */
export function useElapsed(since?: string): number {
  const [start] = useState(() => {
    const t = since ? Date.parse(since) : NaN
    return Number.isFinite(t) ? Math.min(t, Date.now()) : Date.now()
  })
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(id)
  }, [])
  return Math.max(0, Math.floor((now - start) / 1000))
}

/**
 * Working is the one "something is happening" strip for a wait whose length
 * the advisor cannot know: what it is doing, a moving (indeterminate) bar,
 * and the seconds so far once the wait is long enough to wonder about.
 */
export function Working({ label, since }: { label: string; since?: string }) {
  const s = useElapsed(since)
  return (
    <div className="working" role="status" data-testid="working">
      <p className="working__label">{label}</p>
      <progress className="working__bar" aria-label={label} />
      {s >= 2 ? <p className="screen__note working__elapsed">{en.working.elapsed(s)}</p> : null}
    </div>
  )
}
