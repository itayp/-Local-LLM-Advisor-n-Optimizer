import { Route, Routes } from 'react-router'
import { Shell } from './components/Shell'
import { screens } from './screens'
import { SettingsProvider, type Settings } from './state/settings'

/**
 * App is the route tree. The router itself (BrowserRouter in main.tsx,
 * MemoryRouter in tests) wraps it, so tests can start on any screen.
 */
export function App({ initialSettings }: { initialSettings?: Settings }) {
  return (
    <SettingsProvider initial={initialSettings}>
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
