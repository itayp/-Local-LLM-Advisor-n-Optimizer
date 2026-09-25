import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api } from '../api/client'
import type { Purpose, WatchSettings } from '../api/types'

/**
 * Settings the UI keeps: the Advanced toggle (product rule 2 — technical
 * columns live behind it, off by default), the purposes picked on
 * Recommend (durable so the new-model watch has something to check
 * against even with nobody looking — build-plan step 10), and the watch's
 * own settings (its master switch and notification mode).
 *
 * Persistence: the daemon's `settings` table is the durable, machine-wide
 * copy (CLAUDE.md: "durable settings go through the daemon's settings
 * table"); localStorage below is a per-viewer cache that makes the first
 * paint snappy and keeps the choices working even when the daemon cannot be
 * reached. On mount this reads GET /api/settings once and, if it answers
 * with real values, those win over whatever localStorage had — the same "a
 * measurement replaces an estimate" shape product rule 4 uses for numbers
 * applies here to which copy is trusted. Every localStorage access is
 * guarded — it can be absent or throw. `initial` (tests only) skips both
 * localStorage and the daemon round trip, so a test's chosen state is
 * never raced by a background fetch.
 */
export interface Settings {
  advanced: boolean
  purposes: Purpose[]
  watch: WatchSettings
}

/** A fresh install's watch: checking, but quiet — no popups until the user
 * turns them on in Settings (Go: watch.DefaultSettings). */
export const defaultWatchSettings: WatchSettings = { enabled: true, mode: 'quiet', interval: 0 }

export const defaultSettings: Settings = { advanced: false, purposes: [], watch: defaultWatchSettings }

interface SettingsContextValue {
  settings: Settings
  setAdvanced: (on: boolean) => void
  /** Persists the Recommend screen's own purpose selection. */
  setPurposes: (purposes: Purpose[]) => void
  /** Persists the watch's master switch and notification mode. */
  setWatch: (watch: WatchSettings) => void
}

const SettingsContext = createContext<SettingsContextValue | null>(null)

const storageKey = 'advisor.settings.v1'

function load(): Settings {
  try {
    const raw = globalThis.localStorage?.getItem(storageKey)
    if (!raw) return defaultSettings
    const parsed = JSON.parse(raw) as Partial<Settings>
    return {
      advanced: parsed.advanced === true,
      purposes: Array.isArray(parsed.purposes) ? parsed.purposes : defaultSettings.purposes,
      watch: parsed.watch && typeof parsed.watch === 'object' ? { ...defaultWatchSettings, ...parsed.watch } : defaultWatchSettings,
    }
  } catch {
    return defaultSettings
  }
}

function save(s: Settings) {
  try {
    globalThis.localStorage?.setItem(storageKey, JSON.stringify(s))
  } catch {
    // a private window or blocked storage: the choice lasts for this page
  }
}

export function SettingsProvider({ children, initial }: { children: ReactNode; initial?: Settings }) {
  const [settings, setSettings] = useState<Settings>(() => initial ?? load())

  useEffect(() => {
    save(settings)
  }, [settings])

  // Reconcile with the daemon's durable copy once, on mount. Skipped when
  // `initial` was given (tests forcing a state): they never see a daemon,
  // and a background fetch racing their assertions would only add
  // flakiness. A malformed or unreachable answer (no daemon, nothing
  // stored yet) leaves the cached/default choices standing — never turns a
  // real choice off, and never replaces a chosen purpose list with an
  // empty one the daemon has simply never been told about yet.
  useEffect(() => {
    if (initial) return
    const ac = new AbortController()
    api
      .settings(ac.signal)
      .then((s) => {
        if (typeof s.advanced !== 'boolean') return
        setSettings((cur) => ({
          advanced: s.advanced,
          purposes: Array.isArray(s.purposes) && s.purposes.length > 0 ? s.purposes : cur.purposes,
          watch: s.watch ?? cur.watch,
        }))
      })
      .catch(() => {
        // No daemon yet, or nothing stored: the local choice stands.
      })
    return () => ac.abort()
    // Deliberately once: `initial` is a mount-time switch for tests, not a
    // live prop this effect should re-run for.
  }, [])

  const setAdvanced = useCallback((on: boolean) => {
    setSettings((s) => (s.advanced === on ? s : { ...s, advanced: on }))
    // Best-effort: the choice already applies to this session either way
    // (the state update above), so a daemon that cannot be reached or has
    // no database does not need to be surfaced as an error here.
    api.updateSettings({ advanced: on }).catch(() => undefined)
  }, [])

  // advanced rides along on every PUT (Go's SettingsUpdate.Advanced is not
  // optional, unlike Purposes and Watch): these two read it from the
  // current render's settings rather than force every caller to pass it.
  const setPurposes = useCallback(
    (purposes: Purpose[]) => {
      setSettings((s) => ({ ...s, purposes }))
      api.updateSettings({ advanced: settings.advanced, purposes }).catch(() => undefined)
    },
    [settings.advanced],
  )

  const setWatch = useCallback(
    (watch: WatchSettings) => {
      setSettings((s) => ({ ...s, watch }))
      api.updateSettings({ advanced: settings.advanced, watch }).catch(() => undefined)
    },
    [settings.advanced],
  )

  const value = useMemo(() => ({ settings, setAdvanced, setPurposes, setWatch }), [settings, setAdvanced, setPurposes, setWatch])
  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>
}

export function useSettings(): SettingsContextValue {
  const ctx = useContext(SettingsContext)
  if (!ctx) throw new Error('useSettings must be used inside <SettingsProvider>')
  return ctx
}

/** True when the Advanced toggle is on. The one hook most screens need. */
export function useAdvanced(): boolean {
  return useSettings().settings.advanced
}
