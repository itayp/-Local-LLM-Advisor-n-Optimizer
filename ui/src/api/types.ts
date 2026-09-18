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
