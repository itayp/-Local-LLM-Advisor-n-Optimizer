import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, beforeEach, vi } from 'vitest'

// The shell calls GET /api/health on mount. No daemon runs under vitest, so
// fetch answers with a fixed health payload; a test that wants the
// "unreachable" state overrides it.
beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify({ version: 'test', os: 'testos', arch: 'testarch', go_version: 'go' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ),
  )
  try {
    localStorage.clear()
  } catch {
    // ignore
  }
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})
