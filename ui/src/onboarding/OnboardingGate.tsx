import { useEffect, useState, type ReactNode } from 'react'
import { api } from '../api/client'
import { en } from '../copy/en'
import { Onboarding } from './Onboarding'

/**
 * OnboardingGate is what main.tsx renders in place of <App> directly: it
 * asks GET /api/onboarding and shows the first-run flow until POST
 * /api/onboarding/complete has run once, then renders the working app —
 * "make it what the browser opens to until it has been completed once."
 *
 * Deliberately outside App's own route tree: App.tsx, App.test.tsx, and
 * every screen's own test render <App> directly and must keep doing so
 * unaffected by this gate.
 */
export function OnboardingGate({ children }: { children: ReactNode }) {
  const [completed, setCompleted] = useState<boolean | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    api
      .onboardingStatus(ac.signal)
      .then((s) => setCompleted(s.completed))
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        // The daemon couldn't be asked: don't trap the person behind a
        // gate that may never open. Nothing runs without its own button
        // click either way (product rule 5), so showing the working app
        // is the safe fallback.
        setCompleted(true)
      })
    return () => ac.abort()
  }, [])

  if (completed === null) {
    return (
      <p className="screen__note" role="status" data-testid="onboarding-gate-loading">
        {en.onboarding.gateLoading}
      </p>
    )
  }
  if (!completed) {
    return <Onboarding onFinished={() => setCompleted(true)} />
  }
  return <>{children}</>
}
