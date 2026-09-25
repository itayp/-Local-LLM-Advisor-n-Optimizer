import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import type { Purpose, Recommendation, RecommendResult } from '../api/types'
import { Figure, formatBytes } from '../components/Figure'
import { ModelList } from '../components/ModelList'
import { PublicLine } from '../components/PublicFigure'
import { Working } from '../components/Working'
import { en } from '../copy/en'
import { useAdvanced, useSettings } from '../state/settings'

const c = en.screens.recommend

/** The order the question lists the purposes in: the beginner's first. */
const purposeOrder: Purpose[] = ['chat', 'writing', 'coding', 'reasoning', 'long_context', 'vision', 'agentic']

/**
 * Recommend (build-plan step 5): what do you want to use AI for, and up to
 * three models that fit this computer — each with its reasons in plain
 * words, what it costs to get, and how sure the advisor is.
 *
 * Every sentence on a card comes from the daemon (recommend.Reason): the
 * screen adds labels, not claims. A card's public line (step 9b) sits below
 * and apart from the reasons, under its own heading, and is rendered by
 * <PublicFigure> — never <Figure>; "Details" opens the model's detail view,
 * where public data and this computer's numbers sit in two blocks. Every number with provenance goes through
 * <Figure>: a speed is an estimated RANGE until a test measures it, and when
 * there is no estimate the card says so in words — never a number.
 *
 * The purposes picked here are durable (build-plan step 10): saved through
 * the daemon's settings table, so the new-model watch has something to
 * check new models against even with nobody looking. A settings row that
 * already has a choice seeds this screen with it, once — after that, only
 * a pick made here changes either.
 */
export function Recommend() {
  const advanced = useAdvanced()
  const { settings, setPurposes: persistPurposes } = useSettings()
  const [purposes, setPurposes] = useState<Purpose[]>(settings.purposes.length > 0 ? settings.purposes : ['chat'])
  const [result, setResult] = useState<RecommendResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  // Bumped after the model list has been fetched, to ask again.
  const [asked, setAsked] = useState(0)
  // True once `purposes` has been seeded from a real saved choice (at mount,
  // or once the daemon's answer to GET /api/settings arrives) — after that,
  // a later change to the saved copy (which a pick made here also causes)
  // must never overwrite what is on screen.
  const seeded = useRef(settings.purposes.length > 0)

  useEffect(() => {
    if (seeded.current || settings.purposes.length === 0) return
    seeded.current = true
    setPurposes(settings.purposes)
  }, [settings.purposes])

  useEffect(() => {
    if (purposes.length === 0) {
      setResult(null)
      return
    }
    const ac = new AbortController()
    setLoading(true)
    setError(null)
    api
      .recommend(purposes, ac.signal)
      .then((r) => {
        setResult(r)
        setLoading(false)
      })
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setError(err instanceof Error ? err.message : String(err))
        setLoading(false)
      })
    return () => ac.abort()
  }, [purposes, asked])

  const toggle = (p: Purpose) => {
    const next = purposes.includes(p) ? purposes.filter((x) => x !== p) : purposeOrder.filter((x) => x === p || purposes.includes(x))
    setPurposes(next)
    persistPurposes(next)
  }

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>

      <ModelList compact onFetched={() => setAsked((n) => n + 1)} />

      <fieldset className="purposes">
        <legend className="screen__lead">{c.question}</legend>
        <p className="screen__note">{c.questionHelp}</p>
        <ul>
          {purposeOrder.map((p) => (
            <li key={p}>
              <label>
                <input type="checkbox" checked={purposes.includes(p)} onChange={() => toggle(p)} /> {c.purposes[p]}
              </label>
            </li>
          ))}
        </ul>
      </fieldset>

      {purposes.length === 0 ? <p className="screen__note">{c.pickOne}</p> : null}
      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : null}
      {loading && !result ? <Working label={c.loading} /> : null}
      {result && purposes.length > 0 && !error ? <Result result={result} advanced={advanced} /> : null}
    </section>
  )
}

function Result({ result, advanced }: { result: RecommendResult; advanced: boolean }) {
  return (
    <>
      {result.warning ? (
        <div className="notice notice--warning" data-testid="recommend-warning">
          <p>{result.warning}</p>
          {result.gpu_not_used ? (
            <details>
              <summary>{c.whyNotUsed}</summary>
              <p>{result.gpu_not_used.why}</p>
            </details>
          ) : null}
        </div>
      ) : null}
      {result.current ? (
        <p className="notice" data-testid="recommend-current">
          {c.current(result.current.name)} {result.current.verdict}
        </p>
      ) : null}
      {result.empty ? (
        <div className="notice" data-testid="recommend-empty">
          <p>{result.empty}</p>
        </div>
      ) : null}
      {result.recommendations.length > 0 ? (
        <ol className="cards" aria-label={c.listLabel}>
          {result.recommendations.map((r) => (
            <li key={r.file.id}>
              <Card r={r} pathSource={result.path_source} advanced={advanced} />
            </li>
          ))}
        </ol>
      ) : null}
    </>
  )
}

function Card({ r, pathSource, advanced }: { r: Recommendation; pathSource: RecommendResult['path_source']; advanced: boolean }) {
  const words = Math.round((r.num_ctx * 0.75) / 1000) * 1000
  return (
    <article className="card" aria-label={r.display_name}>
      <header className="card__head">
        <h2>{r.display_name}</h2>
        <span className={`confidence confidence--${r.confidence}`} data-testid="confidence" title={r.confidence_why}>
          {c.confidence[r.confidence]}
        </span>
      </header>

      <dl className="card__figures">
        <dt>{c.speed}</dt>
        <dd>{r.speed ? <Figure rate={r.speed} /> : <span className="card__unknown">{c.noSpeed}</span>}</dd>
        <dt>{c.memory}</dt>
        <dd>
          <Figure bytes={r.estimate.memory.total} />
        </dd>
        <dt>{c.download}</dt>
        <dd>{r.installed ? c.installed : formatDownload(r.download_bytes)}</dd>
      </dl>

      <h3>{c.why}</h3>
      <ul className="card__reasons">
        {r.reasons.map((reason) => (
          <li key={reason.text} className={`reason reason--${reason.kind}`}>
            {reason.text}
            {reason.explainer && reason.detail ? (
              <details data-explainer={reason.explainer}>
                <summary>{c.explainer}</summary>
                <p>{reason.detail}</p>
              </details>
            ) : null}
          </li>
        ))}
      </ul>

      <p className="card__confidence">{r.confidence_why}</p>
      <p className="screen__note">
        {c.keepsInMind(words.toLocaleString('en-US'))} · {c.nameInOllama}: <code>{r.pull_name}</code>
      </p>

      <PublicLine entry={r.public} />
      <p className="screen__note">
        <Link to={`/models/${r.model.id}`}>{c.details}</Link>
        {' · '}
        <Link to={`/benchmarks?model=${encodeURIComponent(r.pull_name)}`}>{c.test}</Link>
      </p>

      {advanced ? <Technical r={r} pathSource={pathSource} /> : null}
    </article>
  )
}

/** Download sizes are facts from the catalogue's listing, in the decimal gigabytes download pages use. */
function formatDownload(bytes: number): string {
  const gb = bytes / 1e9
  if (gb < 1) return `${Math.max(1, Math.round(gb * 1000))} MB`
  return `${gb.toFixed(1).replace(/\.0$/, '')} GB`
}

function Technical({ r, pathSource }: { r: Recommendation; pathSource: RecommendResult['path_source'] }) {
  const a = c.advanced
  const e = r.estimate
  const m = e.memory
  const f = r.factors
  const rows: [{ label: string; explain: string }, React.ReactNode][] = [
    [a.quantization, r.file.quant],
    [a.context, a.tokens(m.effective_ctx.toLocaleString('en-US'))],
    [a.path, `${e.request.runtime_path} (${pathSource === 'established' ? a.pathEstablished : a.pathExpected})`],
    [a.weights, <Figure bytes={m.weights} />],
    [a.kvCache, <Figure bytes={m.kv_cache} />],
    [a.overhead, <Figure bytes={m.overhead} />],
    [a.total, <Figure bytes={m.total} />],
    [a.gpuResident, <Figure bytes={m.gpu_resident} />],
    [a.cpuOffload, <Figure bytes={m.cpu_offload} />],
    [a.budget, e.budget_known ? formatBytes(e.budget_bytes) : '—'],
    [a.category, e.threshold],
  ]
  if (e.speed.generation) rows.push([a.generation, <Figure rate={e.speed.generation} />])
  if (e.speed.prompt) rows.push([a.prompt, <Figure rate={e.speed.prompt} />])
  if (e.speed.basis) rows.push([a.speedBasis, e.speed.basis])
  rows.push([a.score, a.scoreValue(r.score, f.purpose, f.fit, f.speed, f.size)])
  if (f.public !== undefined && f.public !== 1) rows.push([a.publicAdjust, `× ${f.public}`])
  return (
    <div className="screen__advanced" data-testid="recommend-advanced">
      <h3>{a.title}</h3>
      <table className="tech">
        <thead>
          <tr>
            <th>{a.term}</th>
            <th>{a.value}</th>
            <th>{a.explain}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(([term, value]) => (
            <tr key={term.label}>
              <th scope="row">{term.label}</th>
              <td>{value}</td>
              <td className="tech__rule">{term.explain}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {e.notes?.length ? (
        <>
          <h3>{a.notes}</h3>
          <ul className="notes">
            {e.notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </>
      ) : null}
    </div>
  )
}
