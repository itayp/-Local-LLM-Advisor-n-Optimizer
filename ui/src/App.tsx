import { Route, Routes } from 'react-router'
import { Shell } from './components/Shell'
import { screens } from './screens'
import { defaultSettings, SettingsProvider, type Settings } from './state/settings'

/**
 * App is the route tree. The router itself (BrowserRouter in main.tsx,
 * MemoryRouter in tests) wraps it, so tests can start on any screen.
 *
 * initialSettings is a test-only seam (SettingsProvider's own `initial`):
 * a partial override is filled out with defaultSettings, so a test that
 * only cares about `advanced` does not also have to spell out `purposes`
 * and `watch`.
 */
export function App({ initialSettings }: { initialSettings?: Partial<Settings> }) {
  const initial: Settings | undefined = initialSettings ? { ...defaultSettings, ...initialSettings } : undefined
  return (
    <SettingsProvider initial={initial}>
      <Routes>
        <Route element={<Shell />}>
          {screens.map((s) => (
            <Route key={s.path} path={s.path} element={<s.Component />} />
          ))}
        </Route>
      </Routes>
    </SettingsProvider>
  )
}
