import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api } from '../api/client'

/**
 * Settings the UI keeps: today only the Advanced toggle (product rule 2 —
 * technical columns live behind it, off by default).
 *
 * Persistence: the daemon's `settings` table is the durable, machine-wide
 * copy (CLAUDE.md: "durable settings go through the daemon's settings
 * table"); localStorage below is a per-viewer cache that makes the first
 * paint snappy and keeps the toggle working even when the daemon cannot be
 * reached. On mount this reads GET /api/settings once and, if it answers
 * with a real value, that value wins over whatever localStorage had —
 * the same "a measurement replaces an estimate" shape product rule 4 uses
 * for numbers applies here to which copy is trusted. Every localStorage
 * access is guarded — it can be absent or throw. `initial` (tests only)
 * skips both localStorage and the daemon round trip, so a test's chosen
 * state is never raced by a background fetch.
 */
export interface Settings {
  advanced: boolean
}

export const defaultSettings: Settings = { advanced: false }

interface SettingsContextValue {
  settings: Settings
  setAdvanced: (on: boolean) => void
}

const SettingsContext = createContext<SettingsContextValue | null>(null)

const storageKey = 'advisor.settings.v1'

function load(): Settings {
  try {
    const raw = globalThis.localStorage?.getItem(storageKey)
    if (!raw) return defaultSettings
    const parsed = JSON.parse(raw) as Partial<Settings>
    return { ...defaultSettings, advanced: parsed.advanced === true }
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
  // stored yet) leaves the cached/default choice standing — never turns a
  // real choice off.
  useEffect(() => {
    if (initial) return
    const ac = new AbortController()
    api
      .settings(ac.signal)
      .then((s) => {
        if (typeof s.advanced !== 'boolean') return
        setSettings((cur) => (cur.advanced === s.advanced ? cur : { ...cur, advanced: s.advanced }))
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
    // Best-effort: the toggle already applies to this session either way
    // (the state update above), so a daemon that cannot be reached or has
    // no database does not need to be surfaced as an error here.
    api.updateSettings({ advanced: on }).catch(() => undefined)
  }, [])

  const value = useMemo(() => ({ settings, setAdvanced }), [settings, setAdvanced])
  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>
}

export function useSettings(): SettingsContextValue {
  const ctx = useContext(SettingsContext)
  if (!ctx) throw new Error('useSettings must be used inside <SettingsProvider>')
  return ctx
}

/** True when the Advanced toggle is on. The one hook screens need. */
export function useAdvanced(): boolean {
  return useSettings().settings.advanced
}
