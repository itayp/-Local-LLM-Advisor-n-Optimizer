-- Step 9b: public benchmark data from the sources step 9a approved
-- (research/EXTERNAL_SOURCES.md, ARCHITECTURE.md D-53).
--
-- catalog_external (schema v0) holds someone else's numbers about a model;
-- benchmark_runs / estimates hold this machine's. They never share a table
-- or a column (D-10). A row is never deleted: a value that leaves its source
-- gets present = 0 and stops being shown or scored (the D-30 / D-35
-- pattern). Only values that map to a catalogue size are stored, so
-- catalog_model_id is set on every row this step writes; what did not map
-- is the coverage report's, not the table's.

ALTER TABLE catalog_external ADD COLUMN source_date TEXT NOT NULL DEFAULT '';   -- the source's own date (YYYY-MM-DD): the evaluation's or the leaderboard's, never the fetch
ALTER TABLE catalog_external ADD COLUMN source_url TEXT NOT NULL DEFAULT '';    -- where a person can read the value
ALTER TABLE catalog_external ADD COLUMN provenance TEXT NOT NULL DEFAULT 'maker'
    CHECK (provenance IN ('maker', 'verified', 'independent', 'crowd'));      -- who produced it; only the last three are scored
ALTER TABLE catalog_external ADD COLUMN attribution TEXT NOT NULL DEFAULT '';   -- the credit its licence asks for, as shown
ALTER TABLE catalog_external ADD COLUMN detail_json TEXT NOT NULL DEFAULT '{}'; -- bounds, votes, rank, board size, notes (Advanced)
ALTER TABLE catalog_external ADD COLUMN present INTEGER NOT NULL DEFAULT 1;     -- 0 once the value has left its source
ALTER TABLE catalog_external ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
-- catalog_external.license: e.g. "CC-BY-4.0", or "HF-ToS; repo:Apache-2.0".
-- catalog_external.fetched_at: when the advisor last read the value.

CREATE INDEX catalog_external_model ON catalog_external (catalog_model_id, present);

ALTER TABLE catalog_models ADD COLUMN hf_base_repo TEXT NOT NULL DEFAULT '';    -- the original model's repo (families.yaml)
ALTER TABLE catalog_models ADD COLUMN ollama_quant TEXT NOT NULL DEFAULT '';    -- the quant ollama_tag pulls, read by hand; '' = the usual default
ALTER TABLE catalog_models ADD COLUMN released_at TEXT;                         -- the original repo's creation date (YYYY-MM-DD); NULL = not read

-- One row per (source, key): a source's run state (key '') and each
-- request's validators (key = the URL), so an unchanged answer is a 304 and
-- a source is read at most once per its cadence. config_digest is the
-- digest of what the source's rows depend on besides the answer (the
-- aliases, the metric map, the catalogue's repos): when it changes, the next
-- read is unconditional even if the answer would be a 304.
CREATE TABLE external_state (
    id            INTEGER PRIMARY KEY,
    created_at    TEXT NOT NULL,
    source        TEXT NOT NULL,
    key           TEXT NOT NULL DEFAULT '',
    etag          TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT '',
    config_digest TEXT NOT NULL DEFAULT '',
    attempted_at  TEXT NOT NULL DEFAULT '',     -- last try, successful or not
    ok_at         TEXT NOT NULL DEFAULT '',     -- last success; '' = never
    error         TEXT NOT NULL DEFAULT '',     -- why the last try failed, in words; '' = it did not
    UNIQUE (source, key)
);
