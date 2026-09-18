import type {
  APIError,
  CatalogRefreshReport,
  CatalogResponse,
  HardwareHistory,
  HardwareResponse,
  Health,
  UnknownInstalledResponse,
} from './types'

// The API is same-origin: the daemon serves both the UI and /api. In
// development Vite proxies /api to the daemon (vite.config.ts).
const base = '/api'

export class ApiRequestError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiRequestError'
    this.status = status
    this.code = code
  }
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  return send<T>('GET', path, signal)
}

async function send<T>(method: 'GET' | 'POST', path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(base + path, { method, signal, headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let code = 'http_error'
    let message = `${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as APIError
      code = body.error.code
      message = body.error.message
    } catch {
      // not a JSON API error; keep the HTTP status text
    }
    throw new ApiRequestError(res.status, code, message)
  }
  return (await res.json()) as T
}

export const api = {
  health: (signal?: AbortSignal) => get<Health>('/health', signal),
  /** Waits while the daemon is still reading the machine (seconds, at start). */
  hardware: (signal?: AbortSignal) => get<HardwareResponse>('/hardware', signal),
  hardwareHistory: (signal?: AbortSignal) => get<HardwareHistory>('/hardware/history', signal),
  hardwareProfile: (id: number, signal?: AbortSignal) => get<HardwareResponse>(`/hardware/profiles/${id}`, signal),
  /** The curated catalogue with what the last refresh resolved. */
  catalog: (signal?: AbortSignal) => get<CatalogResponse>('/catalog', signal),
  /** Installed models the catalogue does not know (the curator's list). */
  catalogUnknown: (signal?: AbortSignal) => get<UnknownInstalledResponse>('/catalog/unknown', signal),
  /** Resolve the catalogue against Hugging Face (metadata only, no weights). 409 while one runs. */
  refreshCatalog: (signal?: AbortSignal) => send<CatalogRefreshReport>('POST', '/catalog/refresh', signal),
}
