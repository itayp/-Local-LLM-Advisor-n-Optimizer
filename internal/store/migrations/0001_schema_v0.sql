-- Schema v0 — Local LLM Advisor & Optimizer.
--
-- Conventions (see CLAUDE.md):
--   * every table has an INTEGER PRIMARY KEY `id` and a `created_at` in
--     RFC 3339 UTC ("2026-09-18T12:00:00Z"), except settings (keyed by name);
--   * booleans are INTEGER 0/1; byte counts are INTEGER; timestamps are TEXT;
--   * a `*_json` column holds the full Go struct as JSON where the struct is
--     expected to grow; the plain columns beside it are the queryable subset.
--     A later step that needs to query a JSON field promotes it to a column
--     in a new migration;
--   * `source` columns take exactly 'estimated' or 'measured' (CHECK);
--   * public data (catalog_external) and local measurements (benchmark_*)
--     never share a table or a column (PRD §10).
--
-- Migrations are applied in order and recorded in schema_migrations. This
-- file is never edited after it ships: a change is a new file.

-- The machine, as detected on every daemon start. History is kept: a user
-- who changes a GPU must keep their old benchmarks attributable to the old one.
CREATE TABLE hardware_profiles (
    id               INTEGER PRIMARY KEY,
    created_at       TEXT    NOT NULL,
    os               TEXT    NOT NULL,           -- linux | darwin | windows
    os_version       TEXT    NOT NULL,           -- "unknown" when unread
    arch             TEXT    NOT NULL,
    hostname         TEXT    NOT NULL,
    cpu_model        TEXT    NOT NULL,
    cpu_cores        INTEGER NOT NULL DEFAULT 0, -- logical; 0 = unknown
    cpu_avx2         INTEGER,                    -- NULL = unknown
    cpu_avx512       INTEGER,                    -- NULL = unknown
    ram_bytes        INTEGER,                    -- NULL = unknown
    unified_memory   INTEGER NOT NULL DEFAULT 0,
    gpu_usable_bytes INTEGER,                    -- NULL = unknown; largest single device, or the unified-memory budget
    is_laptop        INTEGER,                    -- NULL = unknown
    tier             TEXT    NOT NULL,           -- hardware.Tier
    summary          TEXT    NOT NULL,           -- one plain-language sentence
    profile_json     TEXT    NOT NULL,           -- the full hardware.Profile, GPUs included
    fingerprint      TEXT    NOT NULL            -- stable hash of the identifying fields, to tell "same machine" from "changed hardware"
);
CREATE INDEX hardware_profiles_fingerprint ON hardware_profiles (fingerprint, created_at);

-- Runtimes (Ollama; later llama.cpp, LM Studio). One row per detection.
CREATE TABLE backends (
    id                INTEGER PRIMARY KEY,
    created_at        TEXT    NOT NULL,
    name              TEXT    NOT NULL,          -- backend.Backend.Name()
    state             TEXT    NOT NULL,          -- backend.State
    version           TEXT    NOT NULL DEFAULT '',
    host              TEXT    NOT NULL DEFAULT '',
    installed_version TEXT    NOT NULL DEFAULT '', -- what the app installed, when it did
    runtime_paths_json TEXT   NOT NULL DEFAULT '{}', -- per GPU index: cuda | metal | rocm | vulkan | cpu — what actually happened
    env_json          TEXT    NOT NULL DEFAULT '{}', -- OLLAMA_VULKAN, HSA_OVERRIDE_GFX_VERSION, ... captured, never set
    detail            TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX backends_name ON backends (name, created_at);

-- The curated catalogue, one row per family size (data/catalog/families.yaml).
CREATE TABLE catalog_models (
    id             INTEGER PRIMARY KEY,
    created_at     TEXT    NOT NULL,
    family_id      TEXT    NOT NULL,
    display_name   TEXT    NOT NULL,
    maintainer     TEXT    NOT NULL DEFAULT '',
    license_json   TEXT    NOT NULL DEFAULT '{}',
    purposes_json  TEXT    NOT NULL DEFAULT '[]', -- catalog.Purpose[]
    parameters     INTEGER NOT NULL,              -- parameter count
    context_length INTEGER NOT NULL,              -- trained context
    ollama_tag     TEXT    NOT NULL DEFAULT '',
    hf_repo        TEXT    NOT NULL DEFAULT '',
    reviewed_at    TEXT    NOT NULL DEFAULT '',   -- YYYY-MM-DD from the YAML
    UNIQUE (family_id, parameters)
);

-- One GGUF quant variant per row; header read by range request, never downloaded.
CREATE TABLE catalog_files (
    id               INTEGER PRIMARY KEY,
    created_at       TEXT    NOT NULL,
    catalog_model_id INTEGER NOT NULL REFERENCES catalog_models (id),
    filename         TEXT    NOT NULL,
    quant            TEXT    NOT NULL,
    sha              TEXT    NOT NULL DEFAULT '',
    bytes            INTEGER NOT NULL,            -- summed across parts
    bits_per_weight  REAL    NOT NULL DEFAULT 0,
    architecture     TEXT    NOT NULL DEFAULT '',
    block_count      INTEGER NOT NULL DEFAULT 0,
    head_count       INTEGER NOT NULL DEFAULT 0,
    head_count_kv    INTEGER NOT NULL DEFAULT 0,
    key_length       INTEGER NOT NULL DEFAULT 0,  -- attention.key_length; 0 when not stated. Step 0: this is the real head_dim.
    embedding_length INTEGER NOT NULL DEFAULT 0,
    context_length   INTEGER NOT NULL DEFAULT 0,
    sliding_window   INTEGER NOT NULL DEFAULT 0,
    file_type        INTEGER NOT NULL DEFAULT 0,
    expert_count     INTEGER NOT NULL DEFAULT 0,  -- > 0: MoE
    has_vision       INTEGER NOT NULL DEFAULT 0,
    header_json      TEXT    NOT NULL DEFAULT '{}', -- every KV pair the parser kept
    fetched_at       TEXT    NOT NULL,
    UNIQUE (catalog_model_id, filename)
);

-- Public benchmark / quality signals. Never in the same columns as a local measurement.
CREATE TABLE catalog_external (
    id               INTEGER PRIMARY KEY,
    created_at       TEXT    NOT NULL,
    source           TEXT    NOT NULL,            -- step 9a's approved list
    source_model_id  TEXT    NOT NULL,
    catalog_model_id INTEGER REFERENCES catalog_models (id), -- NULL when the alias did not resolve (flagged)
    metric           TEXT    NOT NULL,
    value            REAL,
    value_text       TEXT    NOT NULL DEFAULT '',
    fetched_at       TEXT    NOT NULL,
    license          TEXT    NOT NULL DEFAULT '',
    UNIQUE (source, source_model_id, metric)
);

-- What the runtime has on disk, refreshed on every daemon start and on demand.
CREATE TABLE installed_models (
    id              INTEGER PRIMARY KEY,
    created_at      TEXT    NOT NULL,
    backend_name    TEXT    NOT NULL,
    name            TEXT    NOT NULL,             -- "llama3.1:8b"
    digest          TEXT    NOT NULL DEFAULT '',
    size_bytes      INTEGER NOT NULL,             -- the blob, from the runtime
    quantization    TEXT    NOT NULL DEFAULT '',
    family          TEXT    NOT NULL DEFAULT '',
    parameter_size  TEXT    NOT NULL DEFAULT '',
    modified_at     TEXT    NOT NULL DEFAULT '',
    catalog_file_id INTEGER REFERENCES catalog_files (id), -- NULL: the catalogue does not know this model (a curator signal, not an error)
    last_seen_at    TEXT    NOT NULL,
    present         INTEGER NOT NULL DEFAULT 1,   -- 0 once the runtime stops listing it; the row stays for history
    UNIQUE (backend_name, name)
);

-- One local benchmark run with its whole context (PRD §21, first risk).
CREATE TABLE benchmark_runs (
    id                  INTEGER PRIMARY KEY,
    created_at          TEXT    NOT NULL,
    status              TEXT    NOT NULL,         -- bench.Status
    hardware_profile_id INTEGER NOT NULL REFERENCES hardware_profiles (id),
    backend_name        TEXT    NOT NULL,
    backend_version     TEXT    NOT NULL DEFAULT '',
    runtime_path        TEXT    NOT NULL DEFAULT 'unknown',
    model_name          TEXT    NOT NULL,
    installed_model_id  INTEGER REFERENCES installed_models (id),
    catalog_file_id     INTEGER REFERENCES catalog_files (id),
    quantization        TEXT    NOT NULL DEFAULT '',
    weights_bytes       INTEGER NOT NULL DEFAULT 0,
    num_ctx             INTEGER NOT NULL,
    kv_cache_type       TEXT    NOT NULL DEFAULT 'f16',
    flash_attention     INTEGER NOT NULL DEFAULT 0,
    suite_version       TEXT    NOT NULL DEFAULT '',
    daemon_version      TEXT    NOT NULL DEFAULT '',
    started_at          TEXT    NOT NULL,
    finished_at         TEXT,
    results_json        TEXT    NOT NULL DEFAULT '[]', -- bench.PromptResult[] (median + spread per prompt)
    gen_tps_median      REAL,                     -- headline, duplicated from results_json for queries
    prompt_tps_median   REAL,
    ttft_ms_median      REAL,
    load_ms             REAL,
    peak_vram_bytes     INTEGER,                  -- NULL: no sampler
    peak_ram_bytes      INTEGER,
    sampler_note        TEXT    NOT NULL DEFAULT '',
    error               TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX benchmark_runs_model ON benchmark_runs (model_name, num_ctx, created_at);

-- 1 Hz resource samples during a run, from whichever tool existed.
CREATE TABLE benchmark_samples (
    id              INTEGER PRIMARY KEY,
    run_id          INTEGER NOT NULL REFERENCES benchmark_runs (id) ON DELETE CASCADE,
    sampled_at      TEXT    NOT NULL,
    tool            TEXT    NOT NULL,             -- nvidia-smi | rocm-smi | amd-smi | sysfs | api/ps | os
    gpu_util_pct    REAL,
    vram_used_bytes INTEGER,
    ram_used_bytes  INTEGER,
    temp_c          REAL,
    power_w         REAL
);
CREATE INDEX benchmark_samples_run ON benchmark_samples (run_id, sampled_at);

-- The estimator's answer per (profile, file, ctx, kv cache, runtime path).
-- When a benchmark of the same configuration completes, the row is updated
-- in place: the measured values replace the estimated ones and `source`
-- flips to 'measured' (product rule 4).
CREATE TABLE estimates (
    id                  INTEGER PRIMARY KEY,
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL,
    hardware_profile_id INTEGER NOT NULL REFERENCES hardware_profiles (id),
    catalog_file_id     INTEGER NOT NULL REFERENCES catalog_files (id),
    num_ctx             INTEGER NOT NULL,
    effective_ctx       INTEGER NOT NULL,
    kv_cache_type       TEXT    NOT NULL DEFAULT 'f16',
    runtime_path        TEXT    NOT NULL,         -- cuda | metal | rocm | vulkan | cpu | unknown
    weights_bytes       INTEGER NOT NULL,
    kv_bytes            INTEGER NOT NULL,
    overhead_bytes      INTEGER NOT NULL,
    total_bytes         INTEGER NOT NULL,
    gpu_resident_bytes  INTEGER NOT NULL DEFAULT 0,
    cpu_offload_bytes   INTEGER NOT NULL DEFAULT 0,
    budget_bytes        INTEGER NOT NULL DEFAULT 0,
    category            TEXT    NOT NULL,         -- estimate.Category
    threshold           TEXT    NOT NULL DEFAULT '',
    speed_known         INTEGER NOT NULL DEFAULT 0,
    gen_tps_low         REAL,                     -- the range; equal when measured
    gen_tps_high        REAL,
    prompt_tps_low      REAL,
    prompt_tps_high     REAL,
    source              TEXT    NOT NULL CHECK (source IN ('estimated', 'measured')),
    measured_run_id     INTEGER REFERENCES benchmark_runs (id), -- set when source = 'measured'
    notes_json          TEXT    NOT NULL DEFAULT '[]',
    UNIQUE (hardware_profile_id, catalog_file_id, num_ctx, kv_cache_type, runtime_path)
);

-- The new-model watch: what has been checked, notified, suppressed, and why.
CREATE TABLE watch_state (
    id              INTEGER PRIMARY KEY,
    created_at      TEXT    NOT NULL,
    key             TEXT    NOT NULL UNIQUE,      -- catalogue file id, or a repo for curator flags
    last_checked_at TEXT    NOT NULL,
    notified_at     TEXT,                         -- once per model, ever
    outcome         TEXT    NOT NULL,             -- watch.Outcome
    reason          TEXT    NOT NULL DEFAULT ''
);

-- Key/value settings; value is JSON. Keys are documented where they are read.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
