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

// --- Hardware (Go: internal/hardware, internal/server/hardware.go) --------
//
// Every number here is read from the operating system, so it is neither
// estimated nor measured (Go tags it `source:"n/a"`) and is shown as plain
// text, not through <Figure>. A value the detector could not read is
// "unknown" (strings) or 0 with its *_known flag false (numbers): render it
// as words, never as "0 GB".

export type Vendor = 'nvidia' | 'amd' | 'intel' | 'apple' | 'qualcomm' | 'unknown'

/** How a runtime drives a GPU (Go: hardware.RuntimePath). */
export type RuntimePath = 'cuda' | 'metal' | 'rocm' | 'vulkan' | 'cpu' | 'none' | 'unknown'

/** The plain-language class of the machine (Go: hardware.Tier). */
export type Tier =
  | 'unknown'
  | 'cpu_only'
  | 'integrated'
  | 'gpu_small'
  | 'gpu_medium'
  | 'gpu_large'
  | 'gpu_xl'

export interface GPU {
  vendor: Vendor
  name: string
  vram_bytes: number
  vram_known: boolean
  vram_source: string
  driver_version: string
  is_integrated: boolean
  integrated_known: boolean
  pci_id?: string
  compute_capability?: string
  gfx_target?: string
  linux_driver?: string
  expected_backend: RuntimePath
  expected_backend_reason: string
  expected_backend_rule: string
  note?: string
}

export interface CPU {
  model: string
  cores_physical: number
  cores_logical: number
  has_avx2: boolean
  has_avx512: boolean
  vector_known: boolean
}

export interface Storage {
  models_dir: string
  models_dir_source: string
  models_dir_exists: boolean
  free_bytes: number
  free_known: boolean
}

/** The machine as detected (Go: hardware.Profile). */
export interface HardwareProfile {
  os: 'linux' | 'darwin' | 'windows' | string
  os_version: string
  kernel?: string
  arch: string
  hostname: string
  cpu: CPU
  ram_bytes: number
  ram_known: boolean
  gpus: GPU[]
  unified_memory: boolean
  gpu_usable_bytes: number
  gpu_usable_known: boolean
  gpu_usable_source: string
  storage: Storage
  is_laptop: boolean
  laptop_known: boolean
  tier: Tier
  summary: string
  notes?: string[]
  filtered_adapters?: string[]
  expectations_from: string
  problems?: string[]
}

/** GET /api/hardware and GET /api/hardware/profiles/{id} (Go: server.HardwareResponse). */
export interface HardwareResponse {
  profile_id: number
  detected_at: string
  fingerprint: string
  changed: boolean
  previous_profile_id?: number
  profile: HardwareProfile
}

/** One hardware configuration this machine has had (Go: server.HardwareConfiguration). */
export interface HardwareConfiguration {
  fingerprint: string
  first_seen: string
  last_seen: string
  starts: number
  latest_profile_id: number
  current: boolean
  tier: Tier
  summary: string
  gpus: string[]
}

/** GET /api/hardware/history (Go: server.HardwareHistory). */
export interface HardwareHistory {
  configurations: HardwareConfiguration[]
}

// --- Catalogue (Go: internal/catalog, internal/server/catalog.go) ---------
//
// The curated families and what the last refresh resolved from Hugging
// Face. Every number here is a fact about a file or a model card (a byte
// count, a parameter count, a header field), not an estimate: Go tags them
// `source:"n/a"` and the UI shows them as plain text. Never count the
// catalogue in copy (product rule 8).

/** SPDX where possible, otherwise the licence's name and where to read it (Go: catalog.License). */
export interface License {
  spdx?: string
  name?: string
  url?: string
}

/** One size of a family, as families.yaml states it (Go: catalog.Size). */
export interface CatalogSize {
  parameters: number
  /** Parameters used per token when fewer than all (mixture-of-experts); absent = dense. */
  active_parameters?: number
  context_length: number
  ollama_tag: string
  hf_repo: string
  /** The original model's repo, where its public scores live (step 9b). */
  hf_base_repo: string
  /** Other names a GGUF card may give for the same original weights (a renamed repo, a BF16 copy). */
  hf_base_same_as?: string[]
  /** The quant ollama_tag pulls, read by hand from the Ollama library; absent = the usual default. */
  ollama_quant?: string
}

/** The header fields step 5's estimator reads (Go: catalog.GGUFHeader). */
export interface GGUFHeader {
  architecture: string
  gguf_version: number
  tensor_count: number
  block_count: number
  head_count: number
  head_count_kv: number
  head_count_kv_stated: boolean
  /** 0 when the model does not state one. */
  key_length: number
  value_length: number
  embedding_length: number
  context_length: number
  sliding_window: number
  full_attention_interval: number
  /** -1 when the file does not state it. */
  file_type: number
  file_type_name: string
  expert_count: number
  expert_used_count: number
  has_vision: boolean
  complete: boolean
}

export type FileRole = 'model' | 'projector'

/** One quant variant (or the vision encoder) of a size (Go: catalog.File). */
export interface CatalogFile {
  id: number
  model_id: number
  filename: string
  role: FileRole
  quant: string
  sha?: string
  parts: number
  /** The download size, summed across parts — a fact from the Hub listing. */
  bytes: number
  bits_per_weight: number
  present: boolean
  header: GGUFHeader
  /** What each layer keeps in memory as the context grows; derived, never stored. */
  layout: Layout
  fetched_at: string
}

/** Go: catalog.LayerGroup — a run of layers with the same cache shape. */
export interface LayerGroup {
  kind: 'full' | 'sliding'
  layers: number
  kv_heads: number
  key_length: number
  /** 0: no value cache (multi-head latent attention). */
  value_length: number
  /** Sliding layers only, in tokens. */
  window: number
}

/** Go: catalog.Layout. */
export interface Layout {
  groups: LayerGroup[] | null
  recurrent_layers: number
  recurrent_state_elements: number
  stateless_layers: number
  /** How the layout was established; feeds a recommendation's confidence. */
  basis: 'uniform' | 'stated' | 'architecture' | 'incomplete' | ''
  notes?: string[]
}

/** One catalogue size with what the last refresh learned (Go: catalog.Model). */
export interface CatalogModel {
  /** 0 until the daemon has stored the size. */
  id: number
  family_id: string
  size: CatalogSize
  present: boolean
  hf_sha?: string
  parameters_counted: number
  /** Absent: never resolved. */
  refreshed_at?: string
  refresh_error?: string
  /** When the original model was released (YYYY-MM-DD); display only, never scored. */
  released_at?: string
  files: CatalogFile[]
}

/** Go: server.CatalogFamily. */
export interface CatalogFamily {
  id: string
  display_name: string
  maintainer: string
  license: License
  purposes: Purpose[]
  reviewed_at: string
  source: string
  notes?: string
  sizes: CatalogModel[]
}

/** Go: server.CatalogRefreshInfo. */
export interface CatalogRefreshInfo {
  started_at: string
  finished_at: string
  trigger: 'cli' | 'api' | 'watch' | string
  sizes: number
  resolved: number
}

/** GET /api/catalog (Go: server.CatalogResponse). */
export interface CatalogResponse {
  quants: string[]
  families: CatalogFamily[]
  last_refresh?: CatalogRefreshInfo
}

/** Go: refresh.SizeFailure. */
export interface CatalogSizeFailure {
  family_id: string
  ollama_tag: string
  hf_repo: string
  error: string
}

/** POST /api/catalog/refresh (Go: refresh.Report). */
/** Where a running model-list fetch is (Go: server.CatalogProgress). */
export interface CatalogProgress {
  /** "models": the list's descriptions; "public": the public scores. */
  phase: 'models' | 'public'
  /** What is being read now, in words. */
  message: string
  done: number
  total: number
  started_at: string
}

/** GET /api/catalog/status (Go: server.CatalogStatus). */
export interface CatalogStatus {
  /** At least one size of the list has been resolved: the list can recommend. */
  fetched: boolean
  last_refresh?: CatalogRefreshInfo
  running: boolean
  progress?: CatalogProgress
  /** A public benchmark source has been read at least once. */
  public_fetched: boolean
  /** The detail view's sentence about the public scores' last update. */
  public_updated: string
  /** The public scores are downloading in the background; public_progress says which source. */
  public_running: boolean
  public_progress?: CatalogProgress
}

export interface CatalogRefreshReport {
  started_at: string
  finished_at: string
  trigger: string
  sizes: number
  resolved: number
  files: number
  header_reads: number
  cache_hits: number
  requests: number
  not_modified: number
  bytes_read: number
  failures: CatalogSizeFailure[]
  stopped?: string
  warnings: string[]
  unknown_installed: { backend: string; name: string; note: string }[]
  /** The public-data part (step 9b); absent when the refresh did not read the sources. */
  external?: ExternalReport
}

/** Go: external.Report — what each public-data source did in a refresh (the curator's coverage report; the fields the UI reads). */
export interface ExternalReport {
  started_at: string
  finished_at: string
  sources: { id: string; name: string; status: 'read' | 'unchanged' | 'skipped' | 'failed' | 'disabled'; summary: string; error?: string }[]
  warnings: string[]
}

/** An installed model the catalogue does not know (Go: server.UnknownInstalledModel). */
export interface UnknownInstalledModel {
  backend_name: string
  name: string
  family?: string
  parameter_size?: string
  quantization?: string
  note: string
}

/** GET /api/catalog/unknown (Go: server.UnknownInstalledResponse). */
export interface UnknownInstalledResponse {
  models: UnknownInstalledModel[]
}

// --- Fit and speed (Go: internal/estimate) ---------------------------------
//
// Every number here is an estimate until a benchmark measures it, and says
// so: memory terms are Bytes, speeds are Rates — a RANGE (low < high) while
// estimated, a point once measured. Render them with <Figure> and nothing
// else. When there is no speed estimate the rates are absent and `unknown`
// is the sentence to show; never a number.

export type FitCategory =
  | 'fits_with_headroom'
  | 'fits'
  | 'needs_cpu_offload'
  | 'reduced_context_only'
  | 'not_recommended'
  | 'unknown'

export type KVCacheType = 'f16' | 'q8_0' | 'q4_0'

/** Go: estimate.Request. */
export interface EstimateRequest {
  catalog_file_id: number
  num_ctx: number
  kv_cache_type: KVCacheType
  runtime_path: RuntimePath
}

/** Go: estimate.Memory. */
export interface EstimateMemory {
  weights: Bytes
  kv_cache: Bytes
  overhead: Bytes
  total: Bytes
  gpu_resident: Bytes
  cpu_offload: Bytes
  effective_ctx: number
}

/** Go: estimate.Speed. */
export interface EstimateSpeed {
  known: boolean
  generation?: Rate
  prompt?: Rate
  /** Why there is no estimate, in words, when known is false. */
  unknown?: string
  basis?: string
  /** Built from a test of another model on this computer (calibrated_from names it); still an estimate. */
  calibrated?: boolean
  calibrated_from?: string
}

/** Go: estimate.Basis — which inputs were measured, estimated or unknown. */
export interface EstimateBasis {
  memory_model: 'validated' | 'modelled' | 'incomplete' | 'measured'
  path_source: 'established' | 'expected'
  budget_known: boolean
  speed_source: 'measured' | 'estimated' | 'unknown'
  /** The speed range was built from a test of another model on this computer. */
  speed_calibrated?: boolean
}

/** Go: estimate.Estimate. */
export interface Estimate {
  request: EstimateRequest
  memory: EstimateMemory
  speed: EstimateSpeed
  category: FitCategory
  /** The comparison that decided the category, in words. */
  threshold: string
  /** Read from the machine (less a configured reserve): plain text, not a Figure. */
  budget_bytes: number
  budget_known: boolean
  budget_kind: 'graphics_memory' | 'unified_memory' | 'system_memory'
  suggested_ctx?: number
  basis: EstimateBasis
  notes?: string[]
}

/** Go: estimate.UnusedGPU — a graphics card the numbers are without, and why. */
export interface UnusedGPU {
  name: string
  kind: 'not_used' | 'cannot_use' | 'not_known' | 'memory_unknown'
  why: string
}

// --- Recommendations (Go: internal/recommend) -------------------------------

export type Confidence = 'high' | 'medium' | 'low'

/** Go: recommend.Reason. */
export interface Reason {
  text: string
  kind: 'warning' | 'fit' | 'purpose' | 'speed' | 'size' | 'change'
  /** The explainer this reason links to ("gpu_not_used"), when it has one. */
  explainer?: string
  /** What that explainer says about this machine. */
  detail?: string
}

/** Go: recommend.Factors — internal ranking material, Advanced only. */
export interface ScoreFactors {
  purpose: number
  fit: number
  speed: number
  size: number
  /** The multiplier public quality signals applied inside `purpose` (1 = none). */
  public: number
}

/** Go: recommend.Recommendation — one card. */
export interface Recommendation {
  family_id: string
  display_name: string
  model: CatalogModel
  file: CatalogFile
  /** The exact name to give Ollama. */
  pull_name: string
  num_ctx: number
  estimate: Estimate
  /** A fact from the catalogue's listing; 0 when already installed. */
  download_bytes: number
  installed: boolean
  /** The headline speed; absent when there is no estimate. */
  speed?: Rate
  /** What that speed is good for, per purpose asked (backlog (j)). */
  verdicts?: SpeedVerdict[]
  reasons: Reason[]
  confidence: Confidence
  confidence_why: string
  score: number
  factors: ScoreFactors
  versus_current?: string
  /**
   * The card's one "Public data" line (step 9b): someone else's result for
   * this size, shown below and apart from the reasons. Absent when no public
   * value speaks to the purposes asked.
   */
  public?: PublicEntry
}

/** Go: recommend.Current. */
export interface CurrentModel {
  name: string
  in_catalogue: boolean
  estimate?: Estimate
  verdict: string
}

/** GET /api/recommend?purposes=… (Go: recommend.Result). */
export interface RecommendResult {
  purposes: Purpose[]
  /** At most three, best first. */
  recommendations: Recommendation[]
  warning?: string
  gpu_not_used?: UnusedGPU
  current?: CurrentModel
  /** Why the list is empty, when it is — in words, and as a code for the UI's logic. */
  empty?: string
  empty_code?: 'blocked' | 'budget_unknown' | 'catalogue_empty' | 'nothing_fits' | 'nothing_changes'
  runtime_path: RuntimePath
  path_source: 'established' | 'expected'
}

// --- Speed verdicts (Go: recommend.SpeedVerdict, backlog (j)) --------------
//
// What a speed on this computer is good for, per purpose. A verdict is
// derived from a local number and inherits its source (product rule 4):
// <SpeedVerdict> renders estimated and measured with the same two
// treatments <Figure> uses. The words come from the API.

export type SpeedGrade = 'excellent' | 'good' | 'usable' | 'too_slow'

/** Go: recommend.SpeedVerdict. */
export interface SpeedVerdict {
  purpose: Purpose
  /** False when there is nothing to grade with; text and note say so. */
  known: boolean
  /** The grade at the slower end of the range, and at the faster end (equal for a measurement). */
  low?: SpeedGrade
  high?: SpeedGrade
  /** Which bar decided the grade. */
  limit?: 'answer_speed' | 'wait'
  /** Seconds before the first word for this purpose's typical prompt. */
  wait?: Rate
  source: Source
  /** "excellent for everyday chat" */
  text: string
  /** "about 4 seconds to read a pasted file" — only when the wait holds the grade below excellent. */
  wait_text?: string
  /** What the verdict rests on when it is less than both bars. */
  note?: string
}

// Every number below is a curated configuration value (ARCHITECTURE.md
// D-58, data/recommend/speed-needs.yaml) — never a measurement of this
// machine and never a publisher's figure about a model — so none of them
// render through <Figure> or <PublicFigure>; the tokens_per_sec glossary
// explainer (components/Term.tsx) turns them into words instead.

/** The three grades every speed-needs bar states (Go: server.SpeedLevels). */
export interface SpeedLevels {
  excellent: number
  good: number
  usable: number
}

/** One purpose's row of the table (Go: server.SpeedNeedPurpose). */
export interface SpeedNeedPurpose {
  purpose: Purpose
  /** read_along: a person reads the answer as it streams. per_step (agentic): nobody reads along, so there is no stream bar. */
  mode: 'read_along' | 'per_step'
  /** Answer speed, tokens a second, that earns each grade — read_along only. */
  stream?: SpeedLevels
  /** Seconds before the first word (or, per_step, one whole step) that still earns each grade. */
  wait_s: SpeedLevels
}

/** GET /api/speed-needs (Go: server.SpeedNeedsResponse): what a speed is good for, per purpose. */
export interface SpeedNeedsResponse {
  /** Turns a tokens-a-second number into the words a second the rest of the UI shows. */
  words_per_token: number
  purposes: SpeedNeedPurpose[]
}

/** Go: server.FileFit. */
export interface FileFit {
  file: CatalogFile
  /** The file the size's Ollama tag pulls. */
  default: boolean
  estimate: Estimate
  /** What its speed is good for, per saved purpose (backlog (j)). */
  verdicts?: SpeedVerdict[]
}

/** GET /api/models/{id}/fit (Go: server.ModelFitResponse). */
export interface ModelFitResponse {
  family_id: string
  display_name: string
  model: CatalogModel
  num_ctx: number
  num_ctx_source: 'requested' | 'ollama_default'
  kv_cache_type: KVCacheType
  runtime_path: RuntimePath
  path_source: 'established' | 'expected'
  gpu_not_used?: UnusedGPU
  fits: FileFit[]
}

// --- Installed models (Go: internal/server/backend.go) ------------------------

/** One installed model (Go: server.InstalledModelInfo). Sizes are facts from the runtime: plain text, not a Figure. */
export interface InstalledModel {
  backend_name: string
  name: string
  digest?: string
  size_bytes: number
  quantization?: string
  family?: string
  parameter_size?: string
  modified_at?: string
  last_seen_at: string
  catalog_match: 'file' | 'model' | 'unknown' | ''
  catalog_model_id?: number
  catalog_file_id?: number
  catalog_note?: string
}

/** GET /api/models/installed (Go: server.InstalledModelsResponse). */
export interface InstalledModelsResponse {
  models: InstalledModel[]
}

/** One model the Benchmarks screen can offer (Go: server.BenchModel). */
export interface BenchModel {
  /** An installed model's name, or the Ollama tag a download fetches. */
  name: string
  /** "Qwen3.5 9B"; absent for an installed model the list does not know. */
  display_name?: string
  model_id?: number
  installed: boolean
  /** Not installed: what it costs to get, from the list's own listing (a fact). */
  download_bytes?: number
  /** Not installed: how it is expected to fit at Ollama's default context. */
  fit?: FitCategory
}

/** GET /api/bench/models (Go: server.BenchModelsResponse). */
export interface BenchModelsResponse {
  installed: BenchModel[]
  /** In the list, not installed, and would run here — smallest download first. */
  available: BenchModel[]
  catalogue_fetched: boolean
  /** Sizes in the list left out because they would not fit this computer. */
  too_big: number
}

// --- Benchmarks (Go: internal/bench) ----------------------------------------------
//
// Everything a benchmark produces is MEASURED: a point, rendered with
// <Figure>. The two exceptions are estimates and say so: a plan's duration
// and the time left, and the estimate a run is compared with.

export type BenchStatus = 'queued' | 'running' | 'done' | 'cancelled' | 'failed'
export type BenchPhase = 'preparing' | 'loading' | 'measuring' | 'unloading' | 'finished'
export type Resident = 'gpu' | 'split' | 'cpu' | 'unknown'

/** POST /api/bench, GET /api/bench/plan (Go: bench.Request). */
export interface BenchRequest {
  model: string
  /** 0 or absent: what Ollama itself uses on this computer. */
  num_ctx?: number
  prompts?: string[]
  measure_anyway?: boolean
}

/** What a run was made with — two runs compare only when all of it is equal (Go: bench.RunConfig). */
export interface BenchConfig {
  hardware_profile_id: number
  hardware_fingerprint: string
  backend: string
  backend_version: string
  runtime_path: RuntimePath
  runtime_path_evidence?: string
  model: string
  model_digest: string
  quantization: string
  weights_bytes: number
  catalog_file_id: number
  /** The catalogue size this file belongs to; 0 when the catalogue does not know this model. */
  catalog_model_id: number
  num_ctx: number
  effective_ctx: number
  /** "f16", "q8_0", … or "unknown" when the runtime's output could not be read. */
  kv_cache_type: string
  flash_attention: boolean
  flash_attention_known: boolean
  parallel: number
  suite_version: string
  suite_digest: string
  completion_tokens: number
  repeats: number
  daemon_version: string
}

/** One timed request, the runtime's own counters (Go: bench.Timing). */
export interface BenchTiming {
  prompt_tokens: number
  cached_tokens: number
  cached_known: boolean
  prompt_ms: number
  gen_tokens: number
  gen_ms: number
  ttft_ms: number
  load_ms: number
  done_reason?: string
}

/** Medians and spread of one prompt's timed requests (Go: bench.PromptResult). */
export interface PromptResult {
  prompt: string
  prompt_tokens: number
  gen_tokens: number
  prompt_tps?: Rate
  /** Absent when every answer stopped before the harness's minimum; generation_unknown says why. */
  generation_tps?: Rate
  generation_unknown?: string
  ttft?: Rate
  spread_pct: number
  prompt_spread_pct: number
  runs: number
  timings: BenchTiming[]
  notes?: string[]
}

export interface BenchSkipped {
  prompt: string
  why: string
}

/** The previous run of the same configuration (Go: bench.Comparison). */
export interface BenchComparison {
  run_id: number
  generation_tps: Rate
  diff_pct: number
}

/** One resource reading (Go: bench.Sample). Raw readings: the figures to show are the summaries on the run. */
export interface BenchSample {
  at: string
  tool: string
  device?: string
  gpu_util_pct?: number
  vram_used_bytes?: number
  ram_used_bytes?: number
  temp_c?: number
  power_w?: number
}

/** GET /api/bench/{id}, and every progress event's run (Go: bench.Run). */
export interface BenchRun {
  id: number
  status: BenchStatus
  phase: BenchPhase
  request: BenchRequest
  config: BenchConfig
  started_at: string
  finished_at?: string
  results: PromptResult[]
  skipped?: BenchSkipped[]
  headline?: string
  generation_tps?: Rate
  prompt_tps?: Rate
  ttft?: Rate
  load?: Rate
  resident: Resident
  runtime_size_bytes?: number
  runtime_size_vram_bytes?: number
  peak_vram?: Bytes
  memory_source?: string
  peak_ram?: Bytes
  gpu_util?: Rate
  peak_temp?: Rate
  power?: Rate
  sampler_note?: string
  estimate?: Estimate
  expected_duration?: Rate
  replaced: boolean
  unloaded?: boolean
  comparison?: BenchComparison
  /** What the measured speeds are good for, per saved purpose (backlog (j)). */
  verdicts?: SpeedVerdict[]
  notes?: string[]
  error?: string
  samples?: BenchSample[]
}

/** One event of GET /api/bench/{id} as a stream (Go: bench.Progress). */
export interface BenchProgress {
  run_id: number
  status: BenchStatus
  phase: BenchPhase
  message: string
  step: number
  steps: number
  elapsed_seconds: number
  remaining?: Rate
  last?: BenchSample
  run: BenchRun
}

export interface PlannedPrompt {
  id: string
  tokens: number
  runs: number
  skip?: string
}

export interface BenchSuiteInfo {
  version: string
  digest: string
  completion_tokens: number
  warmups: number
  repeats: number
  temperature: number
  seed: number
}

/** GET /api/bench/plan (Go: bench.Plan). */
export interface BenchPlan {
  model: string
  model_source: 'catalogue' | 'runtime'
  num_ctx: number
  num_ctx_source: 'requested' | 'ollama_default'
  prompts: PlannedPrompt[]
  requests: number
  estimate: Estimate
  duration?: Rate
  duration_unknown?: string
  refusal?: string
  refusal_code?: 'would_spill' | 'not_recommended' | 'nothing_fits'
  suggested_ctx?: number
  suite: BenchSuiteInfo
  notes?: string[]
  /** The latest finished run of this configuration on this computer: shown in the estimate's place (rule 4). */
  measured?: BenchPlanMeasured
}

/** Go: bench.PlanMeasured. */
export interface BenchPlanMeasured {
  run_id: number
  at: string
  generation_tps: Rate
}

/** GET /api/bench/history (Go: bench.History). */
export interface BenchHistory {
  runs: BenchRun[]
}

// --- Backends (Go: internal/backend, internal/server/backend.go) ----------
//
// A runtime's own status, read straight from the process or the OS: not
// estimated, not measured — plain text, not a Figure.

export type BackendState = 'not_installed' | 'installed_not_running' | 'running' | 'unsupported'

/** Go: server.BackendInfo. */
export interface BackendInfo {
  name: string
  state: BackendState
  version?: string
  host?: string
  installed_version?: string
  runtime_paths?: Record<string, RuntimePath>
  env?: Record<string, string>
  detail?: string
  checked_at: string
}

/** GET /api/backends (Go: server.BackendsResponse). */
export interface BackendsResponse {
  backends: BackendInfo[]
}

// --- Onboarding, install, pull, chat apps (Go: internal/server, step 7) ---
//
// The first-run flow's own API. Byte counts here are read live from a
// download's own counter (backend.InstallProgress / PullProgress) — facts,
// tagged `source:"n/a"` on the Go side, shown as plain text and never
// through <Figure>.

/** GET /api/onboarding, POST /api/onboarding/complete (Go: server.OnboardingStatus). */
export interface OnboardingStatus {
  completed: boolean
  completed_at?: string
}

/** GET /api/backends/{name}/install-size (Go: server.InstallSizeResponse). */
export interface InstallSizeResponse {
  bytes: number
  known: boolean
}

export type InstallState = 'idle' | 'running' | 'done' | 'failed'

/** GET and POST /api/backends/{name}/install (Go: server.InstallStatus). */
export interface InstallStatus {
  backend: string
  status: InstallState
  message?: string
  completed_bytes: number
  total_bytes?: number
  error?: string
}

/** POST /api/backends/{name}/start (Go: server.BackendStartResponse). */
export interface BackendStartResponse {
  backend: string
  status: 'starting'
}

/** POST /api/models/pull (Go: server.PullRequest). */
export interface PullRequest {
  ollama_tag: string
}

export type PullState = 'idle' | 'running' | 'done' | 'failed' | 'cancelled'

/** GET and POST /api/models/pull (Go: server.PullStatus). */
export interface PullStatus {
  model?: string
  status: PullState
  message?: string
  completed_bytes: number
  total_bytes?: number
  error?: string
}

/** One chat app the advisor found on this machine — or didn't (Go: chatapps.App). The advisor never installs or drives one (D-4). */
export interface ChatApp {
  id: string
  name: string
  found: boolean
  path?: string
  note?: string
  download_url: string
}

/** GET /api/chatapps (Go: server.ChatAppsResponse). */
export interface ChatAppsResponse {
  apps: ChatApp[]
}

// --- New-model watch (build-plan step 10; Go: internal/watch, internal/server/watch.go) -----
//
// A daily, jittered scheduler refreshes the catalogue and checks every
// curated size against this machine; a candidate that fits and beats the
// current model on a purpose picked earns exactly one desktop notification,
// ever. What it checked, found and suppressed (and why) is this log.

/** What the watch knows about one candidate (Go: watch.Outcome). */
export type WatchOutcome = 'notified' | 'suppressed' | 'already_seen' | 'flagged_for_curator' | 'error'

/** Go: watch.NotifyMode — governs the desktop popup only; the check and the log run the same in every mode except "off" (WatchSettings.enabled). */
export type NotifyMode = 'on' | 'quiet' | 'never'

/** The watch's own settings, read from and written through /api/settings (Go: watch.Settings). */
export interface WatchSettings {
  /** The master switch: off runs no refresh, no check, no log line at all. */
  enabled: boolean
  mode: NotifyMode
  /** Nanoseconds; 0 uses the daemon's own default. Not set from the UI today. */
  interval: number
}

/** One line of the watch log (Go: watch.LogEntry). */
export interface WatchLogEntry {
  at: string
  key: string
  /** The model's display name, or the repo id — never the raw key. */
  name: string
  outcome: WatchOutcome
  detail: string
}

/** One watch run's full report (Go: watch.Report). */
export interface WatchReport {
  started_at: string
  finished_at: string
  trigger: 'scheduler' | 'api' | 'cli' | string
  checked: number
  notified: number
  suppressed: number
  already_seen: number
  flagged: number
  errors: number
  refresh_error?: string
  maintainer_error?: string
  entries: WatchLogEntry[]
}

/** One watch_runs row (Go: server.WatchRunSummary). */
export interface WatchRunSummary {
  id: number
  created_at: string
  finished_at: string
  trigger: string
  checked: number
  notified: number
  suppressed: number
  flagged: number
  report: WatchReport
}

/** GET /api/watch/log (Go: server.WatchLogResponse). */
export interface WatchLogResponse {
  /** Newest first. */
  runs: WatchRunSummary[]
}

// --- Settings (Go: internal/server/settings.go) -----------------------------
//
// Advanced (product rule 2's toggle), the purposes picked on Recommend, and
// the new-model watch's own settings: the durable, machine-wide settings.
// state/settings.tsx keeps a localStorage copy for a snappy first paint and
// reconciles it with this endpoint; this table is what survives a cleared
// browser profile or a second window.

/** GET and PUT /api/settings (Go: server.SettingsResponse). DataDir is a fact from the OS, not a Figure. */
export interface SettingsResponse {
  advanced: boolean
  data_dir: string
  /** The purposes chosen on Recommend, persisted so the watch has something to check against. Empty until chosen once. */
  purposes: Purpose[]
  watch: WatchSettings
}

/** PUT /api/settings (Go: server.SettingsUpdate). purposes and watch are optional: left out, the stored value is unchanged. */
export interface SettingsUpdate {
  advanced: boolean
  purposes?: Purpose[]
  watch?: WatchSettings
}

/** POST /api/backends/{name}/models/remove (Go: server.ModelRemoveRequest). The response is InstalledModelsResponse: the inventory as it now stands. */
export interface ModelRemoveRequest {
  name: string
}

// --- Public data (step 9b; Go: figure.Public, catalog.PublicEntry) -----------
//
// The third kind of number (research/EXTERNAL_SOURCES.md, the display rule):
// a value about a MODEL that someone else published — never a number about
// this machine. It has no `source` field, so <Figure>, whose props require
// one, cannot render it; components/PublicFigure.tsx does, and nothing else.
// Public and local numbers never share a block, a table, a column or a
// sentence: the API sends them as sibling objects ("public", "local").

/** Who produced a public value (Go: figure.Provenance). */
export type Provenance = 'maker' | 'verified' | 'independent' | 'crowd'

/** Where a public value came from, shown directly beneath it (Go: figure.Origin). */
export interface PublicOrigin {
  /** Who published it, in words. */
  publisher: string
  url?: string
  /** The source's own date (YYYY-MM-DD) — never the day the advisor fetched it. */
  date: string
  licence: string
  /** The credit the licence asks for, shown as it is. */
  attribution: string
  provenance: Provenance
}

/** A number someone else published about a model (Go: figure.Public). */
export interface PublicValue {
  value: number
  /** What `value` is measured in: 'rating' (relative) or 'percent'. */
  scale: string
  origin: PublicOrigin
}

/** One labelled line of a public value's Advanced detail, already in words. */
export interface PublicDetail {
  label: string
  text: string
}

/** One public value as a size's "Public data" block shows it (Go: catalog.PublicEntry). */
export interface PublicEntry {
  source_id: 'hf_evals' | 'arena' | 'epoch' | string
  source_name: string
  metric: string
  /** What it tested, in plain words. */
  tests: string
  purposes: Purpose[]
  /** The size's place among the curated sizes this source has scored, in words. */
  position: string
  /** "rated by people comparing answers on Arena", "reported by the model's maker". */
  provenance_words: string
  rated: number
  rank: number
  /** Whether the recommendation engine may use it (a maker's own report is shown, not scored). */
  scored: boolean
  value: PublicValue
  /** Advanced only: the raw value, its scale, bounds, votes, notes, ids. */
  detail: PublicDetail[]
  fetched_at: string
}

/** A size's "Public data" block (Go: server.PublicBlock). */
export interface PublicBlock {
  /** Empty: no approved source has scored this size (never a sibling's score). */
  entries: PublicEntry[]
  /** When public scores were last fetched, and any source whose latest check failed, in words. */
  updated: string
}

/** GET /api/models/{id}/detail (Go: server.ModelDetailResponse): the two blocks, apart. */
export interface ModelDetailResponse {
  id: number
  family_id: string
  /** "Qwen3.5 9B" */
  name: string
  maintainer: string
  purposes: Purpose[]
  released_at?: string
  pull_name: string
  public: PublicBlock
  local: ModelFitResponse
}
