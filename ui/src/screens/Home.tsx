import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import type { BackendInfo, BenchRun, HardwareResponse, InstalledModel } from '../api/types'
import { Figure } from '../components/Figure'
import { InstallCard, StartCard } from '../components/OllamaActions'
import { Term } from '../components/Term'
import { en } from '../copy/en'

const c = en.screens.home

/**
 * Home (build-plan step 8): this computer in one sentence (the same
 * sentence GET /api/hardware gives Your Computer), the model you've
 * actually measured — if any — and one card for the single most useful
 * thing to do next. The next step is worked out from state the other
 * screens already expose (is Ollama running, is anything installed, has
 * anything been benchmarked); when Ollama itself needs a click, the card
 * does it right there with the same install/start endpoints the
 * onboarding flow's Ollama step uses (build-plan step 7), rather than
 * sending the person to a screen that can't yet do more than name the
 * problem.
 */
export function Home() {
  const [hw, setHw] = useState<HardwareResponse | null>(null)
  const [hwError, setHwError] = useState<string | null>(null)
  const [backend, setBackend] = useState<BackendInfo | null>(null)
  const [models, setModels] = useState<InstalledModel[] | null>(null)
  const [bestRun, setBestRun] = useState<BenchRun | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    api
      .hardware(ac.signal)
      .then(setHw)
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setHwError(err instanceof Error ? err.message : String(err))
      })
    return () => ac.abort()
  }, [])

  const loadBackend = useCallback((signal?: AbortSignal) => {
    api
      .backends(signal)
      .then((r) => setBackend(r.backends.find((b) => b.name === 'ollama') ?? r.backends[0] ?? null))
      .catch(() => undefined)
  }, [])

  useEffect(() => {
    const ac = new AbortController()
    loadBackend(ac.signal)
    return () => ac.abort()
  }, [loadBackend])

  useEffect(() => {
    if (backend?.state !== 'running') return
    const ac = new AbortController()
    api
      .installedModels(ac.signal)
      .then((r) => setModels(r.models ?? []))
      .catch(() => undefined)
    api
      .benchHistory(ac.signal)
      .then((h) => {
        const done = h.runs.filter((r) => r.status === 'done' && r.generation_tps)
        const latest = done.reduce<BenchRun | null>((best, r) => (!best || r.started_at > best.started_at ? r : best), null)
        setBestRun(latest)
      })
      .catch(() => undefined)
    return () => ac.abort()
  }, [backend?.state])

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>
      {hwError ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(hwError)}
        </p>
      ) : !hw?.profile ? (
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      ) : (
        <p className="home__lead" data-testid="home-summary">
          {hw.profile.summary}
        </p>
      )}

      {bestRun?.generation_tps ? (
        <dl className="card__figures">
          <dt>{c.yourModel}</dt>
          <dd>
            {bestRun.config.model} — {c.modelMeasured} <Figure rate={bestRun.generation_tps} /> <Term id="tokens_per_sec" />
          </dd>
        </dl>
      ) : null}

      <h2>{c.nextTitle}</h2>
      <NextAction backend={backend} models={models} bestRun={bestRun} onBackendChanged={() => loadBackend()} />
    </section>
  )
}

function NextAction({
  backend,
  models,
  bestRun,
  onBackendChanged,
}: {
  backend: BackendInfo | null
  models: InstalledModel[] | null
  bestRun: BenchRun | null
  onBackendChanged: () => void
}) {
  if (!backend) {
    return (
      <p className="screen__note" role="status">
        {c.loading}
      </p>
    )
  }
  if (backend.state === 'not_installed') {
    return <InstallCard name={backend.name} onDone={onBackendChanged} />
  }
  if (backend.state === 'installed_not_running') {
    return <StartCard name={backend.name} onDone={onBackendChanged} />
  }
  if (backend.state === 'unsupported') {
    return (
      <p className="notice notice--warning" data-testid="next-unsupported">
        {backend.detail || c.next.installLead}
      </p>
    )
  }
  // Running: the next useful thing depends on what's installed and tested.
  if (models !== null && models.length === 0) {
    return (
      <article className="next-card" data-testid="next-no-models">
        <h2>{c.next.noModelsTitle}</h2>
        <p>{c.next.noModelsLead}</p>
        <Link className="button" to="/recommend">
          {c.next.goToRecommend}
        </Link>
      </article>
    )
  }
  if (models !== null && models.length > 0 && !bestRun) {
    return (
      <article className="next-card" data-testid="next-not-benchmarked">
        <h2>{c.next.notBenchmarkedTitle}</h2>
        <p>{c.next.notBenchmarkedLead}</p>
        <Link className="button" to="/benchmarks">
          {c.next.goToBenchmarks}
        </Link>
      </article>
    )
  }
  if (bestRun) {
    return (
      <article className="next-card" data-testid="next-all-good">
        <h2>{c.next.allGoodTitle}</h2>
        <p>{c.next.allGoodLead}</p>
        <Link className="button" to="/recommend">
          {c.next.goToRecommend}
        </Link>
      </article>
    )
  }
  return (
    <p className="screen__note" role="status">
      {c.loading}
    </p>
  )
}
