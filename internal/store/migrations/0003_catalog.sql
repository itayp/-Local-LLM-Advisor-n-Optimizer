-- Step 4: the catalogue, resolved against Hugging Face.
--
-- catalog_models gains what families.yaml says beyond schema v0 and what
-- the last refresh learned; catalog_files gains the header fields step 5
-- reads that v0 did not foresee, the file's role (weights or a vision
-- encoder) and how many parts it is split into. Rows are never deleted: a
-- size that leaves families.yaml, or a file that leaves its repo, is marked
-- present = 0, because estimates and benchmarks point at them.

ALTER TABLE catalog_models ADD COLUMN active_parameters INTEGER NOT NULL DEFAULT 0; -- MoE / per-layer embeddings; 0 = dense
ALTER TABLE catalog_models ADD COLUMN source_url TEXT NOT NULL DEFAULT '';         -- where the curator checked the entry
ALTER TABLE catalog_models ADD COLUMN notes TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_models ADD COLUMN present INTEGER NOT NULL DEFAULT 1;           -- 0 once the size has left families.yaml
ALTER TABLE catalog_models ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE catalog_models ADD COLUMN hf_sha TEXT NOT NULL DEFAULT '';             -- repo commit the last refresh read
ALTER TABLE catalog_models ADD COLUMN parameters_counted INTEGER;                   -- Hugging Face's count from the tensor shapes; NULL = not given
ALTER TABLE catalog_models ADD COLUMN refreshed_at TEXT;                            -- NULL = never resolved
ALTER TABLE catalog_models ADD COLUMN refresh_error TEXT NOT NULL DEFAULT '';      -- why the last refresh of this size failed, in words

ALTER TABLE catalog_files ADD COLUMN role TEXT NOT NULL DEFAULT 'model' CHECK (role IN ('model', 'projector'));
ALTER TABLE catalog_files ADD COLUMN parts INTEGER NOT NULL DEFAULT 1;
ALTER TABLE catalog_files ADD COLUMN gguf_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_files ADD COLUMN tensor_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_files ADD COLUMN head_count_kv_stated INTEGER NOT NULL DEFAULT 1; -- 0: the file leaves it out and llama.cpp uses head_count
ALTER TABLE catalog_files ADD COLUMN value_length INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_files ADD COLUMN full_attention_interval INTEGER NOT NULL DEFAULT 0; -- hybrid models: every Nth layer is attention
ALTER TABLE catalog_files ADD COLUMN expert_used_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE catalog_files ADD COLUMN header_complete INTEGER NOT NULL DEFAULT 0;   -- 1: the whole metadata section was read; 0: stopped at the tokenizer
ALTER TABLE catalog_files ADD COLUMN present INTEGER NOT NULL DEFAULT 1;           -- 0 once the file has left its repo or its quant the tracked list
ALTER TABLE catalog_files ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
-- catalog_files.file_type: -1 when the header does not state general.file_type
-- (0 is a real value, F32). catalog_files.sha: the LFS sha256 of part 1.

-- A parsed GGUF header per file content. The key is the file's LFS sha256,
-- not the repo commit: a README edit does not invalidate a header.
CREATE TABLE hf_header_cache (
    id          INTEGER PRIMARY KEY,
    created_at  TEXT    NOT NULL,
    repo        TEXT    NOT NULL,
    filename    TEXT    NOT NULL,
    sha         TEXT    NOT NULL,
    header_json TEXT    NOT NULL,            -- gguf.Header, tokenizer excluded
    bytes_read  INTEGER NOT NULL DEFAULT 0,  -- how much of the file the read took
    UNIQUE (repo, filename, sha)
);

-- The last model-info answer per repo with its ETag, so an unchanged repo
-- is a 304 on the next refresh.
CREATE TABLE hf_listing_cache (
    id         INTEGER PRIMARY KEY,
    created_at TEXT NOT NULL,
    repo       TEXT NOT NULL UNIQUE,
    etag       TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL,                -- the JSON as the Hub sent it
    fetched_at TEXT NOT NULL
);

-- One row per refresh, whoever started it (the CLI, the API, step 10's
-- nightly watch), with its report.
CREATE TABLE catalog_refreshes (
    id          INTEGER PRIMARY KEY,
    created_at  TEXT NOT NULL,               -- when it started
    finished_at TEXT NOT NULL,
    trigger     TEXT NOT NULL,               -- cli | api | watch
    sizes       INTEGER NOT NULL DEFAULT 0,
    resolved    INTEGER NOT NULL DEFAULT 0,
    report_json TEXT NOT NULL DEFAULT '{}'
);

-- How an installed model maps onto the catalogue (catalog_file_id is in v0).
ALTER TABLE installed_models ADD COLUMN catalog_model_id INTEGER REFERENCES catalog_models (id);
ALTER TABLE installed_models ADD COLUMN catalog_match TEXT NOT NULL DEFAULT ''; -- '' not yet mapped | file | model | unknown
ALTER TABLE installed_models ADD COLUMN catalog_note TEXT NOT NULL DEFAULT '';  -- in words, for the curator
