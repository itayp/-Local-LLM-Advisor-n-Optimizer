import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api/client'
import type { BenchPlan, BenchProgress, BenchRun, InstalledModel } from '../api/types'
import { Figure } from '../components/Figure'
import { en } from '../copy/en'
import { useAdvanced } from '../state/settings'

const c = en.screens.benchmarks

/** The contexts offered besides Ollama's own default, in tokens. */
const contexts = [4096, 8192, 16384, 32768]

/** Tokens to words, the way people count: about three quarters of a word each. */
const words = (tokens: number) => (Math.round((tokens * 0.75) / 100) * 100).toLocaleString('en-US')

/**
 * Benchmarks (build-plan step 6): test an installed model on this computer.
 *
 * The screen asks the daemon what a test would do before offering to run it
 * (GET /api/bench/plan) — how long it takes, which passages fit, and whether
 * the estimate already says the model would spill onto the processor, in
 * which case the test is refused unless asked for anyway. The button says
 * what it will do and how long it takes (product rule 5). While a test runs
 * the screen follows its progress stream and offers to stop it, which frees
 * the model's memory. Every number with provenance goes through <Figure>:
 * results are measured, the duration and the estimate before are estimated.
 *
 * Step 8 builds the full Benchmarks screen (compare two runs side by side);
 * this is what the step 6 gate needs on a machine without a terminal.
 */
export function Benchmarks() {
  const advanced = useAdvanced()
  const [models, setModels] = useState<InstalledModel[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [model, setModel] = useState('')
  const [ctx, setCtx] = useState(0)
  const [plan, setPlan] = useState<BenchPlan | null>(null)
  const [planning, setPlanning] = useState(false)
  const [progress, setProgress] = useState<BenchProgress | null>(null)
  const [shown, setShown] = useState<BenchRun | null>(null)
  const [history, setHistory] = useState<BenchRun[]>([])
  const [busy, setBusy] = useState<'starting' | 'cancelling' | null>(null)
  // Bumped when a run ends, so the plan is asked for again: a finished run
  // replaces the estimate the plan showed (product rule 4).
  const [finished, setFinished] = useState(0)
  const stopFollowing = useRef<(() => void) | null>(null)

  const loadHistory = useCallback(() => {
    setFinished((n) => n + 1)
    api
      .benchHistory()
      .then((h) => setHistory(h.runs ?? []))
      .catch(() => {
        // The history is secondary; the rest of the screen still works.
      })
  }, [])

  const follow = useCallback(
    (id: number) => {
      stopFollowing.current?.()
      stopFollowing.current = api.followBench(
        id,
        (p) => {
          if (p.status === 'running' || p.status === 'queued') {
            setProgress(p)
            return
          }
          setProgress(null)
          setShown(p.run)
          loadHistory()
        },
        () => {
          // The stream broke (the daemon restarted, the network hiccupped):
          // read the run as it stands instead.
          api
            .benchRun(id)
            .then((run) => {
              if (run.status === 'running') return
              setProgress(null)
              setShown(run)
              loadHistory()
            })
            .catch(() => undefined)
        },
      )
    },
    [loadHistory],
  )

  useEffect(() => {
    const ac = new AbortController()
    api
      .installedModels(ac.signal)
      .then((r) => {
        const sorted = [...(r.models ?? [])].sort(
          (a, b) => Number(b.catalog_match === 'file') - Number(a.catalog_match === 'file') || a.size_bytes - b.size_bytes,
        )
        setModels(sorted)
        if (sorted.length > 0) setModel((m) => m || sorted[0].name)
      })
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setError(err instanceof Error ? err.message : String(err))
      })
    api
      .benchHistory(ac.signal)
      .then((h) => {
        const runs = h.runs ?? []
        setHistory(runs)
        const running = runs.find((r) => r.status === 'running')
        if (running) follow(running.id)
      })
      .catch(() => undefined)
    return () => {
      ac.abort()
      stopFollowing.current?.()
    }
  }, [follow])

  useEffect(() => {
    if (!model) return
    const ac = new AbortController()
    setPlanning(true)
    setError(null)
    api
      .benchPlan({ model, num_ctx: ctx || undefined }, ac.signal)
      .then((p) => {
        setPlan(p)
        setPlanning(false)
      })
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setPlan(null)
        setPlanning(false)
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => ac.abort()
  }, [model, ctx, finished])

  const start = (anyway: boolean) => {
    setBusy('starting')
    setError(null)
    setShown(null)
    api
      .benchStart({ model, num_ctx: ctx || undefined, measure_anyway: anyway })
      .then((run) => {
        setBusy(null)
        follow(run.id)
      })
      .catch((err: unknown) => {
        setBusy(null)
        setError(err instanceof Error ? err.message : String(err))
      })
  }

  const cancel = () => {
    if (!progress) return
    setBusy('cancelling')
    stopFollowing.current?.()
    api
      .benchCancel(progress.run_id)
      .then((run) => {
        setBusy(null)
        setProgress(null)
        setShown(run)
        loadHistory()
      })
      .catch((err: unknown) => {
        setBusy(null)
        setError(err instanceof Error ? err.message : String(err))
      })
  }

  const running = progress !== null

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>
      <p className="screen__lead">{c.lead}</p>

      {models === null && !error ? (
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      ) : null}
      {models !== null && models.length === 0 ? <p className="notice">{c.noModels}</p> : null}

      {models !== null && models.length > 0 ? (
        <form className="bench-form" onSubmit={(e) => e.preventDefault()}>
          <label>
            <span className="setting__label">{c.model}</span>
            <select value={model} disabled={running} onChange={(e) => setModel(e.target.value)}>
              {models.map((m) => (
                <option key={m.name} value={m.name}>
                  {m.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="setting__label">{c.context}</span>
            <select value={ctx} disabled={running} aria-describedby="bench-context-help" onChange={(e) => setCtx(Number(e.target.value))}>
              <option value={0}>{c.contextDefault(plan && plan.num_ctx_source === 'ollama_default' ? words(plan.num_ctx) : '…')}</option>
              {contexts.map((n) => (
                <option key={n} value={n}>
                  {c.contextOption(words(n))}
                </option>
              ))}
            </select>
            <span className="setting__help" id="bench-context-help" aria-hidden="true">
              {c.contextHelp}
            </span>
          </label>
        </form>
      ) : null}

      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : null}

      {!running && planning && !plan ? <p className="screen__note">{c.planning}</p> : null}
      {!running && plan && plan.model === model ? (
        <Plan plan={plan} busy={busy === 'starting'} onRun={start} />
      ) : null}

      {progress ? <Running p={progress} cancelling={busy === 'cancelling'} onCancel={cancel} /> : null}

      {shown ? <RunView run={shown} advanced={advanced} /> : null}

      <History runs={history} onShow={(r) => (r.status === 'running' ? follow(r.id) : setShown(r))} />
    </section>
  )
}

/** An estimated duration in seconds as whole minutes, at least one. */
function minutes(low: number, high: number): [number, number] {
  const lo = Math.max(1, Math.round(low / 60))
  return [lo, Math.max(lo, Math.round(high / 60))]
}

function Plan({ plan, busy, onRun }: { plan: BenchPlan; busy: boolean; onRun: (anyway: boolean) => void }) {
  const gen = plan.estimate.speed.generation
  const refused = plan.refusal !== undefined && plan.refusal !== ''
  const canAnyway = refused && plan.refusal_code !== 'nothing_fits'
  return (
    <div className="bench-plan" data-testid="bench-plan">
      <dl className="card__figures">
        <dt>{c.takes}</dt>
        <dd>
          {plan.duration ? (
            <Figure value={c.minutes(...minutes(plan.duration.low, plan.duration.high))} source="estimated" />
          ) : (
            <span className="card__unknown">{c.takesUnknown}</span>
          )}
        </dd>
        {plan.measured ? (
          <>
            <dt>{c.lastMeasured}</dt>
            <dd>
              <Figure rate={plan.measured.generation_tps} />
            </dd>
          </>
        ) : gen ? (
          <>
            <dt>{c.estimateBefore}</dt>
            <dd>
              <Figure rate={gen} />
            </dd>
          </>
        ) : null}
      </dl>
      {plan.prompts
        .filter((p) => p.skip)
        .map((p) => (
          <p key={p.id} className="screen__note">
            {c.promptSkipped(p.id, p.skip ?? '')}
          </p>
        ))}
      {refused ? (
        <div className="notice notice--warning" data-testid="bench-refusal">
          <p>{plan.refusal}</p>
          {canAnyway ? (
            <button type="button" className="button button--secondary" disabled={busy} onClick={() => onRun(true)}>
              {c.runAnyway}
            </button>
          ) : null}
        </div>
      ) : (
        <button type="button" className="button" disabled={busy} onClick={() => onRun(false)}>
          {busy ? c.starting : plan.duration ? c.run(...minutes(plan.duration.low, plan.duration.high)) : c.runUnknown}
        </button>
      )}
    </div>
  )
}

function Running({ p, cancelling, onCancel }: { p: BenchProgress; cancelling: boolean; onCancel: () => void }) {
  return (
    <div className="bench-running notice" data-testid="bench-running">
      <p role="status">{cancelling ? c.cancelling : p.message}</p>
      <progress aria-label={c.progressLabel} value={p.step} max={Math.max(p.steps, 1)} />
      <p className="screen__note">
        {c.step(p.step, p.steps)}
        {p.remaining ? (
          <>
            {' · '}
            {c.timeLeft}{' '}
            <Figure
              value={p.remaining.high < 60 ? c.underAMinute : c.minutes(...minutes(p.remaining.low, p.remaining.high))}
              source="estimated"
            />
          </>
        ) : null}
      </p>
      <button type="button" className="button button--secondary" disabled={cancelling} onClick={onCancel}>
        {c.cancel}
      </button>
    </div>
  )
}

function RunView({ run, advanced }: { run: BenchRun; advanced: boolean }) {
  const head = run.results.find((r) => r.prompt === run.headline) ?? run.results[0]
  const before = run.estimate?.speed.generation
  const notes = [
    ...(run.notes ?? []),
    ...run.results.flatMap((r) =>
      [...(r.generation_unknown ? [r.generation_unknown] : []), ...(r.notes ?? [])].map((n) => c.promptNote(r.prompt, n)),
    ),
  ]
  return (
    <article className="card bench-run" aria-label={`${c.result}: ${run.config.model}`} data-testid="bench-run">
      <header className="card__head">
        <h2>{run.config.model}</h2>
        <span className={`confidence bench-status--${run.status}`}>{c.status[run.status]}</span>
      </header>
      {run.error ? <p className="notice notice--warning">{run.error}</p> : null}
      <dl className="card__figures">
        {run.generation_tps ? (
          <>
            <dt>{c.answering}</dt>
            <dd>
              <Figure rate={run.generation_tps} />
            </dd>
          </>
        ) : null}
        {before ? (
          <>
            <dt>{c.estimateBefore}</dt>
            <dd>
              <Figure rate={before} />
            </dd>
          </>
        ) : null}
        {head?.prompt_tps ? (
          <>
            <dt>{c.reading}</dt>
            <dd>
              <Figure rate={head.prompt_tps} />
            </dd>
          </>
        ) : null}
        {head?.ttft ? (
          <>
            <dt>{c.firstWord}</dt>
            <dd>
              <Figure rate={head.ttft} />
            </dd>
          </>
        ) : null}
        {run.peak_vram ? (
          <>
            <dt>{c.memoryTaken}</dt>
            <dd>
              <Figure bytes={run.peak_vram} />
            </dd>
          </>
        ) : null}
        {run.load ? (
          <>
            <dt>{c.loadTime}</dt>
            <dd>
              <Figure rate={run.load} />
            </dd>
          </>
        ) : null}
      </dl>
      {run.status === 'done' ? <p className="screen__note">{run.replaced ? c.replaced : c.notReplaced}</p> : null}
      {run.unloaded !== undefined ? <p className="screen__note">{run.unloaded ? c.unloaded : c.notUnloaded}</p> : null}
      {run.comparison ? (
        <p className="screen__note">{c.comparison(`${run.comparison.diff_pct > 0 ? '+' : ''}${run.comparison.diff_pct}%`, run.comparison.run_id)}</p>
      ) : null}
      {run.skipped?.length ? (
        <ul className="notes">
          {run.skipped.map((s) => (
            <li key={s.prompt}>{c.promptSkipped(s.prompt, s.why)}</li>
          ))}
        </ul>
      ) : null}
      {notes.length ? (
        <>
          <h3>{c.notes}</h3>
          <ul className="notes">
            {notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </>
      ) : null}
      {run.sampler_note ? (
        <p className="screen__note">
          {c.notSampled}: {run.sampler_note}
        </p>
      ) : null}
      {advanced ? <Technical run={run} /> : null}
    </article>
  )
}

function Technical({ run }: { run: BenchRun }) {
  const a = c.advanced
  const cfg = run.config
  const onOff = (known: boolean, v: boolean) => (known ? (v ? a.yes : a.no) : a.unknown)
  const resources = [
    run.gpu_util ? <Figure key="u" rate={run.gpu_util} /> : null,
    run.peak_temp ? <Figure key="t" rate={run.peak_temp} /> : null,
    run.power ? <Figure key="p" rate={run.power} /> : null,
    run.peak_ram ? <Figure key="r" bytes={run.peak_ram} /> : null,
  ].filter(Boolean)
  const config: [{ label: string; explain: string }, React.ReactNode][] = [
    [a.path, `${cfg.runtime_path}${cfg.runtime_path_evidence ? ` — ${cfg.runtime_path_evidence}` : ''}`],
    [a.kvCache, cfg.kv_cache_type],
    [a.flash, onOff(cfg.flash_attention_known, cfg.flash_attention)],
    [a.context, `${cfg.num_ctx.toLocaleString('en-US')}${cfg.effective_ctx && cfg.effective_ctx !== cfg.num_ctx ? ` (ran ${cfg.effective_ctx.toLocaleString('en-US')})` : ''}`],
    [a.quantization, cfg.quantization],
    [a.suite, `${cfg.suite_version} · ${cfg.backend} ${cfg.backend_version} · advisor ${cfg.daemon_version}`],
    [a.resources, resources.length ? <span className="bench-resources">{resources}</span> : a.unknown],
  ]
  return (
    <div className="screen__advanced" data-testid="bench-advanced">
      <h3>{a.title}</h3>
      <table className="tech">
        <thead>
          <tr>
            <th title={a.prompt.explain}>{a.prompt.label}</th>
            <th title={a.generation.explain}>{a.generation.label}</th>
            <th title={a.promptTps.explain}>{a.promptTps.label}</th>
            <th title={a.ttft.explain}>{a.ttft.label}</th>
            <th title={a.spread.explain}>{a.spread.label}</th>
          </tr>
        </thead>
        <tbody>
          {run.results.map((r) => (
            <tr key={r.prompt}>
              <th scope="row">{r.prompt_tokens.toLocaleString('en-US')}</th>
              <td>{r.generation_tps ? <Figure rate={r.generation_tps} /> : '—'}</td>
              <td>{r.prompt_tps ? <Figure rate={r.prompt_tps} /> : '—'}</td>
              <td>{r.ttft ? <Figure rate={r.ttft} /> : '—'}</td>
              <td>{r.spread_pct}%</td>
            </tr>
          ))}
        </tbody>
      </table>
      <ul className="notes">
        {[a.prompt, a.generation, a.promptTps, a.ttft, a.spread].map((t) => (
          <li key={t.label}>
            {t.label}: {t.explain}
          </li>
        ))}
      </ul>
      <table className="tech">
        <tbody>
          {config.map(([term, value]) => (
            <tr key={term.label}>
              <th scope="row">{term.label}</th>
              <td>{value}</td>
              <td className="tech__rule">{term.explain}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function History({ runs, onShow }: { runs: BenchRun[]; onShow: (r: BenchRun) => void }) {
  return (
    <>
      <h2 className="bench-history__title">{c.history}</h2>
      {runs.length === 0 ? (
        <p className="screen__note">{c.historyEmpty}</p>
      ) : (
        <table className="tech" data-testid="bench-history">
          <thead>
            <tr>
              <th>{c.when}</th>
              <th>{c.model}</th>
              <th>{c.context}</th>
              <th>{c.answering}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr key={r.id}>
                <td>{new Date(r.started_at).toLocaleString()}</td>
                <td>{r.config.model}</td>
                <td>{c.contextOption(words(r.config.num_ctx))}</td>
                <td>{r.generation_tps ? <Figure rate={r.generation_tps} /> : c.status[r.status]}</td>
                <td>
                  <button type="button" className="link-button" onClick={() => onShow(r)}>
                    {c.show}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  )
}
