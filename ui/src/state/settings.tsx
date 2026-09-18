import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

/**
 * Settings the UI keeps: today only the Advanced toggle (product rule 2 —
 * technical columns live behind it, off by default).
 *
 * Persistence: per-viewer in localStorage, so the choice survives a reload.
 * The daemon has a `settings` table for durable, machine-wide settings;
 * step 8 wires this state to GET/PUT /api/settings and localStorage becomes
 * a cache. Every localStorage access is guarded — it can be absent or throw.
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

  const setAdvanced = useCallback((on: boolean) => {
    setSettings((s) => (s.advanced === on ? s : { ...s, advanced: on }))
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
