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
  fetched_at: string
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
