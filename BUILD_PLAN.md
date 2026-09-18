# Local LLM Advisor & Optimizer — build plan (fourteen steps)

**What this builds.** A desktop application for people who want to run AI
models on their own computer and do not want to learn VRAM arithmetic,
quantization names, or which runtime to pick. It looks at the machine, finds
what is installed, recommends models and settings that will actually fit,
tests them, and says when something new is worth trying. It runs on Ubuntu,
macOS and Windows 11. It is the MVP of `PRD — Local LLM Advisor & Optimizer.md`
§17 — no more than that.

**Read before any step:** the PRD (§6–§8 for the journey, §17 for the MVP
line, §21 for the risks), then the Decisions and Product rules below. From
step 1 on, the repo's own `CLAUDE.md` carries the rules.

**Who this is for, in one line, so no step forgets it:** someone who has heard
you can run AI on your own computer, owns a laptop or a gaming PC, and has no
idea what a GGUF is. Itay's own machines are the test fleet, not the customer.
The Mac Pro on Ubuntu with 2013 AMD GPUs is an edge case the product should
survive, not design for.

**Decisions already taken, do not relitigate them mid-step:**

- **Not fully web.** A process on the machine is unavoidable: hardware probing,
  GPU readings, driving Ollama, running benchmarks. The UI is ordinary web code
  served by that process at `http://127.0.0.1:<port>` — the same shape as
  Ollama and Open WebUI. No Electron, no Tauri in the MVP; a tray icon plus
  "open in browser" is the desktop experience.
- **One Go binary per OS, React + TypeScript UI embedded in it, SQLite for
  everything local.** Go cross-compiles to all three targets from one machine
  and needs no runtime on the customer's; pure-Go SQLite (`modernc.org/sqlite`)
  keeps cgo out. Not TypeScript end-to-end: Node has no clean single-binary
  story across three OSes and native modules need a build per platform.
- **Ollama is the only backend in the MVP.** The backend interface exists from
  step 3 so llama.cpp and LM Studio (PRD §18) are a second file each, not a
  rewrite — and it is reviewed against both of their APIs before it is frozen.
- **Runtimes and chat apps are two lists.** A runtime is what runs the model
  (Ollama now; llama.cpp, LM Studio later) and the advisor drives it. A chat
  app is where the person talks to the model (Ollama's own app, LM Studio, the
  Open WebUI desktop app, Jan, AnythingLLM) and the advisor only detects it and
  hands off. The MVP drives one runtime and installs no chat app.
- **The runtime path is a fact the app establishes, not an assumption.** A GPU
  existing and Ollama using it are different things. Ollama drives NVIDIA via
  CUDA, Apple via Metal, some AMD cards via ROCm, and the rest of AMD and Intel
  via Vulkan when it can — and falls back to CPU silently when it cannot. The
  app detects which path was actually taken and says so in plain words. For an
  AMD or Intel owner that sentence may be the most useful thing it ever says.
- **AMD and Intel are customers, not a later phase.** Detection, estimation and
  the runtime path cover every vendor from step 2. What leans on NVIDIA and
  Apple Silicon is only the *gates* — they are where the estimator can be
  checked against real VRAM readings.
- **The candidate set is curated** — a maintained list of trusted model families
  with purpose tags — with Hugging Face as the metadata source. Open discovery
  across all of HF is Phase 2. A beginner shown fifty thousand repos has been
  shown nothing.
- **The advisor calls no LLM to do its own job.** Estimation is arithmetic,
  recommendation is rules over data. The only models that run are the ones
  being tested.
- **No telemetry and no community database in the MVP** (PRD §11 is Phase 2).
  Nothing leaves the machine except requests to the model sources.
- **Official APIs and permitted sources only.** Step 9a decides which; until it
  has, Hugging Face is the only external source.

## Product rules — copy these into the repo's `CLAUDE.md` in step 1

1. **The user never needs a terminal.** A step that needs one is a step the
   product has failed.
2. **Plain language first.** Every technical term the UI shows has a one-line
   explainer a tap away; the technical columns live behind an "Advanced"
   toggle, off by default.
3. **Every recommendation says why**, in words the user can act on ("fits your
   graphics card with room for long documents"), and what it costs ("a 5 GB
   download").
4. **Estimated is never dressed as measured.** Two visual treatments,
   everywhere, always. A measurement replaces an estimate the moment it exists.
5. **Nothing changes on the machine without a click on a button that says what
   it will do** — "Download 5 GB", "Start Ollama", "Run a two-minute test". No
   automatic model switching (PRD §12).
6. **Weak hardware is a tier, not an error.** A laptop with no graphics card
   gets an honest answer ("small models only, roughly a few words a second"),
   not a failure screen.
7. **The daemon binds to 127.0.0.1 only.** No prompt, no file, no usage data
   leaves the machine.
8. **Counts and model names in prose rot.** The catalogue is data; the docs say
   "the curated families", never "the 12 families".

## Test fleet

Itay's machines cover the three operating systems, which is what packaging and
detection need. They do not cover the two machines most beginners actually
have, and both must be borrowed or found before step 13:

- **A Windows PC with a consumer NVIDIA card** (8–16 GB). The largest
  real-world segment.
- **A laptop with no discrete GPU.** The most common machine a beginner will
  try this on, and where product rule 6 is tested.

The Ubuntu Mac Pro (AMD D700s, already running llama.cpp on Vulkan) is the
AMD/Vulkan candidate: Ollama now ships Vulkan on by default on Linux and
Windows, so it may well drive those cards. Try it in step 0. If it does, it is
the AMD test box; if it does not, it is the CPU-only tier and "no supported
GPU" is the correct answer, not a bug. A Windows PC with a Radeon card is a
useful third borrow, after the two above.

---

## How to run this task

**One step per session.** Each step below is a self-contained prompt — paste it
as the whole first message. The model is named per step and **a session must not
switch model mid-step**: the steps are grouped the way they are precisely so
that each one fits a single model's job.

| Step | What | Model | Gate |
|---|---|---|---|
| 0 | The estimator experiment — no app, no UI | **Opus** | **Everything after this is conditional on its result** |
| 1 | Architecture record, repo skeleton, three-OS CI | **Fable** | Every later step inherits its shape |
| 2 | Hardware detection across the real-world matrix | **Opus** | Wrong here is wrong everywhere |
| 3 | Backend interface + Ollama adapter, install, and the runtime path | **Sonnet** | The first thing a beginner hits |
| 4 | Model catalogue — curated families, HF metadata, GGUF parser | **Opus** | Feeds the estimator |
| 5 | Fit estimator + recommendation engine | **Fable** | The product's central claim |
| 6 | Benchmark harness | **Opus** | Measurement correctness |
| 7 | First-run onboarding — the beginner's first hour | **Sonnet** | The product, from the customer's side |
| 8 | Working screens | **Sonnet** | — |
| 9a | External benchmark sources — research note | **Opus** | A decision, not a survey |
| 9b | External benchmark ingestion + the public/local split | **Sonnet** | — |
| 10 | New-model watch + desktop notifications | **Sonnet** | — |
| 11 | Packaging, installers, run-at-login, tray | **Sonnet** | Product rule 1 is tested here |
| 12 | Security & privacy review + full code review | **Opus** | — |
| 13 | Three people who are not developers install it unaided | **Itay + testers** | The only step that can say it works |

**Why Fable is where it is.** Two steps decide the shape of everything after
them: the skeleton and the estimator. A wrong skeleton is paid for in every
later session; a wrong estimator makes the one-line product definition (PRD
§24) false. Everything else is bounded implementation (Sonnet) or
correctness-sensitive plumbing — hardware edge cases, GGUF parsing,
measurement — where Opus is enough. Two Fable sessions for the whole MVP.

**Which model does what at runtime** — none. See Decisions: the advisor is
arithmetic and rules. If a step finds itself wanting to call a model to make a
recommendation, the catalogue is missing a field; add the field.

---

## Step 0 — Does the estimator hold? (Opus)

**Why first.** The product's central claim is "I can tell you what fits your
machine before you download it". If the memory estimate is wrong by 30%, every
recommendation is a coin flip and no UI fixes that. Finding out costs a day,
needs Ollama and a few models, and needs no repo layout, no UI, no schema.
**If the gate fails, the next session is a research session on the estimator,
not step 1.**

```
Read PRD §6 step 3 (compatibility analysis) and §8 (metrics).

Create the repo folder with go.mod (module name: advisor; current stable Go)
and ONE throwaway program, cmd/probe0/main.go. No UI, no database, no other
packages. It will be moved under scripts/ in step 1, not deleted.

It does two things and prints a report:

1. Hardware, the quick way — enough to label the machine:
   OS + version, CPU model + cores, RAM, and for the GPU: vendor, name, VRAM.
   Use the tools each OS already has: nvidia-smi where present; on Linux also
   /sys/class/drm/*/device/mem_info_vram_total; on macOS system_profiler
   SPDisplaysDataType -json plus sysctl hw.memsize and iogpu.wired_limit_mb;
   on Windows nvidia-smi, and for non-NVIDIA the registry value
   HardwareInformation.qwMemorySize under the display-class key — NOT
   Win32_VideoController.AdapterRAM, which is a 32-bit field. Print "unknown"
   where a value cannot be read; never guess.

2. The estimator experiment. For every model Ollama has installed
   (GET /api/tags), read GET /api/show and take from model_info:
     block_count, attention.head_count, attention.head_count_kv,
     embedding_length, context_length, and details.quantization_level;
   weights_bytes = the blob size from /api/tags.
   For each of num_ctx = 4096 and 32768:
     predicted = weights_bytes
               + KV cache: 2 * block_count * head_count_kv
                           * (embedding_length / head_count) * num_ctx
                           * 2 bytes                       (f16 cache)
               + a compute/graph overhead term you choose and STATE.
     Then load the model (POST /api/generate with that num_ctx, one token,
     keep_alive 5m), read GET /api/ps for size and size_vram, and where
     nvidia-smi exists also record the ACTUAL VRAM delta (used memory before
     and after the load). Unload (keep_alive 0) between runs.
   Print one row per (model, ctx): predicted, ollama_reported, actual_vram,
   error %, whether Ollama split layers to CPU, and which path Ollama took
   for the GPU — read /api/ps (size vs size_vram) and the server log's
   device discovery lines: cuda, metal, rocm, vulkan, or cpu. On AMD Linux,
   take the actual VRAM delta from rocm-smi / amd-smi or the sysfs
   mem_info_vram_used file when present.

Exclude and label MoE and vision models — they are their own problem and must
not hide inside the average. Skip any model whose weights alone exceed the
machine; say so in the row.

Cross-compile the program for linux/amd64, darwin/arm64 and windows/amd64
from the one machine and write the exact commands in README.md. Run it on
every test machine available.
```

**Done when** the report has run on at least one NVIDIA machine and one Apple
Silicon machine, and on an AMD machine if one is available — the Mac Pro
first. **Gate:** predicted within 15% of what the machine actually used for at
least four of every five (model, ctx) rows on each. Report the overhead term
that achieved it, the worst row, and which runtime path each machine took.
**Report to Itay and stop** — do not start step 1 in the same session.

---

## Step 1 — Architecture record, skeleton, three-OS CI (Fable)

**Why Fable.** Everything after this step lives inside the shape this step
chooses: package boundaries, the storage model, how the UI talks to the daemon,
the build. A wrong choice is paid for in every later session, and this is the
one step that spans every layer at once.

```
Read the PRD in full, then BUILD_PLAN.md — Decisions, Product rules, and the
step table — and cmd/probe0/main.go from step 0 with its recorded result.

Write ARCHITECTURE.md (an ADR: the decisions from BUILD_PLAN.md restated as
decisions with their consequences, plus the ones you take here) and CLAUDE.md
(the Product rules, verbatim, plus the repo conventions you set — how to run,
test, build, and where things go). Then lay down the skeleton:

  cmd/advisor/           main: starts the daemon, opens the browser once
  internal/server/       HTTP API on 127.0.0.1 only; embedded UI (go:embed)
  internal/store/        SQLite (modernc.org/sqlite, no cgo), migrations,
                         schema v0 for: hardware_profiles, backends,
                         installed_models, catalog_models, catalog_files,
                         catalog_external, estimates, benchmark_runs,
                         benchmark_samples, watch_state, settings
  internal/hardware/     Profile struct + Detect() stub returning "unknown"
  internal/backend/      Backend interface (step 3 fills it) + registry
  internal/catalog/      types only
  internal/estimate/     types only
  internal/recommend/    types only
  internal/bench/        types only
  internal/watch/        types only
  ui/                    Vite + React + TypeScript: a shell with navigation
                         for the screens in PRD §17's workflow, every screen
                         a placeholder; an Advanced toggle in settings state
                         from day one
  data/catalog/          empty families.yaml with the schema in a comment
  scripts/probe0/        step 0's program, moved, unchanged

Two rules the skeleton must enforce, not document:
- The server refuses to bind to anything but 127.0.0.1. A constant, not a
  flag.
- Every API type carrying a number a user will see has an explicit
  `source: "estimated" | "measured"` field wherever both are possible.
  Product rule 4 lives in the type system, not in the UI's discipline.

Build: `make dev` (daemon + Vite with proxy) and `make build` producing ONE
binary per target for linux/amd64, darwin/arm64, darwin/amd64 and
windows/amd64, UI embedded. CI: a GitHub Actions matrix that builds on
ubuntu, macos and windows runners, runs `go test ./...` and the UI tests,
and uploads the binaries as artifacts.

Nothing else. No hardware detection, no Ollama calls, no catalogue content.
A `/api/health` returning the version and the OS is the whole API.
```

**Done when** the CI artifact from one green run starts on all three operating
systems, opens the browser, and shows the shell. State which commit.

---

## Step 2 — Hardware detection (Opus)

**Why Opus.** The matrix is wide and the failure mode is silent: a wrong VRAM
number produces confident wrong recommendations, which is worse than no
recommendation. The population this serves is every consumer machine, not the
test fleet.

```
Read CLAUDE.md, ARCHITECTURE.md, scripts/probe0/main.go, and PRD §6 step 1.

Implement internal/hardware.Detect() → Profile for the real-world matrix, in
this priority order (the order is the size of the audience):

  1. Windows + NVIDIA (GeForce RTX 20/30/40/50, laptop variants)
  2. Apple Silicon Macs (M1–M4 families, every tier)
  3. Windows + AMD Radeon (RX 6000/7000/9000)
  4. Linux (Ubuntu) + NVIDIA, then + AMD
  5. Machines with only an integrated GPU (Intel/AMD iGPU), or no GPU
  6. Intel Arc, Intel Macs, multi-GPU boxes — detect, label, do not optimise

Profile carries: os + version; cpu (model, cores, whether AVX2 / AVX-512 are
present — CPU inference depends on it); ram_bytes; gpus[] (vendor, name,
vram_bytes, driver_version, is_integrated); storage (free bytes on the volume
where the backend keeps models — the default Ollama models folder per OS, or
OLLAMA_MODELS if set); is_laptop where the OS says; and a derived `tier` plus
one plain-language sentence for the UI.

Per GPU, also an `expected_backend` ∈ {cuda, metal, rocm, vulkan, none} —
what Ollama should be able to use for this card, from vendor + model against
data/hardware/runtime-support.yaml (the ROCm-supported list as Ollama ships
it, Vulkan for the rest of AMD and Intel, with the date each row was
checked). This is the expectation; step 3 records what actually happened,
and the two are shown side by side when they differ.

Apple Silicon is unified memory and macOS caps the GPU's working set below
total RAM. Report gpu_usable_bytes from what the OS actually says
(iogpu.wired_limit_mb when set, else the platform default for that RAM size)
and write the derivation next to the code. Do not hard-code a percentage.

Laptops with an iGPU and a discrete GPU: the discrete one is the answer; the
iGPU is listed and marked. Two discrete GPUs: list both, primary first, and
note in the profile that multi-GPU is not optimised in the MVP.

Every value the code cannot read is "unknown" — never a default, never a
guess. Unit tests with fixture outputs from each tool (nvidia-smi CSV,
system_profiler JSON, sysfs files, the Windows registry values) so every
parser is tested on every CI runner regardless of which OS it targets.

Wire GET /api/hardware and store the profile on every daemon start; keep the
history (a user changes a GPU; the benchmarks from before must stay
attributable to the old one).
```

**Done when** the parsers pass fixture tests on all three CI runners, and the
live profile is right on every machine in the test fleet — including the one
that should say "no supported GPU".

---

## Step 3 — Backend interface, Ollama adapter, install, runtime path (Sonnet)

**Why this includes installing Ollama.** For the customer, "Ollama is not
installed" is the normal first-run state, not an error. Product rule 1 says
they will not open a terminal. So the adapter has to get Ollama onto the
machine, with the user's click, on all three operating systems.

```
Read CLAUDE.md, ARCHITECTURE.md, internal/backend/, and PRD §15 (Ollama).

1. Define the Backend interface, once, for every runtime the PRD names:
     Detect() Status          // not_installed | installed_not_running |
                              // running (with version) | unsupported
     Models() []Installed     // name, tag, size, quant, family, modified
     Show(name) ModelInfo     // the metadata fields steps 4 and 5 need
     Running() []Loaded       // name, size, size_vram, until
     Pull(name, progress) error
     Generate(req, stream) error   // used by step 6 only
     Unload(name) error
     Install(progress) error       // see 3
     Start() error
   One implementation in internal/backend/ollama/. llama.cpp and LM Studio
   are second files later; if the interface leaks an Ollama assumption, fix
   the interface, not the caller. Before freezing it, read the LM Studio
   local API and CLI surface and llama-server's API and check every method
   against them. The seam that breaks interfaces is where a model comes
   from — an Ollama library tag versus a GGUF file from Hugging Face — so
   Pull takes a ModelSource (step 4's catalogue carries both per size), not
   a string.

2. The Ollama adapter over its HTTP API on the configured host (default
   127.0.0.1:11434; honour OLLAMA_HOST). Pull streams progress in bytes so
   the UI can say "2.1 of 4.7 GB". Unload is keep_alive: 0.

3. Install and Start, per OS, driven entirely by a UI button that says what
   will happen (product rule 5):
   - macOS and Windows: download the official installer from the official
     download endpoint over HTTPS to a temp folder, show progress, verify
     what can be verified (host, TLS, a published checksum where one
     exists), then launch it and wait for Detect() to change. The user
     still clicks through the installer; the app tells them so beforehand.
   - Linux: a user-space install from the official Linux tarball into the
     app's own data folder, run as a child process the daemon supervises.
     No sudo, no curl | sh, no system service. State in a comment what this
     trades away (no start-at-boot outside the app) and why it wins for the
     customer.
   Record the version installed and the version detected.

4. Inventory: on every daemon start and on demand, write Models() into
   installed_models and expose GET /api/backends and
   GET /api/models/installed. "Installed but not running" must be
   distinguishable from "not installed" in the API — the UI's two buttons
   depend on it.

5. The runtime path. After any model load, read what Ollama actually did:
   /api/ps (size vs size_vram, the GPU/CPU split) and the server log's
   device discovery — and record `runtime_path` ∈ {cuda, metal, rocm,
   vulkan, cpu} per GPU on the backend row, with the Ollama version and the
   relevant environment (OLLAMA_VULKAN, GGML_VK_VISIBLE_DEVICES,
   HSA_OVERRIDE_GFX_VERSION) captured, never set. GET /api/backends returns
   it beside step 2's expected_backend. "You have a graphics card and
   Ollama is not using it" must be a distinct, detectable state, because it
   is the state an AMD or Intel owner is most likely in.

Do not touch the estimator, the catalogue, or the UI beyond a temporary
status line in the shell.
```

**Done when** on a machine with no Ollama, the click installs it, starts it,
and the inventory lists a model pulled through the app — on each of the three
operating systems — and the runtime path reported for each test-fleet machine
matches what its Ollama log says.

---

## Step 4 — Model catalogue (Opus)

**Why Opus.** The GGUF header parser and the fields it extracts are the input
to step 5; a wrong `head_count_kv` makes the KV-cache estimate wrong by a
factor of four. And the curated file is a schema decision that the
recommendation engine, the watch, and the UI all read.

```
Read CLAUDE.md, ARCHITECTURE.md, the internal/store schema, PRD §6 step 2 and
§7, and the Decisions in BUILD_PLAN.md (curated catalogue; HF as the
metadata source).

1. data/catalog/families.yaml — the curated catalogue. Per family: id,
   display name, maintainer, licence (SPDX where possible, otherwise the
   name + URL), purposes it is credible for (coding, chat, reasoning,
   long_context, vision, agentic, writing — this enum is shared with the
   UI), and sizes[] each with: parameter count, context_length, the Ollama
   library tag a beginner would pull, and the HF GGUF repo used for
   metadata. Seed it with the families a careful person would recommend
   today across those purposes, from ~1B to ~70B parameters; stamp each
   entry with the date it was reviewed. The file is data — the docs must
   not count it.

2. internal/catalog/hf — the Hugging Face Hub API client. For a repo: list
   files with sizes (the model-info endpoint with blobs), then for each
   .gguf read ONLY the header via an HTTP range request and parse it in
   internal/catalog/gguf: magic, version, tensor count, and the KV metadata
   (general.architecture, <arch>.block_count, <arch>.attention.head_count,
   <arch>.attention.head_count_kv, <arch>.embedding_length,
   <arch>.context_length, general.file_type; the tokenizer skipped
   entirely). Multi-part GGUFs: read part 1's header, sum all parts' sizes.
   Never download weights. Respect HF's rate limits and etags; cache
   headers by (repo, file, sha).

3. Persist into catalog_models / catalog_files: one row per quant variant
   with bytes, bits-per-weight, and the header fields step 5 needs. The
   nightly refresh is step 10's; here it is `advisor catalog refresh` and
   POST /api/catalog/refresh.

4. Map installed models (step 3's inventory) to catalogue rows by
   family + size + quant, and flag installed models the catalogue does not
   know — that is the signal the curator (Itay) needs, not an error.

Unit tests for the parser with real GGUF headers captured as fixtures (the
first few MB of files, never whole files).
```

**Done when** every family in the seed resolves to per-quant metadata without a
weight download, and a deliberately malformed header fails loudly.

---

## Step 5 — Fit estimator + recommendation engine (Fable)

**Why Fable.** This is the product. PRD §24 stands or falls on whether "will it
fit, and how fast" is right, and on whether the recommendation is explainable
to someone who does not know what a KV cache is. It combines step 0's
arithmetic, step 2's profile, step 4's catalogue, and a speed model that has to
be honest about being an estimate. Get it right once.

```
Read CLAUDE.md, ARCHITECTURE.md, PRD §6 step 3, §7, §12, §13 and §21, the
step 0 program and its recorded result, internal/hardware, internal/catalog,
and BUILD_PLAN.md product rules 3, 4 and 6.

1. internal/estimate — Fit(profile, catalogFile, ctx, kvCacheType) →
   Estimate: weights, kv_cache, overhead, total, gpu_resident_bytes,
   cpu_offload_bytes, and category ∈ { fits_with_headroom, fits,
   needs_cpu_offload, reduced_context_only, not_recommended } with the
   threshold that decided it. Use the overhead term step 0 validated; put
   every constant in one exported config with a comment naming the
   measurement that justifies it. Apple Silicon compares against
   gpu_usable_bytes, not RAM. CPU-only machines compare against RAM with
   the OS's own needs reserved.

2. A speed estimate that is a RANGE and is labelled as such in the type:
   generation tok/s ≈ effective memory bandwidth / bytes read per token,
   scaled by an efficiency factor; prompt processing estimated separately.
   Bandwidth comes from data/hardware/gpus.yaml — seed it with consumer
   NVIDIA, AMD, Intel Arc and Apple Silicon parts, each row with the source
   it was taken from and a date. The efficiency factor is keyed by the
   runtime path, and the width of the range with it: tight for cuda and
   metal, wider for rocm, wider still for vulkan, widest for cpu (where RAM
   bandwidth and AVX support are the inputs). Put the factors in one
   config, each with the measurement behind it; add scripts/calibrate/ with
   a README telling Itay how to run llama-bench on a machine and feed its
   numbers back — that is the dev-side instrument, never something the
   customer installs. Unknown GPU → no speed estimate, and say so; never
   invent a number. The range narrows to a point the moment a measurement
   exists (step 6).

3. internal/recommend — Recommend(profile, purposes[], installed[],
   preferences) → []Recommendation, at most three, each carrying: the
   catalogue file, the context to run it at, the estimate, and reasons[]
   written for the customer ("fits your 16 GB graphics card with room for
   long documents", "about 5 GB to download", "the family people use for
   coding"). Score = purpose fit × fit category × speed × size, with the
   weights in one exported config. If the user already has a model, every
   recommendation says what it changes versus that model, or is dropped.
   When a GPU exists but the runtime path is cpu, the recommendation is
   built for the CPU and its first reason says so in plain words — "your
   graphics card is not being used by Ollama; these are the numbers without
   it" — with a link to the explainer for why (unsupported card, missing
   driver, Vulkan off).

4. Confidence: every Recommendation carries confidence ∈ {high, medium,
   low} derived from which inputs were measured, estimated or unknown, and
   the UI shows it. PRD §21's last risk lives here.

5. Tests: reproduce every step 0 row within the gate; a CPU-only laptop
   profile gets small models and a warning, never an empty list; a 24 GB
   card is never handed a model that wastes it as the top pick when a
   purpose it asked for has a better fit; an unknown GPU yields fit
   categories without speed numbers; a Radeon profile whose runtime path
   came back cpu gets CPU-sized models with the not-used reason first.

Expose GET /api/recommend?purposes=… and GET /api/models/{id}/fit.
```

**Done when** the tests pass, and for each machine in the test fleet the top
recommendation is one Itay would actually give that machine, with reasons he
would say out loud.

---

## Step 6 — Benchmark harness (Opus)

**Why Opus.** A benchmark that is not repeatable is worse than no benchmark:
the user changes models on noise. Timing must come from the runtime's own
counters, runs must be controlled, and every row must carry enough context to
be comparable later (PRD §21, first risk).

```
Read CLAUDE.md, ARCHITECTURE.md, PRD §8, internal/backend, internal/estimate.

internal/bench:

1. A fixed suite in data/bench/: three prompts of roughly 500, 2,000 and
   8,000 tokens of ordinary English text, and a completion budget of 256
   tokens. Deterministic options: temperature 0, fixed seed, num_predict
   256, the num_ctx under test. Text the suite ships, never text the user
   typed — product rule 7.

2. A run = one warm-up (discarded) + N=3 timed runs per prompt; report the
   median and the spread. Timing from Ollama's own response fields:
   prompt_eval_count / prompt_eval_duration → prompt tok/s,
   eval_count / eval_duration → generation tok/s, load_duration; and TTFT
   measured at the client as time to the first streamed token.

3. A resource sampler running alongside at 1 Hz where a tool exists:
   nvidia-smi (utilisation, memory used, temperature, power) on NVIDIA;
   rocm-smi / amd-smi or the sysfs counters on AMD Linux; /api/ps size_vram
   everywhere; system RAM from the OS. Where nothing exists — AMD and Intel
   on Windows, most iGPUs — sample nothing and say so in the row. No
   sudo-only tools.

4. Every benchmark_runs row stores the whole context: hardware profile id,
   backend + version, the runtime path taken, model + quant + bytes,
   num_ctx, KV cache type, flash attention setting, suite version, daemon
   version. Samples go to benchmark_samples. Nothing is comparable without
   all of it — a vulkan run and a rocm run on the same card are two rows.

5. Refuse to run a configuration the estimator says will spill to CPU
   unless the request says measure_anyway — a beginner should not wait ten
   minutes for a number that tells them what the estimate already said.

6. When a run completes, write the measurement back so the estimate for
   that (model, quant, ctx) on this profile is replaced by the measured
   value (product rule 4, second sentence) and the range for similar
   configurations narrows.

POST /api/bench (starts, returns a run id), GET /api/bench/{id} (streams
progress), GET /api/bench/history. Cancel must actually unload the model.
```

**Done when** two consecutive runs of the same configuration agree within 5% on
generation tok/s on the NVIDIA test machine and the Apple Silicon one, and a
cancelled run leaves nothing loaded.

---

## Step 7 — First-run onboarding (Sonnet)

**Why this is its own step.** The customer's entire experience of the product
is the first hour. Everything above is invisible if this sequence asks a
question they cannot answer. It is Sonnet's because every decision it needs has
been taken; the work is copy, sequence and states.

```
Read CLAUDE.md (the Product rules are the spec for this step), PRD §3, §6,
§7, §12 and §17's workflow, and every endpoint the daemon exposes.

Build the first-run flow in ui/, one screen per state, in this order, and
make it what the browser opens to until it has been completed once:

1. Welcome — two sentences: what this does, and that it does not chat (the
   user will chat in Ollama's own app, Open WebUI, or whatever they use;
   say so plainly). One button.
2. Checking your computer — the hardware profile as one sentence and the
   tier, "Show details" collapsed. If the GPU is unknown, say what was
   found and what that means, without apology.
3. Ollama — one of the three states from step 3, each with one button that
   says what it will do: "Install Ollama (about N MB)", "Start Ollama", or
   "Found Ollama 0.x". Progress in bytes. If models are already installed,
   list them.
4. What will you use AI for? — the purposes enum as checkboxes with a
   one-line description each, and a default when they pick nothing ("Not
   sure — general chat").
5. Recommendations — the up-to-three cards from step 5: name in words, the
   reasons, the download size, the confidence, and estimated speed as a
   range in the "estimated" treatment. One primary button per card:
   "Download X GB".
6. Getting it — pull progress, cancellable.
7. Try it — a one-minute benchmark (the 500-token prompt only) with the
   result shown next to the estimate it replaces, in the "measured"
   treatment. The user watches the estimate become a measurement; that is
   the product's promise made visible.
8. Use it — the exact model name to pick in their chat app, a copy button,
   and the chat apps found on this machine. Add a small internal/chatapps
   package that detects installed chat apps by their well-known install
   locations per OS, and list them in this order: Ollama's own app first
   (it came with the runtime they just installed), then LM Studio and the
   Open WebUI desktop app if present — the latter labelled with its
   maturity as it stands at build time — then Jan and AnythingLLM. Nothing
   found → say which ones exist and link to their download pages. The
   advisor never installs a chat app. Then the working screens (step 8).

Copy rules: no term from {VRAM, quantization, GGUF, KV cache, context
window, tokens/sec, offload} appears without its explainer; write the
explainers once in ui/src/copy/glossary.ts and reuse them. Every number
carries its `source` treatment from the API type. Every wait shows what is
happening and how long it usually takes.

Keep all strings in one place from the start (i18n-ready), English only.
```

**Done when** a person who has never seen the app — watched by Itay and not
helped — gets from Welcome to a measured number on their own machine, once.

---

## Step 8 — Working screens (Sonnet)

```
Read CLAUDE.md, the API, and step 7's ui/ (reuse its components and glossary).

Screens behind the navigation the step 1 shell laid out:

- Home — your machine in one sentence, your current model (if one is
  installed and was benchmarked), and one card: the single most useful
  thing to do next.
- Models — installed models with fit badges (the five categories, as
  words), size on disk, measured or estimated speed with its treatment, and
  free space on the models volume. "Remove" needs a confirm that says the
  GB it frees.
- Recommend — the purpose checkboxes again, re-runnable; results as in the
  onboarding.
- Benchmarks — run (pick model, pick context, "about N minutes"), a history
  table, and a compare view of two runs side by side. The Advanced toggle
  reveals prompt tok/s, TTFT, VRAM, temperature and the full configuration.
- Settings — the Advanced toggle, notifications (step 10), the models
  folder and its free space, the data folder, the version, "check for
  updates" (step 11), and a button that opens the local data folder.

Empty states are written, not blank. Errors say what to do next, in words.
No new API unless a screen cannot be built without it; then add the
smallest one and say so.
```

**Done when** every PRD §17 capability has a screen a beginner can find without
a tour.

---

## Step 9a — External benchmark sources, the research note (Opus)

```
Read PRD §10 and §21, and BUILD_PLAN.md's Decisions ("official APIs and
permitted sources only").

Write research/EXTERNAL_SOURCES.md, one section per candidate source:
Hugging Face (the models API, and any benchmark or leaderboard data it
exposes), Artificial Analysis, LMArena, the Ollama library / registry, and
any others you find that publish model-quality or hardware-throughput data.
For each: what it exposes, through what (API, download, page), under what
terms (quote the clause), what it costs, how fresh it is, and which field in
our catalogue it would fill. Then a decision: which sources the MVP ingests,
in what order, and which are explicitly excluded and why. Scraping a page
whose terms do not permit it is excluded regardless of how useful it would
be.

Also decide the display rule for PRD §10's split — public data and local
measurements never in the same column — and write it in the note for step
9b to implement.
```

**Done when** the note ends in a list step 9b can build from, with a clause
quoted for every source on it.

---

## Step 9b — External benchmark ingestion (Sonnet)

```
Read research/EXTERNAL_SOURCES.md, internal/catalog, and the Models and
Recommend screens.

Implement internal/catalog/external: one client per source the note
approved, each writing to catalog_external (source, model-id mapping, metric
name, value, fetched_at, licence) — never into the same columns as local
measurements. Mapping from a source's model names to our catalogue rows is
data (data/catalog/aliases.yaml), flagged when it misses.

Recommendation engine: external quality signals feed the purpose-fit term
only, with a weight in the config; they never touch the fit or speed terms.

UI: the "Public data / Your machine" split from the note on the model detail
view, with the source and date under every public number.
```

**Done when** a recommended model shows a public quality signal with its source
and date, and a local measurement beside it in a different treatment, on the
same screen.

---

## Step 10 — New-model watch + desktop notifications (Sonnet)

```
Read PRD §12, internal/catalog, internal/recommend, and CLAUDE.md product
rule 5.

internal/watch:

1. A scheduler in the daemon (default daily, jittered) that refreshes the
   catalogue from the approved sources and looks for new GGUF files for the
   curated families, plus new repos from the same maintainers — which are
   flagged for the curator, not recommended.
2. Every new candidate goes through Fit and Recommend against the stored
   profile and the user's purposes. It qualifies for a notification only if
   it fits and beats the user's current model on a purpose they selected,
   and the reasons are stated in the notification body exactly as PRD §12's
   example shows. Once per model, ever.
3. Desktop notifications on all three operating systems from the daemon,
   with a quiet setting and a "never" setting; clicking opens the app on
   that model's card with "Run benchmark". Never pull, never switch.
4. A watch log in the UI: what was checked, when, what was found, what was
   suppressed and why.
```

**Done when** a model added to the catalogue that qualifies produces exactly
one notification with a reason, and one that does not qualify produces a log
line and nothing else.

---

## Step 11 — Packaging, installers, run-at-login, tray (Sonnet)

**Why this cannot be last-minute.** Product rule 1 is tested here and nowhere
else. If the customer's first contact is a terminal, a Gatekeeper block or a
SmartScreen wall they were not warned about, nothing above it happened.

```
Read CLAUDE.md product rule 1 and ARCHITECTURE.md.

Ship the daemon as something a beginner installs:

- macOS: a .app bundle wrapping the binary (tray icon, "open in browser"),
  in a .dmg, per architecture or universal. Notarised — a Developer ID is
  Itay's decision (below); without it Gatekeeper stops the customer at the
  first double-click, so the CI job must be ready for the certificate the
  day it exists.
- Windows: an installer (Inno Setup or WiX) that puts the app in Program
  Files, offers run-at-login, and adds a tray icon. Code signing is the same
  decision; unsigned means a SmartScreen warning the customer must click
  through, and the install page has to say so honestly.
- Linux: a .deb and an AppImage; run-at-login via a user systemd unit the
  app offers to install (no sudo).
- All three: first launch opens the browser at the app; later launches just
  start the daemon; "check for updates" compares against the release feed
  and links to the download — no self-update in the MVP.
- GoReleaser or equivalent producing all of it from the CI matrix, with the
  UI already embedded.

Write INSTALL.md for the customer, per OS. Write RELEASING.md for Itay.
```

**Done when** on each OS a fresh machine goes from a download to the Welcome
screen with no terminal, and Itay has decided whether to pay for signing — the
answer changes the install page's copy, not the code.

---

## Step 12 — Security & privacy review, full code review (Opus)

```
Read everything under internal/ and ui/, CLAUDE.md product rule 7, and PRD
§21 (privacy).

Review and fix, in one pass:

- The daemon binds to 127.0.0.1 only and it is not configurable; the origin
  check rejects anything but the daemon's own origin; no CORS wildcard.
- Every outbound request goes to a host on an allow-list in one file
  (Hugging Face, the approved external sources, the Ollama download host);
  the list is the audit.
- Nothing the user typed is ever sent anywhere; the benchmark prompts are
  the suite's, and the code makes that impossible rather than merely true.
- The installer download path: host pinning, TLS, checksum verification
  where a checksum is published, and a clear failure otherwise.
- Local data: what is stored, where, and a "delete everything" that does.
- Dependencies: audit for known vulnerabilities; pin versions.
- Then a full code review against ARCHITECTURE.md: where a product rule is
  documented but not enforced, enforce it.

Write SECURITY.md stating what the app does and does not do with data, for
the customer, in plain language.
```

**Done when** the findings are written, the fixes are in, and SECURITY.md is
something Itay would put on the download page.

---

## Step 13 — Three people who are not developers (Itay + testers)

The only step that can say whether this works. Everything above can pass its
tests and still confuse the one person the product is for.

Find three people who do not write code, on their own machines — ideally one
Windows gaming PC, one MacBook, one ordinary laptop with no graphics card. Send
them the download link and nothing else. Do not help. Watch if you can, and
for each answer:

1. Did they reach a recommendation without asking a question?
2. Did they understand why it was recommended — ask them to say it back?
3. Did the estimate and the measurement read as two different things?
4. Where did they stop, and what did they say when they stopped?
5. What did the app say that made them wince, or laugh?

On one of the three machines, also install LM Studio and put its "will it
fit" badge next to our recommendation for the same model. If the advisor says
nothing LM Studio does not already say, that is a finding about the product,
not the tester.

Answers 4 and 5 are the real output. Record them in `backlog/` in the repo, one
file per finding — the same convention as INFU: an idea is one file, no shared
list.

---

## After the MVP

PRD §18, in this order, each its own task file: the llama.cpp adapter (the
first time the backend interface earns its keep, and the path for GPUs Ollama
drives badly), LM Studio (its local API and CLI; MLX on Apple Silicon, which
is often the faster path there), then the community hardware database — which
needs a small cloud endpoint and its own privacy note before a line of code —
then workload quality evaluations (PRD §9), which is the first time the
advisor itself runs a model, as a local judge.

Watch items, not tasks: the Open WebUI desktop app's built-in engine (a user
on that path has models living outside Ollama), MLX-native runtimes on Macs,
and Ollama's Vulkan support graduating from experimental. Dropped: vLLM — no
beginner install path. A runtime earns a slot only with a beginner install
path AND a local API the advisor can drive.
