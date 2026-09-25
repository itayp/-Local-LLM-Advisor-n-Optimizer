import { useEffect, useRef, useState } from 'react'
import { api } from '../api/client'
import type { BenchProgress, BenchRun, Rate } from '../api/types'
import { Figure } from '../components/Figure'
import { Term } from '../components/Term'
import { SpeedWithVerdict } from '../components/SpeedVerdict'
import { TestProgress } from '../components/TestProgress'
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
  // From the click until the run ends: the button is gone and the progress
  // shows, so there is never a moment where nothing seems to happen.
  const [testing, setTesting] = useState(false)
  const stop = useRef<(() => void) | null>(null)

  useEffect(() => () => stop.current?.(), [])

  const start = () => {
    setTesting(true)
    setError(null)
    const end = (r: BenchRun) => {
      setProgress(null)
      setTesting(false)
      setRun(r)
    }
    api
      .benchStart({ model: modelName, prompts: ['500'] })
      .then((r) => {
        stop.current = api.followBench(
          r.id,
          (p) => {
            if (p.status === 'running' || p.status === 'queued') {
              setProgress(p)
              return
            }
            end(p.run)
          },
          () => {
            api
              .benchRun(r.id)
              .then((rr) => {
                if (rr.status !== 'running') end(rr)
              })
              .catch(() => undefined)
          },
        )
      })
      .catch((err: unknown) => {
        setTesting(false)
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
      {!run && !testing ? (
        <button type="button" className="button" onClick={start}>
          {c.run}
        </button>
      ) : null}
      {testing ? <TestProgress p={progress} /> : null}
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
                <SpeedWithVerdict rate={run.generation_tps} verdicts={run.verdicts} />
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
