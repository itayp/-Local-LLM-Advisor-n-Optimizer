-- Step 6 (benchmark harness).
--
-- Schema v0 created benchmark_runs and benchmark_samples with the columns
-- step 1 could foresee. The harness needs the rest of a run's context to
-- keep it comparable (ARCHITECTURE.md D-44..D-49):
--
--   hardware_fingerprint  the hardware the run measured, beside the profile
--                         id: profile ids change on every daemon start,
--                         the fingerprint only when the hardware does
--                         (hardware.Fingerprint, as in hardware_profiles).
--   model_digest          the runtime's digest of the model. A name can be
--                         pulled again and point at a different file.
--   kv_cache_type         (v0) may now also be 'unknown': the runtime's own
--                         output could not be read, so which cache it
--                         allocated is not known (D-21).
--   flash_attention_known 0 when the runtime's output did not say whether
--                         flash attention was on; flash_attention is then 0
--                         and means nothing.
--   config_key            a digest of every field that makes two runs
--                         comparable (bench.Config.Key); equal keys, and
--                         only equal keys, are compared.
--   config_json           the whole bench.Config, beside the plain columns.
--   model_json            what the harness knew about the model it measured
--                         (header fields, sizes), so a measurement can
--                         calibrate other estimates later (D-48).
--   estimate_json         the estimate this run was compared with before it
--                         started: "estimated X, measured Y".
--   notes_json            what the run noticed, in words.
--   resident              where the runtime put the model: gpu | split | cpu
--                         | unknown (from /api/ps after the load).
--   ps_size_bytes,        the runtime's own accounting after the load — its
--   ps_size_vram_bytes    estimate, not a measurement (D-20 finding 4),
--                         kept as context and as the evidence for where
--                         "fits" ends.
--   effective_ctx         the context the runtime actually ran (it clamps
--                         num_ctx to the model's trained context).
--   memory_source         which counter peak_vram_bytes comes from.
--   unloaded              1 when, after the run, the model was confirmed
--                         gone from the runtime's list of loaded models; 0
--                         when it was still there; NULL when not checked.
--   measure_anyway        the run was asked for over the estimator's refusal.
--
-- peak_vram_bytes (v0) is the peak rise in the graphics memory counter over
-- its reading before the load: what the model took. peak_ram_bytes (v0) is
-- the peak of system memory in use, absolute.
ALTER TABLE benchmark_runs ADD COLUMN hardware_fingerprint  TEXT    NOT NULL DEFAULT '';
ALTER TABLE benchmark_runs ADD COLUMN model_digest          TEXT    NOT NULL DEFAULT '';
ALTER TABLE benchmark_runs ADD COLUMN flash_attention_known INTEGER NOT NULL DEFAULT 0;
ALTER TABLE benchmark_runs ADD COLUMN config_key            TEXT    NOT NULL DEFAULT '';
ALTER TABLE benchmark_runs ADD COLUMN config_json           TEXT    NOT NULL DEFAULT '{}';
ALTER TABLE benchmark_runs ADD COLUMN model_json            TEXT    NOT NULL DEFAULT '{}';
ALTER TABLE benchmark_runs ADD COLUMN estimate_json         TEXT    NOT NULL DEFAULT '';
ALTER TABLE benchmark_runs ADD COLUMN notes_json            TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE benchmark_runs ADD COLUMN resident              TEXT    NOT NULL DEFAULT 'unknown';
ALTER TABLE benchmark_runs ADD COLUMN ps_size_bytes         INTEGER;
ALTER TABLE benchmark_runs ADD COLUMN ps_size_vram_bytes    INTEGER;
ALTER TABLE benchmark_runs ADD COLUMN effective_ctx         INTEGER;
ALTER TABLE benchmark_runs ADD COLUMN memory_source         TEXT    NOT NULL DEFAULT '';
ALTER TABLE benchmark_runs ADD COLUMN unloaded              INTEGER;
ALTER TABLE benchmark_runs ADD COLUMN measure_anyway        INTEGER NOT NULL DEFAULT 0;

CREATE INDEX benchmark_runs_config ON benchmark_runs (config_key, id);
CREATE INDEX benchmark_runs_hardware ON benchmark_runs (hardware_fingerprint, backend_name, id);

-- Which device a sample is of: the GPU index nvidia-smi reports, the DRM
-- card for AMD's sysfs counters, '' for the whole system (system memory,
-- macOS's wired memory, the runtime's /api/ps).
ALTER TABLE benchmark_samples ADD COLUMN device TEXT NOT NULL DEFAULT '';
