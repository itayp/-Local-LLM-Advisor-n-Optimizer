import { useCallback, useEffect, useState } from 'react'
import { api } from '../api/client'
import type { BackendInfo } from '../api/types'
import { InstallCard, StartCard } from '../components/OllamaActions'
import { en } from '../copy/en'

const c = en.screens.ollama

/**
 * Ollama: whether it is installed, whether it is running, which version —
 * GET /api/backends, the status Home reads — and, when it needs a click,
 * the same install or start card Home's next step shows (components/
 * OllamaActions.tsx). No capability of its own.
 */
export function Ollama() {
  const [backend, setBackend] = useState<BackendInfo | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback((signal?: AbortSignal) => {
    setError(null)
    api
      .backends(signal)
      .then((r) => setBackend(r.backends.find((b) => b.name === 'ollama') ?? r.backends[0] ?? null))
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setError(err instanceof Error ? err.message : String(err))
      })
  }, [])

  useEffect(() => {
    const ac = new AbortController()
    load(ac.signal)
    return () => ac.abort()
  }, [load])

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>
      <p className="screen__lead">{c.lead}</p>
      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : !backend ? (
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      ) : (
        <>
          <dl className="facts" data-testid="ollama-status">
            <dt>{c.stateLabel}</dt>
            <dd>{c.states[backend.state] ?? backend.state}</dd>
            {backend.version || backend.installed_version ? (
              <>
                <dt>{c.versionLabel}</dt>
                <dd>{backend.version || backend.installed_version}</dd>
              </>
            ) : null}
          </dl>
          {backend.detail ? <p className="screen__note">{backend.detail}</p> : null}
          {backend.state === 'not_installed' ? <InstallCard name={backend.name} onDone={() => load()} /> : null}
          {backend.state === 'installed_not_running' ? <StartCard name={backend.name} onDone={() => load()} /> : null}
          <button type="button" className="link-button" onClick={() => load()}>
            {c.checkAgain}
          </button>
        </>
      )}
    </section>
  )
}
