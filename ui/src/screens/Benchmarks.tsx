import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiRequestError, api } from '../api/client'
import type { BenchModel, BenchModelsResponse, BenchPlan, BenchProgress, BenchRun, PullStatus } from '../api/types'
import { Figure } from '../components/Figure'
import { SpeedWithVerdict } from '../components/SpeedVerdict'
import { ModelList } from '../components/ModelList'
import { minutes, TestProgress } from '../components/TestProgress'
import { Working } from '../components/Working'
import { en } from '../copy/en'
import { formatDownload } from '../onboarding/format'
import { Progress } from '../onboarding/Progress'
import { useAdvanced } from '../state/settings'

const c = en.screens.benchmarks

/** The contexts offered besides Ollama's own default, in tokens. */
const contexts = [4096, 8192, 16384, 32768]

/** Tokens to words, the way people count: about three quarters of a word each. */
const words = (tokens: number) => (Math.round((tokens * 0.75) / 100) * 100).toLocaleString('en-US')

/** The installed name a pulled tag shows up under: Ollama adds ":latest" to a tag without one. */
function installedAs(list: BenchModelsResponse, wanted: BenchModel | string): BenchModel | undefined {
  const name = typeof wanted === 'string' ? wanted : wanted.name
  const id = typeof wanted === 'string' ? 0 : (wanted.model_id ?? 0)
  return (
    list.installed.find((m) => m.name === name || m.name === `${name}:latest`) ??
    (id ? list.installed.find((m) => m.model_id === id) : undefined)
  )
}

/**
 * Benchmarks (build-plan step 6): test a model on this computer.
 *
 * The picker lists the models already installed and, apart, the ones the
 * list has that would run here but are not downloaded yet (GET
 * /api/bench/models); "Test it on this computer" elsewhere opens this
 * screen with ?model= already picked. For an installed model the screen
 * asks the daemon what a test would do before offering it (GET
 * /api/bench/plan) — how long it takes, which passages fit, and whether it
 * would spill onto the processor, in which case the test is refused unless
 * asked for anyway. For one not downloaded yet, the one button downloads it
 * and then runs the test, and says both, with the size (product rule 5).
 * Every wait shows what is happening (a bar and the time so far), from the
 * click to the result. Every number with provenance goes through <Figure>:
 * results are measured, the duration and the estimate before are estimated.
 */
export function Benchmarks() {
  const advanced = useAdvanced()
  const [params] = useSearchParams()
  const wanted = params.get('model')
  const [list, setList] = useState<BenchModelsResponse | null>(null)
  const [listAsked, setListAsked] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [model, setModel] = useState('')
  const [ctx, setCtx] = useState(0)
  const [plan, setPlan] = useState<BenchPlan | null>(null)
  const [planning, setPlanning] = useState(false)
  const [progress, setProgress] = useState<BenchProgress | null>(null)
  const [following, setFollowing] = useState<number | null>(null)
  const [shown, setShown] = useState<BenchRun | null>(null)
  const [history, setHistory] = useState<BenchRun[]>([])
  const [compareSelected, setCompareSelected] = useState<number[]>([])
  const [comparing, setComparing] = useState<[BenchRun, BenchRun] | null>(null)
  const [busy, setBusy] = useState<'starting' | 'cancelling' | null>(null)
  // A download started from this screen: its status, polled; and whether
  // the test should start by itself once the plan for the new model is in.
  const [pull, setPull] = useState<PullStatus | null>(null)
  const [autoStart, setAutoStart] = useState(false)
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
      setFollowing(id)
      const end = (run: BenchRun) => {
        setProgress(null)
        setFollowing(null)
        setShown(run)
        loadHistory()
      }
      stopFollowing.current = api.followBench(
        id,
        (p) => {
          if (p.status === 'running' || p.status === 'queued') {
            setProgress(p)
            return
          }
          end(p.run)
        },
        () => {
          // Neither the stream nor the progress could be read (the daemon
          // restarted): read the run as it stands instead.
          api
            .benchRun(id)
            .then((run) => {
              if (run.status !== 'running') end(run)
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
      .benchHistory(ac.signal)
      .then((h) => {
        const runs = h.runs ?? []
        setHistory(runs)
        const running = runs.find((r) => r.status === 'running')
        if (running) follow(running.id)
      })
      .catch(() => undefined)
    // A download started earlier (then the page was left) is picked up.
    api
      .pullStatus(ac.signal)
      .then((st) => {
        if (st.status === 'running') setPull(st)
      })
      .catch(() => undefined)
    return () => {
      ac.abort()
      stopFollowing.current?.()
    }
  }, [follow])

  useEffect(() => {
    const ac = new AbortController()
    api
      .benchModels(ac.signal)
      .then((r) => {
        const l = { ...r, installed: r.installed ?? [], available: r.available ?? [] }
        setList(l)
        setModel((cur) => {
          if (cur) {
            const now = installedAs(l, cur)
            if (now) return now.name
            if (l.available.some((m) => m.name === cur)) return cur
          }
          if (wanted) {
            const w = installedAs(l, wanted) ?? l.available.find((m) => m.name === wanted)
            if (w) return w.name
          }
          return l.installed[0]?.name ?? l.available[0]?.name ?? ''
        })
      })
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => ac.abort()
  }, [listAsked, wanted])

  const selected: BenchModel | undefined = list
    ? (list.installed.find((m) => m.name === model) ?? list.available.find((m) => m.name === model))
    : undefined

  useEffect(() => {
    if (!model || !selected?.installed) {
      setPlan(null)
      return
    }
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
        setAutoStart(false)
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => ac.abort()
  }, [model, ctx, finished, selected?.installed])

  const start = useCallback(
    (anyway: boolean) => {
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
    },
    [model, ctx, follow],
  )

  // Downloaded from here: once the new model's plan is in, the test starts
  // by itself — unless the plan refuses it, which the screen then shows.
  useEffect(() => {
    if (!autoStart || !plan || plan.model !== model) return
    setAutoStart(false)
    if (!plan.refusal) start(false)
  }, [autoStart, plan, model, start])

  // A download in progress is polled once a second.
  useEffect(() => {
    if (pull?.status !== 'running') return
    const id = window.setInterval(() => {
      api
        .pullStatus()
        .then((st) => {
          setPull(st)
          if (st.status === 'done') {
            setAutoStart(true)
            setListAsked((n) => n + 1)
          }
        })
        .catch(() => undefined)
    }, 1000)
    return () => window.clearInterval(id)
  }, [pull?.status])

  const download = () => {
    if (!selected || selected.installed) return
    setError(null)
    setShown(null)
    setPull({ status: 'running', model: selected.name, completed_bytes: 0, total_bytes: selected.download_bytes })
    api.pullStart(selected.name).then(setPull, (err: unknown) => {
      if (err instanceof ApiRequestError && err.status === 409) {
        api.pullStatus().then(setPull, () => setPull(null))
        return
      }
      setPull(null)
      setError(err instanceof Error ? err.message : String(err))
    })
  }

  const cancelDownload = () => {
    api.pullCancel().then(setPull, () => undefined)
  }

  const cancel = () => {
    const id = progress?.run_id ?? following
    if (id === null || id === undefined) return
    setBusy('cancelling')
    stopFollowing.current?.()
    api
      .benchCancel(id)
      .then((run) => {
        setBusy(null)
        setProgress(null)
        setFollowing(null)
        setShown(run)
        loadHistory()
      })
      .catch((err: unknown) => {
        setBusy(null)
        setError(err instanceof Error ? err.message : String(err))
      })
  }

  const testing = progress !== null || following !== null || busy === 'starting'
  const downloading = pull?.status === 'running'
  const locked = testing || downloading

  const toggleCompare = (id: number) => {
    setCompareSelected((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : cur.length < 2 ? [...cur, id] : cur))
  }

  const openCompare = () => {
    const [idA, idB] = compareSelected
    const a = history.find((r) => r.id === idA)
    const b = history.find((r) => r.id === idB)
    if (a && b) setComparing([a, b])
  }

  const show = (r: BenchRun) => {
    setComparing(null)
    if (r.status === 'running') follow(r.id)
    else setShown(r)
  }

  const nothing = list !== null && list.installed.length === 0 && list.available.length === 0

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>
      <p className="screen__lead">{c.lead}</p>

      <ModelList onFetched={() => setListAsked((n) => n + 1)} />

      {list === null && !error ? <Working label={c.loading} /> : null}
      {nothing && list?.catalogue_fetched ? <p className="notice">{c.noModels}</p> : null}

      {list !== null && !nothing ? (
        <form className="bench-form" onSubmit={(e) => e.preventDefault()}>
          <label>
            <span className="setting__label">{c.model}</span>
            <select value={model} disabled={locked} onChange={(e) => setModel(e.target.value)}>
              {list.installed.length > 0 ? (
                <optgroup label={c.groupInstalled}>
                  {list.installed.map((m) => (
                    <option key={m.name} value={m.name}>
                      {c.optionInstalled(m.name, m.display_name)}
                    </option>
                  ))}
                </optgroup>
              ) : null}
              {list.available.length > 0 ? (
                <optgroup label={c.groupAvailable}>
                  {list.available.map((m) => (
                    <option key={m.name} value={m.name}>
                      {c.optionAvailable(m.display_name ?? m.name, formatDownload(m.download_bytes ?? 0))}
                    </option>
                  ))}
                </optgroup>
              ) : null}
            </select>
          </label>
          {selected?.installed ? (
            <label>
              <span className="setting__label">{c.context}</span>
              <select value={ctx} disabled={locked} aria-describedby="bench-context-help" onChange={(e) => setCtx(Number(e.target.value))}>
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
          ) : null}
          {list.too_big > 0 ? <p className="screen__note">{c.tooBig(list.too_big)}</p> : null}
        </form>
      ) : null}

      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : null}

      {selected && !selected.installed && !testing && !autoStart ? (
        <DownloadAndTest m={selected} pull={pull} onDownload={download} onCancel={cancelDownload} />
      ) : null}
      {pull?.status === 'done' && autoStart && !testing ? <Working label={c.downloaded} /> : null}

      {!testing && selected?.installed && planning && !plan ? <Working label={c.planning} /> : null}
      {!testing && selected?.installed && plan && plan.model === model && !autoStart ? (
        <Plan plan={plan} busy={false} onRun={start} />
      ) : null}

      {testing ? <TestProgress p={progress} cancelling={busy === 'cancelling'} onCancel={cancel} /> : null}

      {comparing ? (
        <Compare a={comparing[0]} b={comparing[1]} advanced={advanced} onClose={() => setComparing(null)} />
      ) : shown ? (
        <RunView run={shown} advanced={advanced} />
      ) : null}

      <History runs={history} onShow={show} selected={compareSelected} onToggleSelect={toggleCompare} />
      {compareSelected.length === 2 ? (
        <button type="button" className="button button--secondary" onClick={openCompare}>
          {c.compareButton}
        </button>
      ) : compareSelected.length === 1 ? (
        <p className="screen__note">{c.comparePick}</p>
      ) : null}
    </section>
  )
}

/**
 * DownloadAndTest: a model the list has that is not on this computer yet —
 * what it costs to get, how it should fit, and one button that downloads it
 * and then runs the test (it says both). While it downloads: the bytes so
 * far of the total, and a way to stop.
 */
function DownloadAndTest({
  m,
  pull,
  onDownload,
  onCancel,
}: {
  m: BenchModel
  pull: PullStatus | null
  onDownload: () => void
  onCancel: () => void
}) {
  const size = formatDownload(m.download_bytes ?? 0)
  const mine = pull && (pull.model === m.name || !pull.model)
  if (pull?.status === 'running' && mine) {
    return (
      <div className="bench-download" data-testid="bench-download">
        <Progress label={`${c.downloading}: ${m.display_name ?? m.name}`} completed={pull.completed_bytes} total={pull.total_bytes} />
        <button type="button" className="button button--secondary" onClick={onCancel}>
          {c.downloadCancel}
        </button>
      </div>
    )
  }
  return (
    <div className="bench-download" data-testid="bench-download">
      <p>{c.needsDownload(size)}</p>
      {m.fit ? (
        <p className="screen__note">
          {c.fitLabel}: {en.screens.models.fitCategory[m.fit]} — {en.screens.models.fitWhy[m.fit]}
        </p>
      ) : null}
      {pull?.status === 'running' && !mine ? <p className="screen__note">{c.downloadOther(pull.model ?? '')}</p> : null}
      {pull?.status === 'failed' && mine ? (
        <p className="notice notice--warning" role="alert">
          {c.downloadFailed(pull.error ?? '')}
        </p>
      ) : null}
      {pull?.status === 'cancelled' && mine ? <p className="notice">{c.downloadCancelled}</p> : null}
      <button type="button" className="button" disabled={pull?.status === 'running'} onClick={onDownload}>
        {c.downloadAndRun(size)}
      </button>
    </div>
  )
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
              <SpeedWithVerdict rate={plan.measured.generation_tps} />
            </dd>
          </>
        ) : gen ? (
          <>
            <dt>{c.estimateBefore}</dt>
            <dd>
              <SpeedWithVerdict rate={gen} />
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
        <h2>
          {run.config.model}
          {run.config.catalog_model_id ? (
            <>
              {' '}
              <Link className="screen__note" to={`/models/${run.config.catalog_model_id}`}>
                {c.details}
              </Link>
            </>
          ) : null}
        </h2>
        <span className={`confidence bench-status--${run.status}`}>{c.status[run.status]}</span>
      </header>
      {run.error ? <p className="notice notice--warning">{run.error}</p> : null}
      <dl className="card__figures">
        {run.generation_tps ? (
          <>
            <dt>{c.answering}</dt>
            <dd>
              <SpeedWithVerdict rate={run.generation_tps} verdicts={run.verdicts} />
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

function History({
  runs,
  onShow,
  selected,
  onToggleSelect,
}: {
  runs: BenchRun[]
  onShow: (r: BenchRun) => void
  selected: number[]
  onToggleSelect: (id: number) => void
}) {
  return (
    <>
      <h2 className="bench-history__title">{c.history}</h2>
      {runs.length === 0 ? (
        <p className="screen__note">{c.historyEmpty}</p>
      ) : (
        <table className="tech" data-testid="bench-history">
          <thead>
            <tr>
              <th>{c.compareSelect}</th>
              <th>{c.when}</th>
              <th>{c.model}</th>
              <th>{c.context}</th>
              <th>{c.answering}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => {
              const when = new Date(r.started_at).toLocaleString()
              const checked = selected.includes(r.id)
              return (
                <tr key={r.id}>
                  <td>
                    <input
                      type="checkbox"
                      aria-label={c.compareRowLabel(r.config.model, when)}
                      checked={checked}
                      disabled={!checked && selected.length >= 2}
                      onChange={() => onToggleSelect(r.id)}
                    />
                  </td>
                  <td>{when}</td>
                  <td>{r.config.model}</td>
                  <td>{c.contextOption(words(r.config.num_ctx))}</td>
                  <td>
                    {r.generation_tps ? <SpeedWithVerdict rate={r.generation_tps} verdicts={r.verdicts} compact /> : c.status[r.status]}
                  </td>
                  <td>
                    <button type="button" className="link-button" onClick={() => onShow(r)}>
                      {c.show}
                    </button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </>
  )
}

/**
 * Compare (build-plan step 8): two history runs side by side — whichever
 * two the "Compare" checkboxes picked, not necessarily the same
 * configuration (that automatic check is run.comparison, above). Figures
 * keep their own provenance through <Figure>; the technical configuration
 * only appears under Advanced, like everywhere else (product rule 2).
 */
function Compare({ a, b, advanced, onClose }: { a: BenchRun; b: BenchRun; advanced: boolean; onClose: () => void }) {
  const headlineOf = (r: BenchRun) => r.results.find((x) => x.prompt === r.headline) ?? r.results[0]
  const diffPct =
    a.generation_tps && b.generation_tps && b.generation_tps.value !== 0
      ? Math.round(((a.generation_tps.value - b.generation_tps.value) / b.generation_tps.value) * 100)
      : null
  const rows: [string, (r: BenchRun) => React.ReactNode][] = [
    [c.compareRows.model, (r) => r.config.model],
    [c.compareRows.when, (r) => new Date(r.started_at).toLocaleString()],
    [c.compareRows.context, (r) => c.contextOption(words(r.config.num_ctx))],
    [c.answering, (r) => (r.generation_tps ? <Figure rate={r.generation_tps} /> : '—')],
    [c.reading, (r) => (headlineOf(r)?.prompt_tps ? <Figure rate={headlineOf(r)!.prompt_tps!} /> : '—')],
    [c.firstWord, (r) => (headlineOf(r)?.ttft ? <Figure rate={headlineOf(r)!.ttft!} /> : '—')],
    [c.memoryTaken, (r) => (r.peak_vram ? <Figure bytes={r.peak_vram} /> : '—')],
    [c.loadTime, (r) => (r.load ? <Figure rate={r.load} /> : '—')],
    [c.compareRows.resident, (r) => c.compareRows.residentValue[r.resident]],
  ]
  return (
    <article className="card bench-compare" aria-label={c.compareTitle} data-testid="bench-compare">
      <header className="card__head">
        <h2>{c.compareTitle}</h2>
        <button type="button" className="button button--secondary" onClick={onClose}>
          {c.compareClose}
        </button>
      </header>
      {diffPct !== null ? (
        <p className="screen__note">{c.compareDiff(a.config.model, `${diffPct > 0 ? '+' : ''}${diffPct}%`, b.config.model)}</p>
      ) : null}
      <table className="tech" data-testid="bench-compare-table">
        <thead>
          <tr>
            <th>{c.compareRows.metric}</th>
            <th>{a.config.model}</th>
            <th>{b.config.model}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(([label, value]) => (
            <tr key={label}>
              <th scope="row">{label}</th>
              <td>{value(a)}</td>
              <td>{value(b)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {advanced ? (
        <div className="screen__advanced" data-testid="bench-compare-advanced">
          <h3>{c.advanced.config.label}</h3>
          <table className="tech">
            <thead>
              <tr>
                <th>{c.compareRows.metric}</th>
                <th>{a.config.model}</th>
                <th>{b.config.model}</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <th scope="row">{c.advanced.path.label}</th>
                <td>{a.config.runtime_path}</td>
                <td>{b.config.runtime_path}</td>
              </tr>
              <tr>
                <th scope="row">{c.advanced.kvCache.label}</th>
                <td>{a.config.kv_cache_type}</td>
                <td>{b.config.kv_cache_type}</td>
              </tr>
              <tr>
                <th scope="row">{c.advanced.quantization.label}</th>
                <td>{a.config.quantization}</td>
                <td>{b.config.quantization}</td>
              </tr>
              <tr>
                <th scope="row">{c.advanced.context.label}</th>
                <td>{a.config.effective_ctx.toLocaleString('en-US')}</td>
                <td>{b.config.effective_ctx.toLocaleString('en-US')}</td>
              </tr>
              <tr>
                <th scope="row">{c.advanced.suite.label}</th>
                <td>{a.config.suite_version}</td>
                <td>{b.config.suite_version}</td>
              </tr>
            </tbody>
          </table>
        </div>
      ) : null}
    </article>
  )
}
