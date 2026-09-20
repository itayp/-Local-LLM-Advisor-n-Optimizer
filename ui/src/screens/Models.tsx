import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import type { BackendInfo, FitCategory, HardwareResponse, InstalledModel, Rate } from '../api/types'
import { Figure, formatBytes } from '../components/Figure'
import { Term } from '../components/Term'
import { en } from '../copy/en'
import { useAdvanced } from '../state/settings'

const c = en.screens.models

/** backend_name + name is what makes an installed model unique. */
function key(m: InstalledModel): string {
  return `${m.backend_name}:${m.name}`
}

interface Fit {
  category: FitCategory
  speed?: Rate
}

/**
 * Fits looks up, for every installed model the catalogue can match to an
 * exact tracked file, how it fits and how fast it runs — by asking GET
 * /api/models/{id}/fit (build-plan step 5), the same endpoint Recommend
 * uses. That endpoint already folds in this computer's own benchmark
 * evidence (bench.go's write-back), so a model that has been tested shows
 * a measured speed here with no extra work. One request per distinct
 * catalogue size, not per installed model — several quants of the same
 * size share it. A model the catalogue does not track (catalog_match is
 * "model" or "unknown") has no file to look up: its fit stays unknown, in
 * words, never a guess (D-21).
 */
function useFits(models: InstalledModel[] | null): Record<string, Fit> {
  const [fits, setFits] = useState<Record<string, Fit>>({})
  useEffect(() => {
    if (!models || models.length === 0) return
    const ac = new AbortController()
    const ids = new Set<number>()
    for (const m of models) {
      if (m.catalog_match === 'file' && m.catalog_model_id) ids.add(m.catalog_model_id)
    }
    ids.forEach((id) => {
      api
        .modelFit(id, undefined, ac.signal)
        .then((resp) => {
          setFits((cur) => {
            const next = { ...cur }
            for (const m of models) {
              if (m.catalog_match !== 'file' || m.catalog_model_id !== id) continue
              const match = resp.fits.find((f) => f.file.id === m.catalog_file_id)
              if (match) next[key(m)] = { category: match.estimate.category, speed: match.estimate.speed.generation }
            }
            return next
          })
        })
        .catch(() => {
          // No fit for this size (hardware still detecting, the size fell
          // out of the catalogue, ...): the row shows "can't tell" rather
          // than blocking the rest of the list.
        })
    })
    return () => ac.abort()
  }, [models])
  return fits
}

/**
 * Models (build-plan step 8): every model Ollama has downloaded, whether
 * it fits this computer and how fast it runs (estimated until a benchmark
 * measures it), and the free space left on the models volume. "Remove"
 * asks first, naming the GB it frees — a fact the row already has, so the
 * confirm needs no round trip of its own (product rule 5). Family,
 * parameters, quantization and backend are technical columns: hidden until
 * the Advanced toggle is on (product rule 2), off by default.
 */
export function Models() {
  const advanced = useAdvanced()
  const [models, setModels] = useState<InstalledModel[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [backend, setBackend] = useState<BackendInfo | null>(null)
  const [hw, setHw] = useState<HardwareResponse | null>(null)
  const [confirming, setConfirming] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)
  const [removeError, setRemoveError] = useState<string | null>(null)
  const fits = useFits(models)

  useEffect(() => {
    const ac = new AbortController()
    api
      .installedModels(ac.signal)
      .then((r) => setModels(r.models ?? []))
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setError(err instanceof Error ? err.message : String(err))
      })
    api
      .backends(ac.signal)
      .then((r) => setBackend(r.backends.find((b) => b.name === 'ollama') ?? r.backends[0] ?? null))
      .catch(() => undefined)
    api
      .hardware(ac.signal)
      .then(setHw)
      .catch(() => undefined)
    return () => ac.abort()
  }, [])

  const remove = (m: InstalledModel) => {
    setRemoving(key(m))
    setRemoveError(null)
    api
      .removeModel(m.backend_name, m.name)
      .then((r) => {
        setModels(r.models ?? [])
        setConfirming(null)
        setRemoving(null)
      })
      .catch((err: unknown) => {
        setRemoving(null)
        setRemoveError(c.removeFailed(m.name, err instanceof Error ? err.message : String(err)))
      })
  }

  const confirmed = confirming ? (models ?? []).find((m) => key(m) === confirming) : undefined

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>
      <p className="screen__lead">{c.lead}</p>

      {error ? (
        <p className="notice notice--warning" role="alert">
          {c.failed(error)}
        </p>
      ) : models === null ? (
        <p className="screen__note" role="status">
          {c.loading}
        </p>
      ) : backend && backend.state !== 'running' ? (
        <p className="notice">{c.backendNotRunning}</p>
      ) : models.length === 0 ? (
        <div className="notice" data-testid="models-empty">
          <p>{c.empty}</p>
          <Link className="button" to="/recommend">
            {c.goToRecommend}
          </Link>
        </div>
      ) : (
        <>
          <table className="models-table" data-testid="models-table">
            <thead>
              <tr>
                <th>{c.name}</th>
                <th>{c.fit}</th>
                <th>{c.speed}</th>
                <th>{c.size}</th>
                {advanced ? (
                  <>
                    <th>{c.advanced.family}</th>
                    <th>{c.advanced.parameters}</th>
                    <th data-testid="models-advanced">
                      <Term id="quantization">{c.advanced.quantization}</Term>
                    </th>
                    <th>{c.advanced.backend}</th>
                  </>
                ) : null}
                <th />
              </tr>
            </thead>
            <tbody>
              {models.map((m) => {
                const fit = fits[key(m)]
                const known = m.catalog_match === 'file'
                return (
                  <tr key={key(m)}>
                    <td>{m.name}</td>
                    <td>
                      {!known ? (
                        <span className="fit fit--unknown" title={c.fitUnknown}>
                          {c.fitCategory.unknown}
                        </span>
                      ) : fit ? (
                        <span className={`fit fit--${fit.category}`} title={c.fitWhy[fit.category]}>
                          {c.fitCategory[fit.category]}
                        </span>
                      ) : (
                        <span className="screen__note">{c.fitChecking}</span>
                      )}
                    </td>
                    <td>
                      {fit?.speed ? (
                        <Figure rate={fit.speed} />
                      ) : known && !fit ? (
                        <span className="screen__note">{c.fitChecking}</span>
                      ) : (
                        <span className="card__unknown">{c.noSpeed}</span>
                      )}
                    </td>
                    <td>{formatBytes(m.size_bytes)}</td>
                    {advanced ? (
                      <>
                        <td>{m.family ?? c.advanced.unknown}</td>
                        <td>{m.parameter_size ?? c.advanced.unknown}</td>
                        <td>{m.quantization ?? c.advanced.unknown}</td>
                        <td>{m.backend_name}</td>
                      </>
                    ) : null}
                    <td>
                      <button type="button" className="link-button" onClick={() => setConfirming(key(m))}>
                        {c.remove}
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>

          {confirmed ? (
            <div className="notice notice--warning models-table__remove" role="alert" data-testid="remove-confirm">
              <p>{c.removeConfirm(confirmed.name, formatBytes(confirmed.size_bytes))}</p>
              <button type="button" className="button" disabled={removing === confirming} onClick={() => remove(confirmed)}>
                {removing === confirming ? c.removing : c.remove}
              </button>{' '}
              <button type="button" className="button button--secondary" disabled={removing === confirming} onClick={() => setConfirming(null)}>
                {c.cancel}
              </button>
            </div>
          ) : null}
          {removeError ? (
            <p className="notice notice--warning" role="alert">
              {removeError}
            </p>
          ) : null}

          <p className="screen__note">
            {hw?.profile?.storage.free_known ? c.freeSpace(formatBytes(hw.profile.storage.free_bytes)) : c.freeSpaceUnknown}
          </p>
        </>
      )}
    </section>
  )
}
