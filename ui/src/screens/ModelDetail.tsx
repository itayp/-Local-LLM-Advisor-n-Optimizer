import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router'
import { api } from '../api/client'
import type { FileFit, ModelDetailResponse } from '../api/types'
import { Figure } from '../components/Figure'
import { formatDay, PublicFigure } from '../components/PublicFigure'
import { Term } from '../components/Term'
import { en } from '../copy/en'
import { useAdvanced } from '../state/settings'

const c = en.screens.modelDetail
const p = en.publicData
const fitWords = en.screens.models.fitCategory

/**
 * The model detail view (build-plan step 9b): one catalogue size, as two
 * blocks titled apart (research/EXTERNAL_SOURCES.md P-2) —
 *
 *   Public data — how others rated this model: other people's results
 *     about the model, each a <PublicFigure> with its source and date
 *     beneath; "no public scores for this size yet" when there are none,
 *     never a sibling size's (P-7);
 *   Your machine — estimated or measured here: what this computer would do
 *     with it, every number a <Figure> (estimated until a benchmark
 *     measures it).
 *
 * No table, row, sentence or component holds a number from both: the API
 * sends them as sibling objects and the screen keeps them in sibling
 * sections. Reached from a recommendation card's "Details" and from Models.
 */
export function ModelDetail() {
  const { id } = useParams()
  const advanced = useAdvanced()
  const [detail, setDetail] = useState<ModelDetailResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const n = Number(id)
    if (!Number.isInteger(n) || n <= 0) {
      setError('no such model')
      return
    }
    const ac = new AbortController()
    setDetail(null)
    setError(null)
    api
      .modelDetail(n, ac.signal)
      .then(setDetail)
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => ac.abort()
  }, [id])

  if (error) {
    return (
      <section className="screen" aria-labelledby="screen-title">
        <h1 id="screen-title">{en.nav.modelDetail}</h1>
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
        <Link to="/recommend">{c.back}</Link>
      </section>
    )
  }
  if (!detail) {
    return (
      <section className="screen" aria-labelledby="screen-title">
        <h1 id="screen-title">{en.nav.modelDetail}</h1>
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      </section>
    )
  }

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{detail.name}</h1>
      <p className="screen__lead">
        {c.maintainer(detail.maintainer)}
        {detail.released_at ? c.released(formatDay(detail.released_at)) : null}
      </p>
      <p className="screen__note">
        {c.nameInOllama}: <code>{detail.pull_name}</code>
      </p>

      <div className="detail-blocks">
        <section className="detail-block detail-block--public" aria-labelledby="public-title" data-testid="public-block">
          <h2 id="public-title">{p.title}</h2>
          {detail.public.entries.length === 0 ? (
            <p data-testid="public-none">{p.none}</p>
          ) : (
            <>
              {detail.public.entries.map((e) => (
                <PublicFigure key={`${e.source_id}:${e.metric}`} entry={e} advanced={advanced} />
              ))}
              <p className="screen__note">
                {p.caveatBefore}
                <Term id="quantization" />
                {p.caveatAfter} {p.whatIsIt} <Term id="public_benchmark" />?
              </p>
            </>
          )}
          <p className="screen__note" data-testid="public-updated">
            {detail.public.updated}
          </p>
        </section>

        <section className="detail-block" aria-labelledby="machine-title" data-testid="machine-block">
          <h2 id="machine-title">{c.machineTitle}</h2>
          <Machine detail={detail} advanced={advanced} />
        </section>
      </div>
    </section>
  )
}

function Machine({ detail, advanced }: { detail: ModelDetailResponse; advanced: boolean }) {
  const local = detail.local
  const fit = local.fits.find((f) => f.default) ?? local.fits[0]
  if (!fit) return <p>{c.noFile}</p>
  const e = fit.estimate
  const words = (Math.round((local.num_ctx * 0.75) / 1000) * 1000).toLocaleString('en-US')
  return (
    <>
      <p>{c.machineLead(words)}</p>
      <dl className="card__figures">
        <dt>{c.fit}</dt>
        <dd>
          <span className={`fit fit--${e.category}`} title={en.screens.models.fitWhy[e.category]}>
            {fitWords[e.category]}
          </span>
        </dd>
        <dt>{c.speed}</dt>
        <dd>{e.speed.generation ? <Figure rate={e.speed.generation} /> : <span className="card__unknown">{c.noSpeed}</span>}</dd>
        <dt>{c.memory}</dt>
        <dd>
          <Figure bytes={e.memory.total} />
        </dd>
        <dt>{c.download}</dt>
        <dd>{formatDownload(fit.file.bytes)}</dd>
      </dl>
      <p className="screen__note">
        <Link to="/benchmarks">{c.test}</Link> — {c.testHelp}
      </p>
      {advanced && local.fits.length > 1 ? <OtherFiles fits={local.fits} /> : null}
    </>
  )
}

function OtherFiles({ fits }: { fits: FileFit[] }) {
  return (
    <div className="screen__advanced">
      <h3>{c.otherFiles}</h3>
      <table className="tech">
        <thead>
          <tr>
            <th>
              <Term id="quantization">{c.quant}</Term>
            </th>
            <th>{c.fit}</th>
            <th>{c.memory}</th>
            <th>{c.speed}</th>
          </tr>
        </thead>
        <tbody>
          {fits.map((f) => (
            <tr key={f.file.id}>
              <td>{f.file.quant}</td>
              <td>{fitWords[f.estimate.category]}</td>
              <td>
                <Figure bytes={f.estimate.memory.total} />
              </td>
              <td>{f.estimate.speed.generation ? <Figure rate={f.estimate.speed.generation} /> : c.noSpeed}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** Download sizes are facts from the catalogue's listing, in the decimal gigabytes download pages use. */
function formatDownload(bytes: number): string {
  const gb = bytes / 1e9
  if (gb < 1) return `${Math.max(1, Math.round(gb * 1000))} MB`
  return `${gb.toFixed(1).replace(/\.0$/, '')} GB`
}
