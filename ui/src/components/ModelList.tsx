import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiRequestError, api } from '../api/client'
import type { CatalogStatus } from '../api/types'
import { en } from '../copy/en'
import { formatDay } from './PublicFigure'
import { useElapsed } from './Working'

const c = en.modelList

/** How often a running fetch is asked how far it has got. */
const pollEvery = 1000

/**
 * useCatalogStatus reads GET /api/catalog/status, and — while a fetch of
 * the model list runs, whoever started it — keeps reading it once a second
 * for the progress bar. fetchList starts a fetch (POST /api/catalog/refresh)
 * and resolves when it is over; onFetched runs after every fetch that ends.
 */
export function useCatalogStatus(onFetched?: () => void) {
  const [status, setStatus] = useState<CatalogStatus | null>(null)
  const [failed, setFailed] = useState<string | null>(null)
  const [stopped, setStopped] = useState<string | null>(null)
  const [posting, setPosting] = useState(false)
  const wasRunning = useRef(false)
  const wasPublic = useRef(false)
  const postingNow = useRef(false)
  const done = useRef(onFetched)
  done.current = onFetched

  const read = useCallback((signal?: AbortSignal) => {
    return api
      .catalogStatus(signal)
      .then((s) => {
        setStatus(s)
        if (wasRunning.current && !s.running && !postingNow.current) done.current?.()
        wasRunning.current = s.running || postingNow.current
        // The public scores finish later, in the background: ask again then too.
        if (wasPublic.current && !s.public_running) done.current?.()
        wasPublic.current = s.public_running
        return s
      })
      .catch(() => null) // the status is a convenience: a screen still works without it
  }, [])

  useEffect(() => {
    const ac = new AbortController()
    void read(ac.signal)
    return () => ac.abort()
  }, [read])

  const running = posting || status?.running === true
  const publicRunning = !running && status?.public_running === true
  const polling = running || status?.public_running === true
  useEffect(() => {
    if (!polling) return
    const id = window.setInterval(() => void read(), pollEvery)
    return () => window.clearInterval(id)
  }, [polling, read])

  const fetchList = useCallback(() => {
    setFailed(null)
    setStopped(null)
    setPosting(true)
    postingNow.current = true
    wasRunning.current = true
    // The fetch answers only when it is over; the status is polled meanwhile.
    window.setTimeout(() => void read(), 300)
    return api
      .refreshCatalog()
      .then((rep) => {
        if (rep.stopped) setStopped(rep.stopped)
      })
      .catch((err: unknown) => {
        // 409: someone else's fetch is running — follow that one instead.
        if (err instanceof ApiRequestError && err.status === 409) return
        setFailed(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        postingNow.current = false
        setPosting(false)
        void read()
      })
  }, [read])

  return { status, running, publicRunning, failed, stopped, fetchList }
}

/**
 * ModelList is the model list's state on a screen that needs it: while a
 * fetch runs, what it is reading and how far it has got; before the list
 * has ever been fetched, why it is needed and the one button that fetches
 * it (product rule 5: it says what it does and what it costs); once it has
 * been, nothing — or, `compact`, one quiet line with the date and a way to
 * fetch it again. No screen that needs the list is a dead end.
 */
export function ModelList({ onFetched, compact = false }: { onFetched?: () => void; compact?: boolean }) {
  const { status, running, publicRunning, failed, stopped, fetchList } = useCatalogStatus(onFetched)
  if (!status && !running) return null
  const problems = (
    <>
      {failed ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(failed)}
        </p>
      ) : null}
      {stopped ? (
        <p className="notice notice--warning" role="alert">
          {c.stopped(stopped)}
        </p>
      ) : null}
    </>
  )
  if (running) {
    return (
      <>
        <FetchProgress status={status} />
        {problems}
      </>
    )
  }
  if (status && !status.fetched) {
    return (
      <div className="notice" data-testid="model-list-missing">
        <h2 className="notice__title">{c.missingTitle}</h2>
        <p>{c.missingLead}</p>
        <button type="button" className="button" onClick={() => void fetchList()}>
          {c.fetch}
        </button>
        {problems}
      </div>
    )
  }
  if (publicRunning) {
    return (
      <>
        <PublicInBackground status={status} />
        {problems}
      </>
    )
  }
  if (!compact) return problems
  const when = status?.last_refresh?.finished_at || status?.last_refresh?.started_at
  return (
    <>
      <p className="screen__note" data-testid="model-list-fetched">
        {when ? c.fetchedOn(formatDay(when.slice(0, 10))) : null}{' '}
        <button type="button" className="link-button" onClick={() => void fetchList()}>
          {c.refetch}
        </button>
      </p>
      {problems}
    </>
  )
}

/** FetchProgress: what is being read now, a bar, and the time so far. */
export function FetchProgress({ status }: { status: CatalogStatus | null }) {
  const p = status?.progress
  const s = useElapsed(p?.started_at)
  return (
    <div className="working notice" role="status" data-testid="model-list-fetching">
      <p className="working__label">{p ? c.phaseModels : c.starting}</p>
      {p && p.total > 0 && p.done > 0 ? (
        <progress className="working__bar" aria-label={c.progressLabel} value={p.done} max={p.total} />
      ) : (
        <progress className="working__bar" aria-label={c.progressLabel} />
      )}
      <p className="screen__note">
        {p ? `${p.message}… ` : ''}
        {p && p.total > 0 ? `${c.partOf(p.done, p.total)} · ` : ''}
        {en.working.elapsed(s)}
      </p>
    </div>
  )
}

/**
 * PublicInBackground: the public scores downloading after the list is in —
 * quiet, not in the way: which source and what of it, a moving bar, and the
 * time so far. The screens ask again by themselves when it ends.
 */
export function PublicInBackground({ status }: { status: CatalogStatus | null }) {
  const p = status?.public_progress
  const s = useElapsed(p?.started_at)
  return (
    <div className="working working--quiet" role="status" data-testid="public-background">
      <p className="working__label">{c.publicBackground}</p>
      <progress className="working__bar" aria-label={c.publicBackground} />
      <p className="screen__note">
        {p ? `${p.message}… ` : ''}
        {en.working.elapsed(s)}. {c.publicBackgroundLead}
      </p>
    </div>
  )
}
