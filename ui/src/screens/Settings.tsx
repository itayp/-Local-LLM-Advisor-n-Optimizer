import { useEffect, useState } from 'react'
import { api } from '../api/client'
import type { HardwareResponse, Health } from '../api/types'
import { formatBytes } from '../components/Figure'
import { en } from '../copy/en'
import { useSettings } from '../state/settings'

const c = en.screens.settings

/**
 * Settings (build-plan step 8): the Advanced toggle, where things live on
 * disk and a button that opens each (D-16), the version, and stub
 * sections for the two settings still to come (notifications, step 10;
 * checking for updates, step 11) — written as what they are, not left
 * blank (CLAUDE.md: "empty states are written, not blank").
 */
export function Settings() {
  const { settings, setAdvanced } = useSettings()
  const [health, setHealth] = useState<Health | null>(null)
  const [hw, setHw] = useState<HardwareResponse | null>(null)
  const [dataDir, setDataDir] = useState<string | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    api
      .health(ac.signal)
      .then(setHealth)
      .catch(() => undefined)
    api
      .hardware(ac.signal)
      .then(setHw)
      .catch(() => undefined)
    api
      .settings(ac.signal)
      .then((s) => setDataDir(s.data_dir))
      .catch(() => undefined)
    return () => ac.abort()
  }, [])

  return (
    <section className="screen" aria-labelledby="screen-title">
      <h1 id="screen-title">{c.title}</h1>

      <div className="setting">
        <label className="setting__label">
          <input
            type="checkbox"
            checked={settings.advanced}
            onChange={(e) => setAdvanced(e.target.checked)}
            aria-describedby="advanced-help"
          />{' '}
          {c.advancedLabel}
        </label>
        <p id="advanced-help" className="setting__help">
          {c.advancedHelp}
        </p>
      </div>

      <div className="settings-section">
        <h2>{c.notificationsTitle}</h2>
        <p className="screen__note">{en.placeholder.comingIn(c.notificationsStep)}</p>
      </div>

      <div className="settings-section">
        <h2>{c.updatesTitle}</h2>
        <p className="screen__note">{en.placeholder.comingIn(c.updatesStep)}</p>
      </div>

      <div className="settings-section">
        <dl>
          <div className="settings-row">
            <dt>{c.modelsFolder}</dt>
            <dd>
              <span>
                {hw?.profile ? hw.profile.storage.models_dir : c.modelsFolderUnknown}
                {hw?.profile?.storage.free_known ? ` — ${c.freeSpace(formatBytes(hw.profile.storage.free_bytes))}` : ` — ${c.freeSpaceUnknown}`}
              </span>
              <OpenButton open={() => api.openModelsDir()} />
            </dd>
          </div>
          <div className="settings-row">
            <dt>{c.dataFolder}</dt>
            <dd>
              <span>{dataDir || c.modelsFolderUnknown}</span>
              <OpenButton open={() => api.openDataDir()} />
            </dd>
          </div>
          <div className="settings-row">
            <dt>{en.app.title}</dt>
            <dd>{health ? `${c.version(health.version)} · ${c.versionPlatform(health.os, health.arch)}` : '…'}</dd>
          </div>
        </dl>
      </div>
    </section>
  )
}

function OpenButton({ open }: { open: () => Promise<unknown> }) {
  const [state, setState] = useState<'idle' | 'opening' | 'failed'>('idle')
  const [error, setError] = useState<string | null>(null)
  const click = () => {
    setState('opening')
    setError(null)
    open()
      .then(() => setState('idle'))
      .catch((err: unknown) => {
        setState('failed')
        setError(err instanceof Error ? err.message : String(err))
      })
  }
  return (
    <>
      <button type="button" className="button button--secondary" disabled={state === 'opening'} onClick={click}>
        {state === 'opening' ? c.opening : c.open}
      </button>
      {state === 'failed' ? (
        <span className="notice notice--warning" role="alert">
          {c.openFailed(error ?? '')}
        </span>
      ) : null}
    </>
  )
}
