import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api, ApiRequestError } from '../api/client'
import type { WatchLogEntry, WatchRunSummary } from '../api/types'
import { Working } from '../components/Working'
import { en } from '../copy/en'
import { useSettings } from '../state/settings'

const c = en.screens.watch

/** One entry, with the run it came from — flattened across recent runs so the log reads as one list, newest first. */
interface Row {
  runID: number
  trigger: string
  entry: WatchLogEntry
}

function triggerLabel(trigger: string): string {
  return (c.trigger as Record<string, string>)[trigger] ?? trigger
}

function rows(runs: WatchRunSummary[]): Row[] {
  const out: Row[] = []
  for (const run of runs) {
    for (const entry of run.report.entries) {
      out.push({ runID: run.id, trigger: run.trigger, entry })
    }
  }
  return out
}

/**
 * New models (build-plan step 10): what the daily watch checked, when,
 * what it found, and what it suppressed and why — item 4's log, read
 * straight from GET /api/watch/log with no arithmetic of its own; every
 * sentence here is internal/watch's own words (reasons.go), the same
 * "what the customer reads is templated" rule the rest of the app follows.
 * "Check now" runs one check on demand (POST /api/watch/run); the watch
 * never pulls or switches a model itself (product rule 5) — a notified
 * entry only ever points at that model's card.
 */
export function Watch() {
  const { settings } = useSettings()
  const [runs, setRuns] = useState<WatchRunSummary[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [checking, setChecking] = useState(false)
  const [checkError, setCheckError] = useState<string | null>(null)
  const [checkBusy, setCheckBusy] = useState(false)

  const load = useCallback((signal?: AbortSignal) => {
    return api
      .watchLog(signal)
      .then((r) => {
        setRuns(r.runs)
        setError(null)
      })
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

  const checkNow = () => {
    setChecking(true)
    setCheckError(null)
    setCheckBusy(false)
    api
      .watchRun()
      .then(() => load())
      .catch((err: unknown) => {
        if (err instanceof ApiRequestError && err.code === 'watch_running') {
          setCheckBusy(true)
        } else {
          setCheckError(err instanceof Error ? err.message : String(err))
        }
      })
      .finally(() => setChecking(false))
  }

  const latest = runs && runs.length > 0 ? runs[0] : null
  const entries = runs ? rows(runs) : []

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>
      <p className="screen__lead">{c.lead}</p>

      {!settings.watch.enabled ? <p className="notice">{c.pausedNotice}</p> : null}
      {settings.watch.enabled && settings.watch.mode !== 'on' ? <p className="notice">{c.disabledNotice}</p> : null}
      <p className="screen__note">
        <Link to="/settings">{c.settingsLink}</Link>
      </p>

      <button type="button" className="button button--secondary" disabled={checking} onClick={checkNow}>
        {checking ? c.checking : c.checkNow}
      </button>
      {checkBusy ? <p className="notice">{c.running}</p> : null}
      {checkError ? (
        <p className="notice notice--warning" role="alert">
          {c.checkFailed(checkError)}
        </p>
      ) : null}

      {latest?.report.refresh_error ? (
        <p className="notice notice--warning">{c.refreshError(latest.report.refresh_error)}</p>
      ) : null}
      {latest?.report.maintainer_error ? (
        <p className="notice notice--warning">{c.maintainerError(latest.report.maintainer_error)}</p>
      ) : null}

      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.loadFailed(error)}
        </p>
      ) : runs === null ? (
        <Working label={c.loading} />
      ) : entries.length === 0 ? (
        <p className="notice" data-testid="watch-empty">
          {c.empty}
        </p>
      ) : (
        <table className="models-table" data-testid="watch-table">
          <thead>
            <tr>
              <th>{c.columns.when}</th>
              <th>{c.columns.model}</th>
              <th>{c.columns.result}</th>
              <th>{c.columns.detail}</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((row, i) => (
              <tr key={`${row.runID}-${row.entry.key}-${i}`}>
                <td>
                  {new Date(row.entry.at).toLocaleString()} <span className="screen__note">({triggerLabel(row.trigger)})</span>
                </td>
                <td>{row.entry.name}</td>
                <td>
                  <span className={`badge watch-outcome--${row.entry.outcome}`}>{c.outcome[row.entry.outcome]}</span>
                </td>
                <td>{row.entry.detail}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}
