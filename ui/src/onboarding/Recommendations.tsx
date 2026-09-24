import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { Purpose, Recommendation, RecommendResult } from '../api/types'
import { Figure } from '../components/Figure'
import { PublicLine } from '../components/PublicFigure'
import { Term } from '../components/Term'
import { en } from '../copy/en'
import { formatDownload } from './format'

const c = en.onboarding.recommend

/**
 * Screen 5: at most three cards — name in words, why (the daemon's own
 * templated reasons), download size, confidence, estimated speed range —
 * each with one button that says what it will do (product rule 5).
 * Purposes default to "everyday chat" when none were picked, matching
 * GET /api/recommend's own default.
 */
export function Recommendations({ purposes, onDownload }: { purposes: Purpose[]; onDownload: (r: Recommendation) => void }) {
  const [result, setResult] = useState<RecommendResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  // Bumped after the model list has been fetched, to ask again.
  const [asked, setAsked] = useState(0)

  useEffect(() => {
    const ac = new AbortController()
    api
      .recommend(purposes.length ? purposes : ['chat'], ac.signal)
      .then(setResult)
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => ac.abort()
  }, [purposes, asked])

  return (
    <section className="onboarding__step" aria-labelledby="onboarding-title">
      <h1 id="onboarding-title">{c.title}</h1>
      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : null}
      {!result && !error ? (
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      ) : null}
      {result ? (
        result.recommendations.length === 0 ? (
          <div className="notice" data-testid="onboarding-recommend-empty">
            <p>{result.empty ?? c.empty}</p>
            {result.empty_code === 'catalogue_empty' ? <FetchList onDone={() => setAsked((n) => n + 1)} /> : null}
          </div>
        ) : (
          <ol className="cards" aria-label={c.listLabel}>
            {result.recommendations.map((r) => (
              <li key={r.file.id}>
                <Card r={r} onDownload={onDownload} />
              </li>
            ))}
          </ol>
        )
      ) : null}
    </section>
  )
}

/**
 * The one thing this step can do besides download a model: fetch the
 * catalogue's model list from Hugging Face when it hasn't been resolved
 * yet (a fresh install, before any /catalog/refresh has run) — without
 * this, an empty catalogue is a dead end on first run. The button says
 * what it does and what it costs (product rule 5); the daemon downloads
 * descriptions only, never model weights.
 */
function FetchList({ onDone }: { onDone: () => void }) {
  const [state, setState] = useState<'idle' | 'fetching'>('idle')
  const [failed, setFailed] = useState<string | null>(null)
  const fetchList = () => {
    setState('fetching')
    setFailed(null)
    api
      .refreshCatalog()
      .then(onDone)
      .catch((err: unknown) => {
        setFailed(err instanceof Error ? err.message : String(err))
        setState('idle')
      })
  }
  return (
    <>
      {state === 'fetching' ? (
        <p role="status">{c.fetching}</p>
      ) : (
        <button type="button" className="button" onClick={fetchList}>
          {c.fetchList}
        </button>
      )}
      {failed ? <p role="alert">{c.fetchFailed(failed)}</p> : null}
    </>
  )
}

function Card({ r, onDownload }: { r: Recommendation; onDownload: (r: Recommendation) => void }) {
  const words = Math.round((r.num_ctx * 0.75) / 1000) * 1000
  return (
    <article className="card" aria-label={r.display_name}>
      <header className="card__head">
        <h2>{r.display_name}</h2>
        <span className={`confidence confidence--${r.confidence}`} title={r.confidence_why}>
          {en.screens.recommend.confidence[r.confidence]}
        </span>
      </header>

      <dl className="card__figures">
        <dt>{c.speed}</dt>
        <dd>{r.speed ? <Figure rate={r.speed} /> : <span className="card__unknown">{c.noSpeed}</span>}</dd>
        <dt>{c.download}</dt>
        <dd>{r.installed ? c.installed : formatDownload(r.download_bytes)}</dd>
      </dl>

      <ul className="card__reasons">
        {r.reasons.map((reason) => (
          <li key={reason.text} className={`reason reason--${reason.kind}`}>
            {reason.text}
          </li>
        ))}
      </ul>

      <p className="screen__note">
        {c.keepsInMind(words.toLocaleString('en-US'))}
        <Term id="context_window" />
        {c.keepsInMindSuffix}
      </p>

      <PublicLine entry={r.public} />

      <button type="button" className="button" onClick={() => onDownload(r)}>
        {r.installed ? c.continueInstalled : c.downloadButton(formatDownload(r.download_bytes))}
      </button>
    </article>
  )
}
