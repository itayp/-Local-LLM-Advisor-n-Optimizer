# CLAUDE.md — Local LLM Advisor & Optimizer

The working reference for every session in this repo. Read it first, then
`ARCHITECTURE.md` (the decisions and why), then the step you are on in
`BUILD_PLAN.md`. The PRD is `PRD — Local LLM Advisor & Optimizer.md`.

**Who this is for, in one line:** someone who has heard you can run AI on
their own computer, owns a laptop or a gaming PC, and has no idea what a GGUF
is. Itay's machines are the test fleet, not the customer.

## Product rules

Verbatim from `BUILD_PLAN.md`. They are the spec; when code and a rule
disagree, the code is wrong.

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

## Two rules the code enforces (do not weaken them)

- **Rule 7 is a constant.** `server.LoopbackHost` is `const`; `server.Listen`
  takes only a port; `Serve` refuses a non-loopback listener; every request
  needs a loopback `Host` header. There is no bind flag and there must never
  be one. Tests: `internal/server/server_test.go`.
- **Rule 4 is a type.** A number a user will see about this machine is a
  `figure.Bytes` or `figure.Rate` with `source: "estimated" | "measured"`;
  a number someone else published about a model is a `figure.Public`, which
  has no source, carries its origin (publisher, the source's own date,
  attribution) and never shares a struct with the other two
  (`TestPublicAndLocalNeverShareAStruct`; ARCHITECTURE.md D-53). A numeric API field
  that is neither (an id, a count, a timestamp, configuration, a value read
  from the OS) carries the struct tag `source:"n/a"` and a comment saying
  which. `server.APITypes()` lists every API type and
  `TestEveryUserFacingNumberHasASource` fails the build otherwise. When you
  add an API type, add it to that list. In the UI, a number with provenance
  is rendered by `<Figure>` and nothing else, and a public value by
  `<PublicFigure>` and nothing else — in its own block, never the same
  column, row or sentence as a local number.

## Layout

```
cmd/advisor/            main: opens the store, starts the server on 127.0.0.1, opens the browser once
internal/server/        HTTP API (/api/*), Host/Origin checks, embedded UI (go:embed), APITypes()
internal/store/         SQLite via modernc.org/sqlite; migrations/NNNN_*.sql; DefaultDataDir
internal/hardware/      Profile + Detect: per-OS probes behind an env seam, runtime-support rules, tier, fingerprint
internal/backend/       Backend interface + registry; internal/backend/ollama arrives in step 3
internal/catalog/       curated families: YAML loader + validation, repo-file grouping, installed-model matching (step 4)
internal/catalog/gguf/  the GGUF header parser (stops at the tokenizer; real header fixtures in testdata/)
internal/catalog/hf/    the Hugging Face client: listings with ETags, header range reads, rate limits
internal/catalog/refresh/  Run (YAML → Hub → catalog_models/catalog_files, then the public sources) and MapInstalled
internal/catalog/external/ public benchmark data (step 9b): the approved sources' clients (Hugging Face Eval Results, Arena, Epoch AI), a polite fetcher limited to PermittedHosts, the coverage report, and the View the screens and the engine read
internal/estimate/      Fit (memory terms, split, category + the threshold that decided), the speed range, placement, gpus.yaml loader; Config holds every constant
internal/recommend/     Recommend: at most three cards with templated reasons, versus-current, confidence; Config holds every weight
internal/bench/         the benchmark harness (step 6): suite loader, plan + spill refusal, one-at-a-time runner with a cancel that unloads, 1 Hz resource sampler (per-OS probes behind a sysEnv seam), medians + spread, write-back and calibration evidence
internal/watch/         new-model watch state, notifications, log (step 10)
internal/figure/        Source, Bytes, Rate, Public, Check and CheckSeparation — product rule 4, and public data kept apart
internal/version/       Version (set by -ldflags), GoVersion
ui/                     Vite + React + TypeScript; builds into internal/server/ui/dist
data/                   data.go embeds the data files (package advisor/data)
data/catalog/           families.yaml — the curated catalogue (data, never counted in prose); external.yaml — the approved public-data sources and the metric → purpose map; aliases.yaml — each source's names for catalogue sizes
data/hardware/          runtime-support.yaml — which GPU path Ollama should use per card; gpus.yaml — memory bandwidth per graphics part and processor family; both with sources and dates
data/bench/             suite.yaml + text.txt — the benchmark suite: the advisor's own prose, three prompts, the options; versioned and pinned by digest (suite_test.go)
scripts/probe0/         step 0's estimator experiment, unchanged, with its reports in results/ (internal/estimate's tests replay them)
scripts/calibrate/      the dev-side speed instrument: llama-bench JSON → the speed model's factors, results/ to commit; README says how
claude/                 step-by-step build history: one doc per BUILD_PLAN.md step (closed once its gate passes), plus backlog.md and ci-failures-to-fix.md; real repo files, not just Project docs
.github/workflows/      CI: ubuntu, macos, windows
```

Dependency direction is one way (`ARCHITECTURE.md` D-11): `cmd` → `server`
→ domain packages → `figure`/`version`. Nothing imports `server`.

## How to run, test, build

Needs Go (the version in `go.mod`; a newer toolchain fetches it) and Node 22+.

```
make dev        # daemon (-tags noui, no browser) + Vite dev server with /api proxied; Ctrl-C stops both
make ui         # build the UI into internal/server/ui/dist
make test       # UI build, go test ./... (UI embedded), UI tests — what CI runs
make test-go    # go test -tags noui ./... — no Node needed
make check      # gofmt, go vet, tsc
make build      # dist/advisor-{linux-amd64,darwin-arm64,darwin-amd64,windows-amd64[.exe]}
```

On a Mac, `scripts/verify.command` (double-clickable) runs `go mod tidy`,
`make test`, `make build` and a smoke test of the built binary, and writes
the output to `verify.log` — the same sequence as CI, for a machine where
a session cannot run Go itself.

- A fresh clone needs `make ui` once before a plain `go build ./...` works,
  because `go:embed` needs the built UI to exist. Until then use `-tags noui`.
- `make dev` keeps its data in `./.dev-data` (`ADVISOR_DATA_DIR`), never in
  the real data folder.
- The daemon: `advisor [-port N] [-data-dir DIR] [-no-browser] [-v] [-version]`.
  The port is the only network setting.
- The curator's catalogue tools (never the customer's):
  `advisor catalog check [FILE]` validates `families.yaml`, `external.yaml`
  and `aliases.yaml` offline;
  `advisor catalog refresh [-data-dir DIR] [-family ID,...] [-json]`
  resolves every size against Hugging Face (listings + header range reads,
  no weights), stores it, and lists installed models the catalogue does not
  know, then reads the public benchmark sources that are due
  (`-no-external` skips them); it exits 1 if a size did not resolve. The
  daemon's `POST /api/catalog/refresh` runs the same thing.
  `advisor catalog external [-data-dir DIR] [-force] [-report] [-capture DIR]`
  reads the approved public sources alone and prints the coverage report
  (each source's hits and misses across the curated sizes); `-report` is
  offline; `-capture` saves every answer, for fixtures.
- The developer's view of a running daemon's recommendations, as text:
  `advisor recommend [-port N] [-purposes chat,coding] [-current NAME] [-detail]` —
  what `scripts/verify.command` prints for build-plan step 5's gate;
  `-detail` adds the top pick's detail view (public data and this machine,
  apart — step 9b's gate).
- The developer's benchmark client for a running daemon (build-plan step 6's
  gate): `advisor bench [-model NAME] [-num-ctx N] [-prompts 500,2000]
  [-runs 2] [-measure-anyway]` runs the suite and prints it (exit 3 when
  consecutive runs differ by more than `-agree`, 5%; without `-model`, the
  smallest installed curated model of 3B parameters or more that fits —
  ARCHITECTURE.md D-50); `advisor bench -cancel-during loading|measuring`
  checks a cancel leaves nothing loaded (asking Ollama's own `/api/ps`
  too); `advisor bench -history`. The customer's is the Benchmarks screen,
  on the same API.
- `go run ./scripts/calibrate -label NAME bench.json` scores a llama-bench
  run against the speed model (scripts/calibrate/README.md).
- UI alone: `cd ui && npm run dev | build | test | check`.
- This machine's hardware profile, as the daemon would read it:
  `ADVISOR_PRINT_PROFILE=1 go test ./internal/hardware -run TestDetectOnThisMachine -v`,
  or `GET /api/hardware` on a running daemon.

## Conventions

**Go.** `gofmt`, `go vet`, standard library first. Errors are wrapped with
context (`fmt.Errorf("store: open %s: %w", …)`); package-prefixed messages.
`log/slog` for logging. Contexts on anything that waits. Tests live beside
the code, use `t.TempDir()`, and never touch the network or the real data
folder. Fixture files, not live tools, for every parser: hardware
detection reads the machine through `internal/hardware`'s `env` seam, and a
machine is one `testdata/<os>/<name>.txtar` holding each tool's output in its
real format (`-- $ nvidia-smi --`, `-- sys/bus/pci/devices/…/class --`). A
fixture says in its header whether values were captured or chosen. Each
scenario has a golden profile in `testdata/golden/`; after an intended change
run `go test ./internal/hardware -run Scenario -update` and review the diff
like code. No symlinks and no colons in fixture file names (Windows checks
them out). GGUF fixtures are real headers — the first megabytes of a model
file, gzipped, never a whole file — with their provenance in
`internal/catalog/gguf/testdata/README.md`; the Hugging Face client and the
refresh are tested against an `httptest` hub that serves them by range. The
benchmark sampler reads the machine through `internal/bench`'s `sysEnv` seam
(fixtures in `testdata/sampler/`, each tool's real format), and the Ollama
adapter's load report parses server-log fixtures shaped from the format
strings of the Ollama and llama.cpp builds it names
(`internal/backend/ollama/testdata/README.md` says which). The public-data
clients are tested against an `httptest` fake of the three sources; their
fixtures (`internal/catalog/external/testdata/`) are shaped from
documentation until real answers captured by `scripts/verify.command`
(`.captures/external/`) replace them — the README there says which is which.

**API.** JSON, snake_case keys, `GET /api/…`. Register endpoints through
`Server.api("METHOD /api/path", handler)` so a wrong method is a 405. Errors
are `{"error": {"code", "message"}}`. Numbers a user sees are figures
(above). Strings the UI shows come from the API in words, not codes, when
they are for the user; codes are for the UI's logic.

**Store.** Every table has integer `id` + RFC 3339 UTC `created_at`;
booleans 0/1; a `*_json` column for a struct that will grow beside plain
queryable columns; `source` columns are `CHECK`ed. A schema change is a new
`internal/store/migrations/NNNN_name.sql`; never edit a shipped one. Typed
methods on `*store.Store`, not ad-hoc SQL in other packages.

**Unknown is unknown.** A value the code cannot read is `"unknown"` / `0`
with `*_known: false` / `NULL` — never a default, never a guess. The UI turns
it into a sentence, never `0 GB`. A speed the advisor cannot estimate is an
absent `Rate` and a sentence (`estimate.Speed.Unknown`); a memory budget it
cannot read is category `unknown`.

**Constants live in configs.** Every number the estimator uses is in
`estimate.Config` (`internal/estimate/config.go`), marked MEASURED (fleet),
MEASURED (public) or CHOSEN with what would settle it; every weight and
threshold of the recommendation engine is in `recommend.Config`; every
constant of the benchmark harness is in `bench.Config`, and what it sends to
the model is the suite (`data/bench/`) — change either and bump the suite's
version when the change alters what a run measures. No magic
number anywhere else in those packages. The memory formula is pinned by
`step0_test.go` — a change that moves a step 0 row is a change to
ARCHITECTURE.md D-20, not a refactor — and the outcomes the weights must
produce are pinned by `recommend_test.go` on the golden hardware profiles.

**What the customer reads is templated.** Reasons, warnings and the
confidence sentence are built in `internal/recommend/reasons.go` from the
facts the rules used, and arrive at the UI as sentences; the screen adds
labels, not claims. A test holds the copy rule over every reason.

**UI.** Every string in `ui/src/copy/en.ts` (i18n-ready, English only);
screens carry no prose of their own. Every screen is in `ui/src/screens/
index.ts`, which generates both the navigation and the routes. Settings
state (`ui/src/state/settings.tsx`) owns the Advanced toggle; read it with
`useAdvanced()`. API types are mirrored by hand in `ui/src/api/types.ts` —
change both sides together. `localStorage` is guarded and per-viewer only;
durable settings go through the daemon's `settings` table (step 8).

**Copy.** No term from {VRAM, quantization, GGUF, KV cache, context window,
tokens/sec, offload} without its explainer (step 7 adds the glossary).
Buttons say what they will do and what it costs. Never count the catalogue.

**Data files** (`data/`) are data: the schema is in the file's header
comment and mirrored by a Go type; entries carry the date a person reviewed
them and where they came from. They are embedded through `data/data.go` and
decoded strictly (`goccy/go-yaml`, unknown keys are errors).
`runtime-support.yaml` is checked against the Ollama release it names — its
build presets, not only its docs; `support_test.go` is its contract.
`gpus.yaml` rows state the memory configuration their bandwidth derives
from (the parser refuses one that does not equal rate × width ÷ 8), are
ORDERED (laptop parts above desktop parts of the same name), and carry an
efficiency or prompt-ratio override only with the measurement behind it;
`devices_test.go` lists real device names and the rows they must land on —
add the name when you add a row. A part that is not in the file gets no
speed estimate, by design.

**Dependencies.** Go: stdlib + `modernc.org/sqlite`, `github.com/goccy/go-yaml`
(data files) and `golang.org/x/sys` (CPUID) — ARCHITECTURE.md D-26. UI:
React, React Router, Vite, Vitest, Testing Library. A new one is a sentence
in the PR saying what it replaces. Nothing that needs cgo, ever.

**Network.** Outbound requests go only to the model sources on an allow-list:
Hugging Face (`huggingface.co` and its CDNs), the Ollama download host (step
3), and the public-data sources step 9a approved —
`datasets-server.huggingface.co` (Arena's dataset) and `epoch.ai` —
bounded by `external.PermittedHosts` and switched on in
`data/catalog/external.yaml` (ARCHITECTURE.md D-53). The public-data
requests are a function of the data files alone, the same on every
install. Nothing the user typed is ever sent anywhere. No telemetry. The
Hugging Face client (`internal/catalog/hf`) follows redirects only to
Hugging Face's own hosts, reads GGUF headers with range requests and
refuses a whole-file answer, never sends a token, and honours the Hub's
rate limits (ARCHITECTURE.md D-34).

**The advisor calls no LLM to do its own job.** Estimation is arithmetic,
recommendation is rules over data. Wanting a model to decide means the
catalogue is missing a field.

## Git

`main` is the integration branch. One build-plan step per session; a step
ends with a commit that names it (`step 1: architecture record, skeleton,
CI`). Do not commit `dist/`, `internal/server/ui/dist/`, `ui/node_modules/`
or any `*.db`; `.gitignore` has them. Line endings are LF everywhere
(`.gitattributes`).

## What each step reads

Before a step, read `CLAUDE.md`, `ARCHITECTURE.md`, and the packages the
step's prompt names. Step 0's findings that bind later steps are
`ARCHITECTURE.md` D-20. When a decision changes, add a superseding entry to
`ARCHITECTURE.md` rather than editing the old one.
