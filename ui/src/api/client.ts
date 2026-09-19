import type {
  APIError,
  BenchHistory,
  BenchPlan,
  BenchProgress,
  BenchRequest,
  BenchRun,
  InstalledModelsResponse,
  CatalogRefreshReport,
  CatalogResponse,
  HardwareHistory,
  HardwareResponse,
  Health,
  ModelFitResponse,
  Purpose,
  RecommendResult,
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

async function send<T>(method: 'GET' | 'POST', path: string, signal?: AbortSignal, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(base + path, { method, signal, headers, body: body === undefined ? undefined : JSON.stringify(body) })
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
  /** At most three recommendations for this machine and these purposes (none = everyday chat). */
  recommend: (purposes: Purpose[], signal?: AbortSignal) =>
    get<RecommendResult>(`/recommend?purposes=${encodeURIComponent(purposes.join(','))}`, signal),
  /** How every tracked variant of one catalogue size fits; without ctx, at Ollama's own default context. */
  modelFit: (modelId: number, ctx?: number, signal?: AbortSignal) =>
    get<ModelFitResponse>(`/models/${modelId}/fit${ctx ? `?ctx=${ctx}` : ''}`, signal),
  /** What Ollama has installed, as the daemon last read it. */
  installedModels: (signal?: AbortSignal) => get<InstalledModelsResponse>('/models/installed', signal),
  /** What a test would do: the prompts that fit, how long it takes, and whether it is refused. Loads nothing. */
  benchPlan: (req: BenchRequest, signal?: AbortSignal) => get<BenchPlan>(`/bench/plan?${benchQuery(req)}`, signal),
  /** Start a test. 409 while one runs, or (code would_spill) for a configuration that would spill — unless measure_anyway. */
  benchStart: (req: BenchRequest, signal?: AbortSignal) => send<BenchRun>('POST', '/bench', signal, req),
  /** Stop a test and free the model's memory; answers with the run as it ended. */
  benchCancel: (id: number, signal?: AbortSignal) => send<BenchRun>('POST', `/bench/${id}/cancel`, signal),
  benchRun: (id: number, signal?: AbortSignal) => get<BenchRun>(`/bench/${id}`, signal),
  benchHistory: (signal?: AbortSignal) => get<BenchHistory>('/bench/history', signal),
  /**
   * Follow a test as it runs: GET /api/bench/{id} as server-sent events.
   * onProgress sees every event; the last has a finished status. Returns
   * the function that stops following (the test itself runs on).
   */
  followBench: (id: number, onProgress: (p: BenchProgress) => void, onError?: () => void): (() => void) => {
    const es = new EventSource(`${base}/bench/${id}`)
    es.addEventListener('progress', (ev) => {
      const p = JSON.parse((ev as MessageEvent<string>).data) as BenchProgress
      onProgress(p)
      if (p.status !== 'running' && p.status !== 'queued') es.close()
    })
    es.onerror = () => {
      es.close()
      onError?.()
    }
    return () => es.close()
  },
}

function benchQuery(req: BenchRequest): string {
  const q = new URLSearchParams({ model: req.model })
  if (req.num_ctx) q.set('num_ctx', String(req.num_ctx))
  if (req.prompts?.length) q.set('prompts', req.prompts.join(','))
  return q.toString()
}
