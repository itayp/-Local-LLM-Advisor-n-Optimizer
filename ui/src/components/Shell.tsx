import { useEffect, useState } from 'react'
import { NavLink, Outlet } from 'react-router'
import { api } from '../api/client'
import type { Health } from '../api/types'
import { en } from '../copy/en'
import { screens } from '../screens'
import { useAdvanced } from '../state/settings'

/**
 * Shell is the frame every screen sits in: the title, the navigation for
 * the PRD §17 workflow, and a footer with the daemon's version from
 * GET /api/health — the one API call that exists in step 1.
 */
export function Shell() {
  const advanced = useAdvanced()
  const [health, setHealth] = useState<Health | null>(null)
  const [unreachable, setUnreachable] = useState(false)

  useEffect(() => {
    const ac = new AbortController()
    api
      .health(ac.signal)
      .then((h) => {
        setHealth(h)
        setUnreachable(false)
      })
      .catch((err: unknown) => {
        if ((err as { name?: string })?.name === 'AbortError') return
        setUnreachable(true)
      })
    return () => ac.abort()
  }, [])

  return (
    <div className="shell">
      <header className="shell__header">
        <div className="shell__brand">
          <span className="shell__title">{en.app.title}</span>
          <span className="shell__tagline">{en.app.tagline}</span>
        </div>
        {advanced ? (
          <span className="badge" data-testid="advanced-badge">
            {en.app.advancedOn}
          </span>
        ) : null}
      </header>
      <nav className="shell__nav" aria-label="Main">
        <ul>
          {screens.map((s) => (
            <li key={s.path}>
              <NavLink to={s.path} end={s.path === '/'} className={({ isActive }) => (isActive ? 'active' : undefined)}>
                {s.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
      <main className="shell__main">
        {unreachable ? (
          <p className="notice notice--warning" role="status">
            {en.app.daemonUnreachable}
          </p>
        ) : null}
        <Outlet />
      </main>
      <footer className="shell__footer">
        {health ? (
          <span data-testid="daemon-version">
            {en.app.version(health.version)} · {health.os}/{health.arch}
          </span>
        ) : null}
      </footer>
    </div>
  )
}
