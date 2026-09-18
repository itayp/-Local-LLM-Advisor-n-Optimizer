// Types of what the daemon's API returns. They mirror the Go types in
// internal/ (server.Health, figure.Bytes, figure.Rate, ...), by hand and
// deliberately: keep the two in step when an API type changes.

/**
 * Where a number came from. Product rule 4 lives here: every number a user
 * sees arrives with a Source, and the UI renders the two differently — see
 * components/Figure.tsx. There is no third value.
 */
export type Source = 'estimated' | 'measured'

/** A memory or disk size, with its provenance (Go: figure.Bytes). */
export interface Bytes {
  value: number
  source: Source
}

/**
 * A throughput or latency, with its provenance (Go: figure.Rate). An
 * estimate is a range (low..high); a measurement is a point (low == high).
 */
export interface Rate {
  value: number
  low: number
  high: number
  unit: string
  source: Source
}

/** GET /api/health — the whole API in build-plan step 1. */
export interface Health {
  version: string
  os: 'linux' | 'darwin' | 'windows' | string
  arch: string
  go_version: string
}

/** Every non-2xx API response. */
export interface APIError {
  error: { code: string; message: string }
}

/** What a user wants local AI for. Shared with Go's catalog.Purpose. */
export type Purpose =
  | 'coding'
  | 'chat'
  | 'reasoning'
  | 'long_context'
  | 'vision'
  | 'agentic'
  | 'writing'
