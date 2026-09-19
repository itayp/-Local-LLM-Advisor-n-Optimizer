import { useEffect, useRef, useState } from 'react'
import { api } from '../api/client'
import type { BenchProgress, BenchRun, Rate } from '../api/types'
import { Figure } from '../components/Figure'
import { Term } from '../components/Term'
import { en } from '../copy/en'

const c = en.onboarding.tryit

/**
 * Screen 7: a one-minute benchmark (the 500-token passage), the result
 * shown next to the estimate it replaces — one <Figure> with
 * source="estimated", one with source="measured" (product rule 4: the
 * two treatments, side by side, never confused).
 */
export function TryIt({ modelName, priorEstimate, onNext }: { modelName: string; priorEstimate?: Rate; onNext: () => void }) {
  const [progress, setProgress] = useState<BenchProgress | null>(null)
  const [run, setRun] = useState<BenchRun | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [starting, setStarting] = useState(false)
  const stop = useRef<(() => void) | null>(null)

  useEffect(() => () => stop.current?.(), [])

  const start = () => {
    setStarting(true)
    setError(null)
    api
      .benchStart({ model: modelName, prompts: ['500'] })
      .then((r) => {
        setStarting(false)
        stop.current = api.followBench(
          r.id,
          (p) => {
            if (p.status === 'running' || p.status === 'queued') {
              setProgress(p)
              return
            }
            setProgress(null)
            setRun(p.run)
          },
          () => {
            api
              .benchRun(r.id)
              .then((rr) => {
                if (rr.status === 'running') return
                setProgress(null)
                setRun(rr)
              })
              .catch(() => undefined)
          },
        )
      })
      .catch((err: unknown) => {
        setStarting(false)
        setError(err instanceof Error ? err.message : String(err))
      })
  }

  return (
    <section className="onboarding__step" aria-labelledby="onboarding-title">
      <h1 id="onboarding-title">{c.title}</h1>
      <p className="screen__lead">
        {c.lead}
        <Term id="tokens_per_sec">{c.leadTermTokens}</Term>
        {c.leadSuffix}
      </p>
      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : null}
      {!run && !progress ? (
        <button type="button" className="button" disabled={starting} onClick={start}>
          {starting ? c.starting : c.run}
        </button>
      ) : null}
      {progress ? (
        <p className="screen__note" role="status">
          {progress.message}
        </p>
      ) : null}
      {run ? (
        run.status === 'done' && run.generation_tps ? (
          <>
            <dl className="card__figures">
              {priorEstimate ? (
                <>
                  <dt>{c.estimated}</dt>
                  <dd>
                    <Figure rate={priorEstimate} />
                  </dd>
                </>
              ) : null}
              <dt>{c.measured}</dt>
              <dd>
                <Figure rate={run.generation_tps} />
              </dd>
            </dl>
            <button type="button" className="button" onClick={onNext}>
              {c.continue}
            </button>
          </>
        ) : run.status === 'failed' ? (
          <p className="notice notice--warning" role="alert">
            {c.testFailed(run.error ?? '')}
          </p>
        ) : (
          <>
            <p className="notice">{c.noSpeed}</p>
            <button type="button" className="button" onClick={onNext}>
              {c.continue}
            </button>
          </>
        )
      ) : null}
    </section>
  )
}
