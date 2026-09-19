import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { PullStatus, Recommendation } from '../api/types'
import { Term } from '../components/Term'
import { en } from '../copy/en'
import { Progress } from './Progress'

const c = en.onboarding.getting

/**
 * Screen 6: pull progress, cancellable. No SSE (pull.go's own doc
 * comment) — the UI polls GET /api/models/pull once a second, the same
 * shape as install.go's status endpoint.
 */
export function Getting({ recommendation, onDone }: { recommendation: Recommendation; onDone: () => void }) {
  const [status, setStatus] = useState<PullStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [cancelling, setCancelling] = useState(false)

  useEffect(() => {
    let cancelled = false
    api
      .pullStart(recommendation.pull_name)
      .then((s) => !cancelled && setStatus(s))
      .catch(() => {
        // A pull may already be running (a reload mid-download): fall
        // back to reading its status instead of treating this as a
        // failure.
        api
          .pullStatus()
          .then((s) => !cancelled && setStatus(s))
          .catch((err: unknown) => !cancelled && setError(err instanceof Error ? err.message : String(err)))
      })
    return () => {
      cancelled = true
    }
  }, [recommendation.pull_name])

  useEffect(() => {
    if (status?.status !== 'running') return
    let cancelled = false
    const id = window.setInterval(() => {
      api
        .pullStatus()
        .then((s) => !cancelled && setStatus(s))
        .catch(() => undefined)
    }, 1000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [status?.status])

  useEffect(() => {
    if (status?.status === 'done') onDone()
  }, [status?.status, onDone])

  const cancel = () => {
    setCancelling(true)
    api.pullCancel().finally(() => setCancelling(false))
  }

  return (
    <section className="onboarding__step" aria-labelledby="onboarding-title">
      <h1 id="onboarding-title">{c.title}</h1>
      <p className="screen__lead">{c.lead(recommendation.display_name)}</p>
      <p className="screen__note">
        {c.fileNotePrefix} <Term id="gguf" /> {c.fileNoteSuffix}
      </p>
      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : null}
      {!status || status.status === 'running' || status.status === 'idle' ? (
        <>
          <Progress label={c.downloading} completed={status?.completed_bytes ?? 0} total={status?.total_bytes} />
          <button type="button" className="button button--secondary" disabled={cancelling} onClick={cancel}>
            {cancelling ? c.cancelling : c.cancel}
          </button>
        </>
      ) : status.status === 'failed' ? (
        <p className="notice notice--warning" role="alert">
          {c.pullFailed(status.error ?? '')}
        </p>
      ) : status.status === 'cancelled' ? (
        <p className="notice">{c.cancelled}</p>
      ) : null}
    </section>
  )
}
