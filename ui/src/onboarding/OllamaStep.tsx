import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { BackendInfo, InstallSizeResponse, InstallStatus, InstalledModel } from '../api/types'
import { Term } from '../components/Term'
import { en } from '../copy/en'
import { formatDownload } from './format'
import { Progress } from './Progress'

const c = en.onboarding.ollama

/**
 * Screen 3: one of the three states step 3 (internal/backend) already
 * tells apart — not installed, installed but not running, running — each
 * with one button that says what it does (product rule 5): "Install
 * Ollama (about N MB)", "Start Ollama", or nothing at all once it's
 * found. No SSE for install progress (install.go's own doc comment): the
 * UI polls GET /api/backends and GET /api/backends/{name}/install once a
 * second, same as pull.go's reasoning for the download step.
 */
export function OllamaStep({ onNext }: { onNext: () => void }) {
  const [backend, setBackend] = useState<BackendInfo | null>(null)
  const [backendError, setBackendError] = useState<string | null>(null)
  const [installSize, setInstallSize] = useState<InstallSizeResponse | null>(null)
  const [install, setInstall] = useState<InstallStatus | null>(null)
  const [starting, setStarting] = useState(false)
  const [startError, setStartError] = useState<string | null>(null)
  const [models, setModels] = useState<InstalledModel[]>([])

  const name = backend?.name ?? 'ollama'

  useEffect(() => {
    let cancelled = false
    const tick = () => {
      api
        .backends()
        .then((r) => {
          if (cancelled) return
          const b = r.backends.find((x) => x.name === 'ollama') ?? r.backends[0] ?? null
          setBackend(b)
          if (b?.state === 'running') {
            api
              .installedModels()
              .then((m) => !cancelled && setModels(m.models ?? []))
              .catch(() => undefined)
          }
        })
        .catch((err: unknown) => {
          if (!cancelled) setBackendError(err instanceof Error ? err.message : String(err))
        })
    }
    tick()
    const id = window.setInterval(tick, 1500)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [])

  useEffect(() => {
    if (backend?.state !== 'not_installed' || installSize) return
    let cancelled = false
    api
      .backendInstallSize(name)
      .then((s) => !cancelled && setInstallSize(s))
      .catch(() => !cancelled && setInstallSize({ bytes: 0, known: false }))
    return () => {
      cancelled = true
    }
  }, [backend?.state, name, installSize])

  useEffect(() => {
    if (install?.status !== 'running') return
    let cancelled = false
    const id = window.setInterval(() => {
      api
        .backendInstallStatus(name)
        .then((s) => !cancelled && setInstall(s))
        .catch(() => undefined)
    }, 1000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [install?.status, name])

  const startInstall = () => {
    api
      .backendInstallStart(name)
      .then(setInstall)
      .catch((err: unknown) =>
        setInstall({ backend: name, status: 'failed', completed_bytes: 0, error: err instanceof Error ? err.message : String(err) }),
      )
  }

  const startRuntime = () => {
    setStarting(true)
    setStartError(null)
    api
      .backendStart(name)
      .catch((err: unknown) => setStartError(err instanceof Error ? err.message : String(err)))
      .finally(() => setStarting(false))
  }

  return (
    <section className="onboarding__step" aria-labelledby="onboarding-title">
      <h1 id="onboarding-title">{c.title}</h1>
      <p className="screen__lead">
        {c.lead}
        <Term id="vram">{c.leadTermVram}</Term>
        {c.leadSuffix}
      </p>
      {backendError ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(backendError)}
        </p>
      ) : null}
      {!backend ? (
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      ) : backend.state === 'running' ? (
        <>
          <p className="notice" data-testid="ollama-found">
            {c.found(backend.version ?? '')}
          </p>
          {models.length > 0 ? (
            <dl className="facts">
              <dt>{c.installedModelsLabel}</dt>
              <dd>
                <ul>
                  {models.map((m) => (
                    <li key={m.name}>{m.name}</li>
                  ))}
                </ul>
              </dd>
            </dl>
          ) : null}
          <button type="button" className="button" onClick={onNext}>
            {c.continue}
          </button>
        </>
      ) : backend.state === 'not_installed' ? (
        install?.status === 'running' ? (
          <Progress label={c.installing} completed={install.completed_bytes} total={install.total_bytes} />
        ) : (
          <>
            {install?.status === 'failed' ? (
              <p className="notice notice--warning" role="alert">
                {c.installFailed(install.error ?? '')}
              </p>
            ) : null}
            <button type="button" className="button" onClick={startInstall}>
              {c.install(installSize?.known ? formatDownload(installSize.bytes) : undefined)}
            </button>
          </>
        )
      ) : backend.state === 'installed_not_running' ? (
        <>
          {startError ? (
            <p className="notice notice--warning" role="alert">
              {c.startFailed(startError)}
            </p>
          ) : null}
          <button type="button" className="button" disabled={starting} onClick={startRuntime}>
            {starting ? c.starting : c.start}
          </button>
        </>
      ) : (
        <p className="notice notice--warning">{c.unsupported}</p>
      )}
    </section>
  )
}
