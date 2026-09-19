# Step 4 — Model catalogue

**Status: GATE PASSED (2026-09-19). Step 4 is done; step 5 may start — in a
new session, with Fable.**

Commits on `main`: `c1013aa` step 4 (pushed by Itay), `49615eb` the gate's
follow-up fix (local, one ahead of `origin/main` — **push it**). Author
`Claude <noreply@anthropic.com>`. The device shell cannot push (no SSH key);
the container has no credentials for the private repo.

## The gate, as it ran

"Every family in the seed resolves to per-quant metadata without a weight
download, and a deliberately malformed header fails loudly."

- `scripts/verify.command` on the M1 Pro (2026-09-19, build `c1013aa`):
  **ALL GREEN**. go mod tidy clean, `make test` (embedded UI + noui), UI
  tests, `make build` (4 binaries), smoke test, hardware profile unchanged
  (gpu_medium, 11.84 GiB Metal budget), schema version 3.
- `advisor catalog refresh`: **22 of 22 sizes resolved, 139 files**, no
  weights downloaded. Every family's tracked quants plus vision encoders;
  Llama 3.3 70B's split quants summed (Q5_K_M/Q6_K/Q8_0, 2 parts each).
- Installed models on the Mac: `llama3.2:1b`, `llama3.2:3b`, `llama3.1:8b`
  matched; `minicpm-v4.6:latest` and `qwen3:4b` listed as unknown (curator
  signal, as designed).
- Malformed headers: covered by unit tests (all green on the Mac too).

## What the gate found, and what was fixed (`49615eb`)

- **1.5 GB of header downloads instead of ~10 MB.** Current llama.cpp
  quantizers write `general.file_type` (and quantization_version) *after*
  the tokenizer; it was a required key for the early stop, so nearly every
  read went through 6–11 MB of vocabulary. Fixed: file_type is no longer
  required (it's a cross-check; the quant is in the file name, bytes come
  from the listing), -1 when stated late. ARCHITECTURE.md **D-37**; D-33
  marked superseded in part. Verified by unit tests (a first refresh is back
  to one 64 KiB range per file); not re-run live — no need, resolution is
  unchanged. The next `verify.command` run will show the byte count drop.
- **Gemma 4 12B context**: model card says 256K, its GGUF files state
  131072 → families.yaml now follows the files (note added). That was the
  only "Worth a look" warning.

## For step 5 (Fable) — in ARCHITECTURE.md's open items

- Hybrid attention: `full_attention_interval` (Qwen3.5/3.6/3.8), per-layer
  `head_count_kv` with zeros (Nemotron-H).
- `block_count` is one more than the model card's layers for Qwen3.5/3.6/3.8
  and GLM-4.7-Flash (Qwen3.8 27B: 65 vs 64) — most likely the MTP layer
  (`nextn_predict_layers` in header_json); it should keep no KV cache.
- Gemma 4 states `key_length` 512 (global layers); sliding-window layers
  may use a smaller head dim stated separately (a `_swa` key).
- GLM-4.7-Flash (`deepseek2`, MLA): stated head dim 576 = kv_lora_rank 512
  + rope 64 — KV sized by `kv_lora_rank`, not head_count_kv.
- Sliding windows (Gemma 4, gpt-oss), `active_parameters` for speed,
  projector bytes when images are used, `file_type` -1 = not stated.

## What exists

- `data/catalog/families.yaml` — curated families from ~1B to ~70B
  (Qwen3.5 small sizes, Qwen3.6 35B-A3B, Qwen3.8 27B, Gemma 4, gpt-oss 20B,
  Devstral Small 2, Ministral 3, Nemotron 3 Nano, GLM-4.7-Flash, Llama
  3.2/3.1/3.3), reviewed 2026-09-18 with model-card sources; schema adds
  `quants` (tracked: Q3_K_M, IQ4_XS, Q4_K_M, Q5_K_M, Q6_K, Q8_0, MXFP4),
  `source`, `active_parameters`, `notes`. MoE and vision families are in
  and marked (D-32).
- `internal/catalog/gguf` (parser, stops at the tokenizer), `internal/
  catalog/hf` (listings with ETags, range reads, rate limits, redirect
  allow-list, unreachable → stop), `internal/catalog/refresh` (Run +
  MapInstalled), migration 0003, store methods.
- API: `GET /api/catalog`, `POST /api/catalog/refresh`, `GET
  /api/catalog/unknown`; installed models carry `catalog_match`. CLI:
  `advisor catalog check|refresh`. Daemon syncs the YAML at start.
- UI types mirrored; no screen (step 8).
- ARCHITECTURE.md D-32..D-37; CLAUDE.md; verify.command runs the refresh.

## Session notes

- **Go workaround** (no Go from the network in either environment): Go
  1.27.1 built from GitHub source (bootstrap: container's Go 1.24.7), and
  the **Mac's own module cache** (`~/go/pkg/mod/cache/download`, read-only
  grant) tarred and used as `GOPROXY=file://…`. Real `modernc.org/sqlite`,
  no driver swap, `go mod tidy -diff` clean. Reuse this next session.
- GGUF fixtures: first MBs of Ollama blobs from `~/.ollama/models/blobs`
  (read-only grant), gzipped; provenance in
  `internal/catalog/gguf/testdata/README.md`.
- Hugging Face is blocked from the container and the device VM; web fetch
  reached the Hub API for checking repos. The live refresh only runs on the
  Mac itself (verify.command).
- Commits travel as git bundles: `.git/` isn't writable via the remote
  tools, so bundles go to the repo root, then `git fetch` + `merge
  --ff-only` in the device shell, then deleted (delete permission granted).

## Next

Push `49615eb`. Then step 5 (Fable) in a new session: paste BUILD_PLAN.md's
step 5 prompt; it should read ARCHITECTURE.md's open items first.
