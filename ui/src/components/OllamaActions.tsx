import { useEffect, useRef, useState } from 'react'
import { api } from '../api/client'
import type { InstallSizeResponse, InstallStatus } from '../api/types'
import { en } from '../copy/en'
import { formatDownload } from '../onboarding/format'
import { Progress } from '../onboarding/Progress'

const c = en.screens.home

/**
 * The two cards that change Ollama's state, each one button that says what
 * it will do (product rule 5): install it, or start it. Home's "next step"
 * and the Ollama screen both use them, on the same install/start endpoints
 * the onboarding flow's Ollama step uses (build-plan step 7).
 */
export function InstallCard({ name, onDone }: { name: string; onDone: () => void }) {
  const [size, setSize] = useState<InstallSizeResponse | null>(null)
  const [status, setStatus] = useState<InstallStatus | null>(null)
  const poll = useRef<number | null>(null)

  useEffect(() => {
    api
      .backendInstallSize(name)
      .then(setSize)
      .catch(() => setSize({ bytes: 0, known: false }))
  }, [name])

  useEffect(() => {
    if (status?.status !== 'running') {
      if (poll.current) window.clearInterval(poll.current)
      return
    }
    poll.current = window.setInterval(() => {
      api
        .backendInstallStatus(name)
        .then((s) => {
          setStatus(s)
          if (s.status === 'done') onDone()
        })
        .catch(() => undefined)
    }, 1000)
    return () => {
      if (poll.current) window.clearInterval(poll.current)
    }
  }, [status?.status, name, onDone])

  const start = () => {
    api
      .backendInstallStart(name)
      .then(setStatus)
      .catch((err: unknown) =>
        setStatus({ backend: name, status: 'failed', completed_bytes: 0, error: err instanceof Error ? err.message : String(err) }),
      )
  }

  return (
    <article className="next-card" data-testid="next-install">
      <h2>{c.next.installTitle}</h2>
      <p>{c.next.installLead}</p>
      {status?.status === 'running' ? (
        <Progress label={c.next.installing} completed={status.completed_bytes} total={status.total_bytes} />
      ) : (
        <>
          {status?.status === 'failed' ? <p className="notice notice--warning">{c.next.installFailed(status.error ?? '')}</p> : null}
          <button type="button" className="button" onClick={start}>
            {c.next.install(size?.known ? formatDownload(size.bytes) : undefined)}
          </button>
        </>
      )}
    </article>
  )
}

export function StartCard({ name, onDone }: { name: string; onDone: () => void }) {
  const [starting, setStarting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const start = () => {
    setStarting(true)
    setError(null)
    api
      .backendStart(name)
      .then(() => window.setTimeout(onDone, 1000))
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setStarting(false))
  }

  return (
    <article className="next-card" data-testid="next-start">
      <h2>{c.next.installTitle}</h2>
      <p>{c.next.startLead}</p>
      {error ? <p className="notice notice--warning">{c.next.startFailed(error)}</p> : null}
      <button type="button" className="button" disabled={starting} onClick={start}>
        {starting ? c.next.starting : c.next.start}
      </button>
    </article>
  )
}
