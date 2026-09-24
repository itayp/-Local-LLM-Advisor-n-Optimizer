import type {
  APIError,
  BackendsResponse,
  BackendStartResponse,
  BenchHistory,
  BenchPlan,
  BenchProgress,
  BenchRequest,
  BenchRun,
  ChatAppsResponse,
  InstalledModelsResponse,
  CatalogRefreshReport,
  CatalogResponse,
  HardwareHistory,
  HardwareResponse,
  Health,
  InstallSizeResponse,
  InstallStatus,
  ModelDetailResponse,
  ModelFitResponse,
  OnboardingStatus,
  PullStatus,
  Purpose,
  RecommendResult,
  SettingsResponse,
  SettingsUpdate,
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

async function send<T>(method: 'GET' | 'POST' | 'PUT', path: string, signal?: AbortSignal, body?: unknown): Promise<T> {
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
  /** One catalogue size: what others have published about it, and what this computer would do with it, apart. */
  modelDetail: (modelId: number, signal?: AbortSignal) => get<ModelDetailResponse>(`/models/${modelId}/detail`, signal),
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

  /** Whether the first-run flow (build-plan step 7) has been completed. */
  onboardingStatus: (signal?: AbortSignal) => get<OnboardingStatus>('/onboarding', signal),
  /** Mark first-run setup as finished. */
  onboardingComplete: (signal?: AbortSignal) => send<OnboardingStatus>('POST', '/onboarding/complete', signal),
  /** Every registered runtime's status, detected fresh on every call — cheap, never starts anything. */
  backends: (signal?: AbortSignal) => get<BackendsResponse>('/backends', signal),
  /** name's installer size, before Install ever runs (product rule 5). */
  backendInstallSize: (name: string, signal?: AbortSignal) =>
    get<InstallSizeResponse>(`/backends/${encodeURIComponent(name)}/install-size`, signal),
  /** The latest install status for name; the UI polls this (no SSE — a short, one-viewer action). */
  backendInstallStatus: (name: string, signal?: AbortSignal) =>
    get<InstallStatus>(`/backends/${encodeURIComponent(name)}/install`, signal),
  /** Start installing name. 409 while one is already running for it. */
  backendInstallStart: (name: string, signal?: AbortSignal) =>
    send<InstallStatus>('POST', `/backends/${encodeURIComponent(name)}/install`, signal),
  /** Launch name's runtime; poll backends() afterwards until it reports running. */
  backendStart: (name: string, signal?: AbortSignal) =>
    send<BackendStartResponse>('POST', `/backends/${encodeURIComponent(name)}/start`, signal),
  /** The latest download status; the UI polls this. */
  pullStatus: (signal?: AbortSignal) => get<PullStatus>('/models/pull', signal),
  /** Start downloading an Ollama tag (a recommendation's pull_name). 409 while one is already running. */
  pullStart: (ollamaTag: string, signal?: AbortSignal) =>
    send<PullStatus>('POST', '/models/pull', signal, { ollama_tag: ollamaTag }),
  /** Stop the running download. */
  pullCancel: (signal?: AbortSignal) => send<PullStatus>('POST', '/models/pull/cancel', signal),
  /** Chat apps already on this machine, detected fresh on every call. The advisor never installs or drives one (D-4). */
  chatApps: (signal?: AbortSignal) => get<ChatAppsResponse>('/chatapps', signal),
  /** The durable, machine-wide settings (build-plan step 8). */
  settings: (signal?: AbortSignal) => get<SettingsResponse>('/settings', signal),
  /** Change them. */
  updateSettings: (update: SettingsUpdate, signal?: AbortSignal) => send<SettingsResponse>('PUT', '/settings', signal, update),
  /** Open the daemon's own data folder in the OS file manager. */
  openDataDir: (signal?: AbortSignal) => send<object>('POST', '/settings/open-data-dir', signal),
  /** Open the folder the runtime keeps its models in. */
  openModelsDir: (signal?: AbortSignal) => send<object>('POST', '/settings/open-models-dir', signal),
  /** Remove an installed model from backendName; answers with the inventory as it now stands. */
  removeModel: (backendName: string, name: string, signal?: AbortSignal) =>
    send<InstalledModelsResponse>('POST', `/backends/${encodeURIComponent(backendName)}/models/remove`, signal, { name }),
}

function benchQuery(req: BenchRequest): string {
  const q = new URLSearchParams({ model: req.model })
  if (req.num_ctx) q.set('num_ctx', String(req.num_ctx))
  if (req.prompts?.length) q.set('prompts', req.prompts.join(','))
  return q.toString()
}
