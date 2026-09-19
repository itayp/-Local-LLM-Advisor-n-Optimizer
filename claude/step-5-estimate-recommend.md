# Step 5 — Fit estimator + recommendation engine

**Status: GATE PASSED (2026-09-19). Step 5 is done; step 6 may start — in a
new session, with Opus.**

Commits on `main`, both pushed (`main` == `origin/main` at close): `992db0f`
step 5, `86f6632` the Windows CI fix (step 3's `ollama.exe` fixture key +
actions moved off Node 20 — see `claude/ci-failures-to-fix.md`). Author
`Claude <noreply@anthropic.com>`. Everything was compiled and tested in the
cloud container against the real `modernc.org/sqlite`: `make check`,
`make test` (UI embedded + UI tests, 31), `go test -tags noui ./...`,
`make build` (four binaries), and a smoke test of the linux binary's two new
endpoints.

## The gate, as it ran

"The tests pass, and for each machine in the test fleet the top
recommendation is one Itay would actually give that machine, with reasons he
would say out loud."

- **Itay, 2026-09-19:** tested on every fleet machine; the recommendations
  look legit. Gate passed.
- **M1 Pro, `scripts/verify.command`** (verify.log, 13:43 CEST): **ALL
  GREEN** — go mod tidy, `make test`, `make build`, smoke test, hardware
  profile unchanged (gpu_medium, 11.8 GB Metal budget), catalogue refresh.
  What `advisor recommend` printed (llama3.1:8b installed, so every card
  compares against it):
  - chat → **Gemma 4 12B** (7.8 GB, fits with headroom, ≈ 16–22 tok/s) /
    Ministral 3 14B (9.7 GB, fits) / Qwen3.5 4B
  - coding → **Qwen3.5 9B** (7.1 GB, 16k context, "made for coding, which
    llama3.1:8b is not")
  - long_context + vision → **Gemma 4 12B** at 64k (8.7 GB) / Qwen3.5 9B /
    Ministral 3 3B
- Windows PC (RTX 5070 Ti) and Mac Pro (D700): checked by Itay in the app;
  no output captured here.

## Worth a look in step 6 (not blockers — found reading verify.log at close)

1. **Gemma 4 12B cards carry no sliding-window note** — the tripwire this
   doc set. Reading `internal/catalog/layout.go`: when the header states
   `attention.sliding_window_pattern` as a per-layer list (what gemma4's
   converter writes), the list branch marks the layers but emits **no note**;
   only the period / known-architecture branch notes. And the cache looks
   correctly sized: 8k → 64k context adds ≈ 0.9 GB (7.8 → 8.7 GB), ≈ 16 KB
   per token — the same order as step 0's measured gemma4:e4b (17 KB/token);
   full attention on every layer would be an order of magnitude more. So most
   likely a missing note, not a wrong estimate (the printed GB are rounded).
   Confirm with `GET /api/models/{id}/fit` terms and a real load; the fix is
   a note in the list branch (count of sliding layers + window).
2. **M1 Pro chat #3 differs from the fixture test.** The real Mac shows
   Qwen3.5 4B; `TestFleetTopPicks` expects Qwen3.5 9B for the M1 Pro golden
   profile. Itay accepted the real answer. Find which input differs (the
   installed llama3.1:8b as current? the real 11.8 GB budget?) so the test
   pins what the machine does.
3. **Speed ranges at long context are too wide to be advice.** Ministral 3
   3B at 64k: ≈ 14–80 tok/s ("roughly 11 to 60 words a second"). First
   calibration target together with the Mac Pro's D700 (AMD-on-Vulkan range
   probably over-promises there).
4. Still open from the build: speed ranges vs. Ollama's own `--verbose` eval
   rate — the constants come from llama-bench, not Ollama.

## What exists

- `internal/estimate`: `Fit(machine, model, request)` → memory terms,
  gpu_resident / cpu_offload, category ∈ five (+ `unknown` when the budget
  cannot be read) with `threshold` in words; `Place` (Apple → gpu_usable_bytes;
  card → largest device; processor → RAM − OS reserve; integrated graphics →
  system memory without the "card not used" message; the path SEEN after a
  load wins over the expectation); the speed range; `WithMeasurement` (the
  step 6 seam); `OllamaDefaultContext` (4k/32k/256k by graphics memory, from
  Ollama's source). Every constant in `Config`, marked MEASURED (fleet),
  MEASURED (public) or CHOSEN.
- `internal/catalog/layout.go`: `Layout` — which layers keep a cache, which
  slide, which are recurrent, shared, MTP; MLA; per-layer heads; derived from
  `header_json` **on every read** (no refresh needed; step 4 rows work as is).
- `data/hardware/gpus.yaml`: NVIDIA (RTX 50/40/30/20, GTX 16/10, laptops),
  AMD (RX 9000/7000/6000/5700/580, Radeon VII, FirePro D700), Intel Arc,
  Apple M1–M5 incl. Pro/Max/Ultra with GPU-core variants, and processor
  memory ranges by family. Parser re-derives bandwidth = data rate × bus
  width ÷ 8; ordered rows; ambiguous pci.ids names take the slowest match.
- `scripts/calibrate/` + README: llama-bench JSON → efficiency and prompt
  ratio vs. the configured range, results file to commit.
- `internal/recommend`: `Engine.Recommend` — at most three, one per family,
  the file the Ollama tag pulls; score = purpose × fit × speed^1.5 × size;
  templated reasons (copy rule tested); versus-current or dropped; the
  GPU-not-used reason first with explainer id `gpu_not_used` + the why;
  confidence high/medium/low + why; `empty` + `empty_code`.
- API: `GET /api/recommend?purposes=…[&current=&min_context=&gpu_only=&allow_split=]`,
  `GET /api/models/{id}/fit[?ctx=&kv=]` ({id} = a size's id from
  /api/catalog). Both re-check the runtimes first. Migration 0004:
  `backends.hardware_fingerprint` (an observed runtime path never crosses a
  GPU swap).
- UI: the Recommend screen (purposes, warning + "why is my card not used",
  current model, cards with Figure ranges, confidence badge + why, Advanced
  table with every term's explainer, the fetch-the-model-list button).
- `advisor recommend` (dev-side text printer of a running daemon's answer);
  verify.command and CI call it.
- ARCHITECTURE.md D-38..D-43 + open items; CLAUDE.md; README status;
  families.yaml header (purposes are read MOST CREDIBLE FIRST).

## Evidence gathered in the build session

- Step 0 replay: 27/28 within 15%, worst llama3.2:3b @4096 on the M1 Pro at
  −20.8% — identical to the recorded result, pinned in `step0_test.go`.
- Step 0's four minicpm-v4.6 rows (qwen35 hybrid, excluded there as vision)
  land within 10% with one-layer-in-four caching; with every layer counted
  the 32k rows over-predict by 40%. Its gemma4:e4b rows grow 17 KB/token vs
  16 KB for a shared-cache, mostly-sliding layout.
- Speed constants from llama.cpp's scoreboards read 2026-09-19 (discussions
  #15013 CUDA, #4167 Apple, #10879 Vulkan, #15021 ROCm, #15396 gpt-oss):
  cuda 0.62–0.76 of bandwidth, metal base/Pro 0.68–0.84 (Max/Ultra lower →
  per-row overrides), rocm 0.54–0.68, vulkan AMD 0.59–0.86 / Intel 0.32–0.59;
  MoE generation 0.53–0.72 of the dense efficiency. Tests pin public numbers
  inside the ranges (RTX 3090 8B-class, gpt-oss-20b on the 5070 Ti at 189 tok/s).
- llama.cpp facts used (5b59b83): SWA cache = pad256(min(ctx, window + 512));
  recurrent state = (conv−1)(inner + 2·groups·state) + state·inner, f32; MLA
  caches K only; per-arch SWA periods; gemma4 writes pattern/shared/key_length_swa.
- Ollama facts used (6383a0f): default num_ctx 4k/32k/256k at 23/47 GiB.

## Decisions worth knowing next session

- Estimates are NOT written to the `estimates` table (no writes on a GET);
  step 6 owns the row and feeds measurements through `Engine.Measurements` /
  `Estimate.WithMeasurement`.
- CHOSEN constants to revisit with step 6 data: FitsFraction 0.92,
  HeadroomFraction 0.80, OS reserve (4 GiB Win/mac, 2.5 Linux), processor
  efficiency 0.35–0.80 and NoAVX2 0.5.
- Default quant only (Q4_K_M, else MXFP4, else nearest). Ollama's library
  default for llama3.2:1b is Q8_0 — a per-size `ollama_quant` field is the
  fix when it matters. Non-default quants need the Ollama adapter to pull
  `hf.co/…` (it returns ErrUnsupportedSource today).
- No recency/quality signal beyond size: an older family of the same size
  can top a list (the 8 GB laptop gets Llama 3.1 8B for chat because it has
  headroom where the 9B models are tight). Step 9b is where that belongs.
- Split (offload) models compete only when nothing fits, or with
  `allow_split=1`.

## Session notes

- Go workaround as in step 4: Go 1.27.1 built from GitHub source in the
  container; the Mac's module cache (`~/go/pkg/mod/cache/download`, granted
  read access) tarred and used as `GOPROXY=file://…`. proxy.golang.org,
  go.dev, sum.golang.org still 403 from the container.
- Repo in/out as git bundles through the connected folder; delete
  permission was granted to clean them up (done — nothing left behind).
- The device VM has no Go, so nothing was run on the Mac; verify.command is
  the live check.
- **Read-only git in the device shell: use `git --no-optional-locks …`.**
  A plain `git status` refreshes the index and can leave an empty
  `.git/index.lock` the VM cannot unlink without delete permission — which
  then blocks every git command on the Mac. Happened at close; removed.

## Next

Step 6 (Opus): the benchmark harness, in a new session. Read ARCHITECTURE.md
D-43 (the measurement seam) and D-38's open items first, then "Worth a look
in step 6" above — items 1–3 are cheap to settle once a run can measure.
