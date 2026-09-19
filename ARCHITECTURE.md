# Architecture decision record — Local LLM Advisor & Optimizer

**Status:** accepted, step 1 (2026-09-18); D-23 to D-26 added in step 2,
D-27 to D-31 in step 3, D-32 to D-37 in step 4, D-38 to D-43 in step 5,
D-44 to D-49 in step 6.
Every later step inherits this shape. A change to a decision here is a new
numbered entry that supersedes the old one — the old entry stays, marked
superseded, so the reasoning survives.

**Read with:** `PRD — Local LLM Advisor & Optimizer.md` (what and why),
`BUILD_PLAN.md` (the fourteen steps), `CLAUDE.md` (the product rules and the
repo conventions — the working reference for every session).

## Context

The product is the MVP of PRD §17: a desktop application for someone who has
heard you can run AI on your own computer, owns a laptop or a gaming PC, and
has no idea what a GGUF is. It looks at the machine, finds what is installed,
recommends models and settings that will fit, tests them, and says when
something new is worth trying. Ubuntu, macOS and Windows 11.

Step 0 (`scripts/probe0/`) established that the product's central claim
holds: memory use can be predicted from model metadata within 15% on 27 of
28 dense rows across CUDA, Metal and Vulkan. The formula that passed, and the
findings that came with it, are constraints on this architecture (D-20).

Decisions D-1 to D-10 restate what `BUILD_PLAN.md` decided before this step,
with the consequences made explicit. D-11 onward are taken here.

---

## D-1. A local daemon serves an ordinary web UI at `http://127.0.0.1:<port>`

**Decision.** Not fully web, not Electron, not Tauri. A process on the machine
is unavoidable — hardware probing, GPU readings, driving Ollama, running
benchmarks — and that process serves the UI over HTTP on the loopback
interface, the same shape as Ollama and Open WebUI. A tray icon plus "open in
browser" is the desktop experience (step 11).

**Consequences.**
- The UI is a normal single-page app; there is no IPC layer, no native
  bridge, no second build toolchain per OS.
- The daemon must be safe to leave running: it binds to loopback only (D-12)
  and defends against the browser being used against it (Host/Origin checks).
- The user's browser is a dependency the product does not control. The UI
  targets current evergreen browsers and nothing else.
- "Opening the app" means opening a URL. The first launch opens the browser
  once; later launches must not spawn a new tab every time (step 11).

## D-2. One Go binary per OS, React + TypeScript UI embedded, SQLite for everything local

**Decision.** Go for the daemon: it cross-compiles to all targets from one
machine and needs no runtime on the customer's. The UI is Vite + React +
TypeScript, built to static files and embedded in the binary with `go:embed`.
Local state is one SQLite file through `modernc.org/sqlite` (pure Go), so
there is no cgo and no C toolchain per platform. Not TypeScript end-to-end:
Node has no clean single-binary story across three OSes, and native modules
need a build per platform.

**Consequences.**
- `CGO_ENABLED=0` is a build invariant. A dependency that needs cgo is
  rejected, whatever it offers.
- The build is `make build`: four binaries (`linux/amd64`, `darwin/arm64`,
  `darwin/amd64`, `windows/amd64`), each self-contained. There is nothing to
  install beside the binary.
- Two toolchains (Go, Node) exist at development time only. The customer
  sees neither.
- Anything that must be shared between the Go and TypeScript sides — API
  types, enums — is mirrored by hand and kept in step by review, not by a
  code generator (D-19).

## D-3. Ollama is the only backend in the MVP; the backend interface exists from step 3

**Decision.** One runtime, driven through its HTTP API. The `Backend`
interface is defined once, in step 3, after checking every method against
the LM Studio local API and llama-server's API, so llama.cpp and LM Studio
(PRD §18) are a second file each, not a rewrite.

**Consequences.**
- `internal/backend` owns the interface and a registry; implementations live
  in `internal/backend/<name>` and register themselves. Step 1 ships the
  registry and the part of the interface the skeleton needs (`Name`,
  `Detect` → `Status`); step 3 completes it without renaming anything.
- Where a model comes from — an Ollama library tag versus a GGUF file on
  Hugging Face — is the seam most likely to leak. `Pull` takes a
  `ModelSource`, not a string (step 3).
- An Ollama-shaped assumption found in a caller is fixed in the interface,
  not in the caller.

## D-4. Runtimes and chat apps are two lists

**Decision.** A runtime is what runs the model (Ollama now; llama.cpp, LM
Studio later) and the advisor drives it. A chat app is where the person talks
to the model (Ollama's own app, LM Studio, the Open WebUI desktop app, Jan,
AnythingLLM); the advisor only detects it and hands off. The MVP drives one
runtime and installs no chat app.

**Consequences.**
- `internal/backend` never grows chat-app detection. Step 7 adds a small
  `internal/chatapps` package that knows well-known install locations per OS
  and nothing else.
- The product never becomes a chat interface (PRD §22). "Use it" ends with
  the model's exact name and a copy button.

## D-5. The runtime path is a fact the app establishes, not an assumption

**Decision.** A GPU existing and Ollama using it are different things. Per
GPU, `hardware` records the *expected* path (cuda, metal, rocm, vulkan, none)
from vendor + model against a data file, and `backend` records the path
*actually taken* after a load, from `/api/ps` and the runtime's own device
discovery lines. The two are shown side by side when they differ.

**Consequences.**
- `hardware.RuntimePath` is one type shared by both packages (defined in
  `hardware`, which `backend` imports; never the reverse).
- "You have a graphics card and Ollama is not using it" is a distinct,
  detectable state, because it is the state an AMD or Intel owner is most
  likely in. The recommendation engine builds for the CPU in that state and
  says so first (step 5).
- The estimator's overhead term is keyed by the runtime path (D-20), so an
  estimate made before any load is made against the expected path and
  labelled as such.

## D-6. AMD and Intel are customers, not a later phase

**Decision.** Detection, estimation and the runtime path cover every vendor
from step 2. What leans on NVIDIA and Apple Silicon is only the *gates* —
where the estimator can be checked against real VRAM readings.

**Consequences.**
- `hardware.Vendor` has nvidia, amd, intel and apple from day one; nothing
  in the skeleton special-cases NVIDIA.
- Where a vendor has no reading (AMD and Intel on Windows have no
  per-process VRAM counter), the row says so rather than showing zero.
- Step 0 answered the open question: the Ubuntu Mac Pro's D700s run on
  Vulkan under Ollama, and it is the AMD test box.

## D-7. The candidate set is curated; Hugging Face is the metadata source

**Decision.** A maintained list of trusted model families with purpose tags
(`data/catalog/families.yaml`), with Hugging Face as the source of per-quant
GGUF metadata. Open discovery across all of Hugging Face is Phase 2. A
beginner shown fifty thousand repos has been shown nothing.

**Consequences.**
- The catalogue is data. Docs, copy and code never state its size (product
  rule 8).
- The YAML is the schema of record; `catalog.Family` mirrors it. Step 4 adds
  the loader and the Hugging Face client; the schema in the file's header
  comment is what it loads.
- An installed model the catalogue does not know is a signal for the
  curator, stored as `installed_models.catalog_file_id IS NULL`, not an error.

## D-8. The advisor calls no LLM to do its own job

**Decision.** Estimation is arithmetic; recommendation is rules over data.
The only models that run are the ones being tested.

**Consequences.**
- There is no inference client in the daemon besides the backend adapter,
  and the adapter's `Generate` is used by the benchmark harness only.
- If a step wants a model to make a decision, the catalogue is missing a
  field. Add the field.
- Explanations ("fits your graphics card with room for long documents") are
  templated from structured reasons (`recommend.Reason`), which keeps them
  true and testable.

## D-9. No telemetry and no community database in the MVP

**Decision.** Nothing leaves the machine except requests to the model
sources. PRD §11 is Phase 2 and needs its own privacy note before a line of
code.

**Consequences.**
- There is no analytics, crash reporting or update-check-with-payload code
  path. "Check for updates" (step 11) compares a version string against a
  release feed and links to a download.
- Benchmark prompts are the suite's own text; nothing the user typed is ever
  sent anywhere, and the code should make that impossible rather than merely
  true (step 12 reviews it).

## D-10. Official APIs and permitted sources only

**Decision.** Until step 9a decides otherwise, Hugging Face is the only
external source. Scraping a page whose terms do not permit it is excluded
regardless of usefulness.

**Consequences.**
- Every outbound host is on an allow-list in one file (step 12); the list is
  the audit.
- Public data and local measurements never share a table or a column
  (`catalog_external` versus `benchmark_runs`), and never a UI column (PRD
  §10).

---

## D-11. Package layout and dependency direction

**Decision.** The repo is laid out as `BUILD_PLAN.md` step 1 specifies, with
two small additions (`internal/figure`, `internal/version`) justified below.
Dependencies point one way:

```
cmd/advisor ──▶ internal/server ──▶ store, hardware, backend, catalog,
                                     estimate, recommend, bench, watch
                                     (all of which may import figure, version)

hardware ◀── backend            (RuntimePath lives in hardware)
catalog  ◀── estimate ◀── recommend ◀── watch
figure   ◀── estimate, recommend, bench
```

`internal/server` is the only package that knows HTTP. No `internal/*`
package imports `server`, and nothing imports `cmd/advisor`. The domain
packages (`catalog`, `estimate`, `recommend`, `bench`, `watch`) are
types-only in step 1 and gain behaviour in their own steps without moving.

**Why.** One direction means one place to look for any concern, and no
package can reach back into HTTP handlers or the process. The order matches
the workflow: hardware → backend → catalogue → estimate → recommend →
bench → watch.

**Consequences.**
- A new concern gets a new package under `internal/`; nothing accretes in
  `server` or `main`. `cmd/advisor/main.go` stays a wiring file.
- Storage access is behind `internal/store` (D-13); other packages take a
  `*store.Store` and add typed methods to it rather than writing SQL where
  they live.
- The additions: `internal/figure` (D-14) because provenance is a cross-
  cutting type every domain package needs, and `internal/version` because
  both `main` and `server` need the build stamp and `-ldflags -X` needs a
  fixed import path.

## D-12. The daemon binds to `127.0.0.1` — a constant, refused otherwise, and checked twice

**Decision.** `server.LoopbackHost` is a `const`. `server.Listen(port)` is
the only listener constructor and takes only a port. `Server.Serve` refuses a
listener whose address is not loopback (`ErrNotLoopback`). Above the socket,
every request's `Host` header must be `127.0.0.1` or `localhost` (421
otherwise) and a browser-sent `Origin`, when present, must agree (403
otherwise). All of this is tested in `internal/server/server_test.go`.

**Why.** Product rule 7 says "binds to 127.0.0.1 only". A flag would make it
a default; a constant makes it a property. The Host check exists because
loopback binding alone does not stop a web page in the user's browser from
addressing the daemon through DNS rebinding.

**Consequences.**
- There is no bind-address flag, environment variable or setting, and there
  must never be one. A future request for LAN access is a product decision
  that reopens this entry, not a flag.
- IPv4 loopback only (`tcp4`): one address to reason about, one address the
  Host check admits.
- The Vite dev server proxies `/api` with `changeOrigin: true` so its
  requests carry the daemon's own Host.
- Step 12 reviews this layer; it does not need to introduce it.

## D-13. Storage: one SQLite file, numbered embedded migrations, schema v0 now

**Decision.** One database file in the per-OS application-data folder
(`store.DefaultDataDir`: `~/.local/share/advisor`, `~/Library/Application
Support/Advisor`, `%LOCALAPPDATA%\Advisor`; `ADVISOR_DATA_DIR` overrides).
WAL journal, `busy_timeout`, foreign keys on, a single connection. Schema
changes are `internal/store/migrations/NNNN_name.sql`, embedded, applied in
order in a transaction and recorded in `schema_migrations`. A shipped
migration is never edited. Schema v0 (`0001_schema_v0.sql`) creates every
table the fourteen steps need: `hardware_profiles`, `backends`,
`installed_models`, `catalog_models`, `catalog_files`, `catalog_external`,
`estimates`, `benchmark_runs`, `benchmark_samples`, `watch_state`, `settings`.

Column conventions: every table has an integer `id` and an RFC 3339 UTC
`created_at`; booleans are 0/1; a `*_json` column holds a Go struct expected
to grow, beside the plain columns that are queried; `source` columns are
`CHECK (source IN ('estimated','measured'))`.

**Why.** Every later step needs a place to write, and a schema that exists
before the code that fills it makes each step's data model a review item now
rather than a surprise later. Plain `database/sql` with hand-written SQL and
no ORM keeps the dependency count at one and the SQL readable.

**Consequences.**
- Step N adds columns or tables with `000N_*.sql`; it does not touch
  `0001_schema_v0.sql`. Tests check that migrations are consecutive and that
  the v0 table set survives.
- History is kept on purpose: `hardware_profiles` gets a row per daemon
  start, `installed_models` marks rows `present = 0` rather than deleting
  them, so a benchmark from last month stays attributable.
- `estimates` rows are updated in place when a measurement arrives (their
  `source` flips), which is product rule 4's second sentence expressed as a
  schema.
- The data folder is the one place the daemon writes (D-16); "delete
  everything" (step 12) deletes it.

## D-14. Provenance lives in the type system: `internal/figure`

**Decision.** A number a user will see is a `figure.Bytes` or a `figure.Rate`,
each carrying `Source ∈ {estimated, measured}` in a JSON field `source`.
`Source` refuses to marshal or unmarshal any other value, including the zero
value, so a forgotten provenance is a failing test or a 500, never a number
on screen that looks measured. Numeric API fields that are neither estimated
nor measured — ids, counts, timestamps, configuration such as `num_ctx`,
values read from the OS — carry the struct tag `source:"n/a"` with a comment
saying which. `internal/server.APITypes()` lists every type the API serves,
and `TestEveryUserFacingNumberHasASource` runs `figure.Check` over it: a new
numeric field without a figure type or the tag fails the build.

On the UI side, `ui/src/api/types.ts` mirrors `Source`, `Bytes` and `Rate`,
and `components/Figure.tsx` is the one component that renders such a value —
two visual treatments, the `source` prop required by the type.

**Why.** Product rule 4 ("estimated is never dressed as measured") is the
product's most-cited risk (PRD §21) and the easiest rule to erode one field
at a time. A convention in the UI is exactly that kind of erosion. A type
that cannot be serialised without provenance, plus a test that inspects every
API type, cannot be forgotten.

**Consequences.**
- An estimate is a range (`Rate.Low..High`) and a measurement is a point;
  the type says which, and the UI never guesses.
- Adding an API type means adding it to `APITypes()`; the reviewer looks for
  that line. Adding a numeric field means deciding, in the type, whether it
  is a figure.
- `hardware.Profile`'s numbers are facts read from the OS and are tagged
  `n/a`; what is derived from them lives in `estimate` as figures.
- The `estimates` table's `CHECK` (D-13) is the same rule at the storage
  layer.

## D-15. The UI is embedded with `go:embed`; a `noui` build tag keeps development honest

**Decision.** Vite builds to `internal/server/ui/dist` (gitignored);
`internal/server/ui.go` embeds it with `//go:embed all:ui/dist` and a SPA
handler serves files or falls back to `index.html`. `ui_noui.go` (`-tags
noui`) serves a one-paragraph placeholder instead, so the Go side compiles
and tests before the UI has ever been built. `make dev` runs the daemon with
`-tags noui` and the Vite dev server with `/api` proxied to it; `make build`
and CI build the UI first and embed it.

**Why.** `go:embed` cannot be conditional and refuses an empty directory.
The alternatives — committing a placeholder `index.html` that every build
overwrites, or a copy step — leave the working tree dirty after every build.
A build tag is explicit and cheap.

**Consequences.**
- A fresh clone needs `make ui` once (or `-tags noui`) before plain
  `go build ./...` works; `CLAUDE.md` says so. CI runs both configurations.
- Client-side routes work when served by the daemon because every non-file
  path returns `index.html`; the API is matched first, so `/api/*` never
  falls through to the SPA.
- Hashed assets under `/assets/` are cached for a year; `index.html` is not.

## D-16. Where things live on the customer's disk

**Decision.** The daemon writes to one folder, `store.DefaultDataDir()`
(D-13), and reads model files from wherever the runtime keeps them (the
default Ollama models folder per OS, or `OLLAMA_MODELS`). The Linux
user-space Ollama install (step 3) goes under the same data folder.

**Consequences.**
- The Settings screen can show one path for "your data" and one for "your
  models", and a button that opens each.
- No sudo, no system service, no writes outside the user's profile.
- `make dev` sets `ADVISOR_DATA_DIR=./.dev-data` so development never
  touches a real install.

## D-17. Versioning and `/api/health`

**Decision.** `internal/version.Version` defaults to `dev` and is set at link
time (`-ldflags -X advisor/internal/version.Version=<git describe>`) by the
Makefile and CI. `GET /api/health` returns `{version, os, arch, go_version}`
and is the whole API in step 1. Every API response is JSON with snake_case
keys; every error is `{"error": {"code", "message"}}`; a wrong method is 405
and an unknown `/api/*` path is a JSON 404.

**Consequences.**
- A developer's build says `dev`; a binary that says `dev` in the field was
  not built by CI.
- Benchmark rows record `daemon_version` (D-13), so results stay comparable
  across releases.
- The UI shows the version in the footer from the first screen, which is
  also the "is the daemon up" check.

## D-18. Build and CI

**Decision.** `make build` cross-compiles the four targets from any machine
with `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X …Version"`. CI
(`.github/workflows/ci.yml`) is a matrix on `ubuntu-latest`, `macos-latest`
and `windows-latest`; each runner builds the UI, runs the UI tests, `go vet`,
`go test ./...` with the UI embedded and again with `-tags noui`, builds its
own OS's binary natively, starts it and checks `/api/health`, the SPA and the
foreign-Host refusal, then uploads the binary as an artifact. The ubuntu
runner also runs `make build` and uploads all four. `darwin/amd64` is
cross-compiled on the arm64 macOS runner (there is no Intel runner to rely
on) and smoke-tested under Rosetta when present.

Toolchain versions come from the repo: Go from `go.mod` (`go 1.27.1`), Node
22 from the workflow, npm packages from `ui/package-lock.json`.

**Consequences.**
- Green CI means: tests pass on three operating systems, and a binary
  started on each one served the UI and answered the API. That is the step 1
  gate, minus a human opening the browser.
- Windows runners skip the `gofmt` check (checkout may rewrite line endings);
  `.gitattributes` forces LF so it should never matter.
- Release packaging (installers, signing, tray) is step 11 and builds on this
  job; it does not replace it.

## D-19. Two type systems, mirrored by hand

**Decision.** API types are written twice — Go in `internal/*`, TypeScript
in `ui/src/api/types.ts` — and kept in step by review. No OpenAPI, no
generator.

**Why.** The API is small and shaped by the product, not by a schema
language; a generator is a third toolchain and a source of drift of its own.
The Go side is the source of truth; the TypeScript side is a faithful copy
with the same names.

**Consequences.**
- A PR that changes an API type touches both files, or the reviewer asks
  why. Enums (`Source`, `Purpose`, `Category`, `State`) are copied as string
  unions with identical values.
- If the API grows past what hand-mirroring can hold, this entry is
  superseded with a generator — not silently.

## D-20. Constraints inherited from step 0 (the estimator experiment)

*Generalised, not changed, by D-38: every step 0 row still computes to the
byte. Finding 5's "excluded from recommendations until handled explicitly"
is discharged there — mixture-of-experts, hybrid and vision models are
handled explicitly, and marked as not yet measured.*

**Decision.** The estimator (step 5) implements exactly the formula that
passed the step 0 gate on three machines and three runtime backends, and the
schema, the catalogue types and the GGUF parser (step 4) carry the fields it
needs:

```
predicted = weights_bytes + KV + overhead
KV        = 2 · block_count · head_count_kv · head_dim · ctx · 2 B      (f16 cache)
head_dim  = attention.key_length where the model states one,
            else embedding_length / head_count
ctx       = min(num_ctx, the model's trained context_length)          (Ollama clamps)
overhead  = per runtime path: cuda 250 MiB · metal 0 · vulkan 50 · rocm 50 · cpu 0
```

Result: 27 of 28 dense rows within 15% (mean |err| 5.5%, median 3.9%);
worst row `llama3.2:3b` @ 4096 on the M1 Pro at −20.8%, a measurement-noise
row on a laptop in use.

**Findings that shape later steps.**
1. `attention.key_length` is the real head dimension where stated
   (`qwen3:4b` states 128 against a computed 80). `catalog_files.key_length`
   and `catalog.GGUFHeader.KeyLength` exist for this; the parser must read it.
2. `num_ctx` is clamped to the trained context; `estimates.effective_ctx`
   records what the estimate is actually for.
3. The overhead is a property of the runtime backend, not the model's width
   (fitted against width the slope is negative). The term is keyed by
   `runtime_path` — which is why D-5 matters to the estimator.
4. `/api/ps size` is Ollama's own estimate, not a measurement (0.09% apart
   across Metal and Vulkan for the same model). Measurements come from a
   device-memory delta; `/api/ps` is context.
5. Blob size ≠ resident weights for vision and elastic models; for dense
   text models the blob is sound. MoE and vision are excluded from the gate
   and from recommendations until handled explicitly.
6. On Apple Silicon, wired memory (`vm_stat`) tracks the model; `ioreg`'s
   "In use system memory" does not. The Vulkan sysfs counter under-reads for
   some models (part of the model in GTT).
7. Sliding-window architectures over-predict; step 5 reads
   `attention.sliding_window` and caps the KV term.
8. VRAM is never summed across devices: the budget is the largest single
   device, and unified memory is compared against the OS's stated GPU limit,
   not RAM.

**Consequences.**
- `estimate.Memory` exposes the terms separately so the Advanced view can
  show the arithmetic, and every constant lives in one exported config with
  the measurement that justifies it.
- The step 0 reports in `scripts/probe0/results/` are the calibration set;
  step 5's tests reproduce every dense row within the gate. Reports from the
  Windows/NVIDIA machine and the Mac Pro belong in that folder too.

## D-21. Values the code cannot read are unknown, never defaulted

**Decision.** Following probe0: a string field the detector could not read
is `"unknown"`; a numeric field is `0` with a sibling `*_known: false`; a
nullable column is `NULL`. The UI renders unknown as words ("could not read
how much memory the graphics card has"), never as `0 GB`.

**Consequences.**
- `hardware.Detect` in step 1 returns a profile that is unknown everywhere
  and a `Problems` list saying why; step 2 fills the values in and keeps the
  contract.
- An unknown GPU yields fit categories without speed numbers (step 5), and
  the UI says so — a missing number is a sentence, not a blank.

## D-22. Dependencies are few and named

*Superseded in part by D-26 (step 2 adds the YAML parser and `golang.org/x/sys`).*

**Decision.** Go: the standard library plus `modernc.org/sqlite`. UI: React,
React Router, and the Vite/Vitest/Testing Library toolchain. A new dependency
is a line in the PR description saying what it replaces and why writing it
would be worse.

**Consequences.**
- No HTTP framework, no ORM, no state-management library, no component
  library in the skeleton. Step 7 may add a component approach and says so
  in this file.
- YAML parsing for the catalogue (step 4) is the first expected addition;
  the candidates are `gopkg.in/yaml.v3` or `github.com/goccy/go-yaml`, both
  pure Go.

---

Step 2 (hardware detection, 2026-09-18) adds D-23 to D-26.

## D-23. Hardware detection reads the OS behind one seam, and every OS path runs on every runner

**Decision.** `internal/hardware` reads the machine through an `env`
interface (commands, a root `fs.FS`, environment, disk space, CPUID). The
real one shells out and reads files; the tests' one answers from fixtures —
one `testdata/<os>/<machine>.txtar` per machine, holding each tool's output
in its real format — so the Windows, macOS and Linux paths are all exercised
on every CI runner, and the parsers never need the tool they parse. Each
fixture machine also has a golden profile (`testdata/golden/*.json`) that is
the reviewable answer to "what does the advisor say about this machine".

The sources, per OS, and what is authoritative for what:

| | Windows | macOS | Linux |
|---|---|---|---|
| OS, CPU, RAM, form factor | one PowerShell query: CIM + registry (`-EncodedCommand`, no console window) | `sysctl`, `sw_vers`, `system_profiler -json` | `/etc/os-release`, `/proc`, `/sys/class/dmi/id/chassis_type`, sysfs CPU topology |
| GPU list | `Win32_VideoController` (present devices only) → its display-class registry key | `SPDisplaysDataType` | `/sys/bus/pci/devices` class 0x03, names from `pci.ids` |
| GPU memory | registry `HardwareInformation.qwMemorySize` (never `AdapterRAM`, which wraps at 4 GB) | `spdisplays_vram`; Apple Silicon: D-25 | amdgpu `mem_info_vram_total` |
| NVIDIA | `nvidia-smi` (memory, driver, compute capability, PCI id) overrides the registry | — | `nvidia-smi`, merged by PCI address |
| AMD LLVM target | from the name (D-24 table) | from the name | KFD topology `gfx_target_version` |
| AVX2 / AVX-512 | CPUID via `golang.org/x/sys/cpu` (x86); not applicable on ARM | same | same |

**Consequences.**
- Every value that could not be read is unknown with the reason in
  `Problems` (D-21). An unread device list is not "no GPU": the tier is then
  unknown, not "cpu only".
- The GPU list is ordered so the answer comes first: devices the runtime can
  use, discrete before integrated, most memory first. An iGPU beside a
  discrete card is listed and marked; two discrete cards produce a note that
  multi-GPU is not optimised in the MVP. The budget is the largest single
  device, never a sum (D-20, finding 8).
- Display-only devices (virtual and remote displays, BMC chips, VM adapters)
  are named in `filtered_adapters`, never counted as GPUs. A card with no
  vendor driver (Linux: nothing bound; Windows: Microsoft Basic Display
  Adapter) is still listed, from its PCI id, with what would unlock it.
- The Intel build under Rosetta and the x64 build on ARM Windows describe the
  machine, not the emulator (`arch` is arm64, with a note).
- `Tier` is one of unknown, cpu_only, integrated, gpu_small (< 7 GiB),
  gpu_medium (< 14), gpu_large (< 30), gpu_xl, sized by the best device's
  memory; the thresholds and their reasons are in `derive.go`. `Summary` is
  one sentence built from the profile; it is prose, the estimator decides fits.
- Detection runs in the background after the listener is up, so the browser
  never waits for `system_profiler` or PowerShell; `GET /api/hardware` waits
  for it. Each daemon start inserts a `hardware_profiles` row (with the
  daemon version, migration 0002) and never updates one. `Fingerprint`
  identifies hardware, not state — OS family, arch, CPU model, RAM and each
  GPU's PCI id and memory, rounded to the GiB — so a driver update is the same
  machine and a swapped GPU is a new configuration; old benchmarks keep
  pointing at the old row. `GET /api/hardware/history` lists configurations;
  `GET /api/hardware/profiles/{id}` returns any stored profile.

## D-24. The expected runtime path is data, checked against what Ollama ships

**Decision.** `data/hardware/runtime-support.yaml` holds ordered rules
(vendor, OS, arch, AMD LLVM target, NVIDIA compute capability and driver
floor, Linux kernel driver, "no driver", name patterns) → expected backend,
with a plain sentence, a source and a `checked` date per row; a table of AMD
marketing names → LLVM targets for the OSes that do not report one; and a
table classifying integrated versus discrete by name where the OS does not
say. Rows were checked against Ollama v0.34.2's own build presets
(`llama/server/CMakePresets.json`: the CUDA architectures and ROCm
`AMDGPU_TARGETS` each shipped build compiles) and its docs. Where the two
disagree — the Windows ROCm build compiles gfx1030 and RDNA 4, the docs list
only the RX 7000 series — the build wins ("as Ollama ships it") and the row
says so; step 3 records what actually happened. The file is embedded through
a small `advisor/data` package (`go:embed` cannot reach `../../data`), parsed
strictly (unknown keys are errors) and validated on load; the last rule must
be a catch-all, so no GPU leaves without an expectation, even if it is
"unknown".

**Consequences.**
- Updating for a new Ollama release is a data change reviewed like code; the
  contract tests (`support_test.go`) show every customer-visible answer that
  moves.
- A condition on a value the detector could not read does not hold, so
  unknown falls through to a later rule rather than matching by accident.
- Rules marked `actionable` (a missing or old driver) also become a note on
  the profile, because the fix is the user's to make.
- An OS below Ollama's floor (macOS 14, Windows 10 22H2) expects nothing on
  every GPU, and the tier is unknown with a sentence saying why.

## D-25. Apple Silicon's GPU budget is what macOS says, never a percentage

**Decision.** `gpu_usable_bytes` on Apple Silicon is `iogpu.wired_limit_mb`
when it is set (the OS enforces exactly that), otherwise Metal's
`recommendedMaxWorkingSetSize` — the platform default for that Mac's RAM and
macOS release, and the number Ollama itself schedules against
(`discover/gpu_info_darwin.m`). It is read by asking Metal through
`osascript -l JavaScript` (JavaScript for Automation can bind
`MTLCreateSystemDefaultDevice`), which every Mac has: no cgo, no compiler, no
Metal in the daemon's own process. If neither can be read, the budget is
unknown.

**Why not a table or a ratio.** The widely quoted rule (two-thirds of RAM up
to 36 GB, three-quarters above) does not hold on the fleet's M1 Pro under
macOS 26.6.2: Ollama logs Metal `total="11.8 GiB"` for 16 GiB (0.74). The
value is the OS's to choose and to change; reading it is the only way to be
right on the next release.

**Consequences.** Confirmed on the fleet's M1 Pro (2026-09-18,
`scripts/verify.command`): the Metal query answered 12,713,115,648 bytes
(11.84 GiB, 0.74 of RAM), the figure Ollama logs, and the M1 Pro fixture
carries that value. The other Apple Silicon fixtures' Metal values are
illustrative and say so; replace them when a machine of that size is at hand.
`scripts/verify.command` prints `/api/hardware` and stays the live check.

## D-26. Two dependencies: a YAML parser and golang.org/x/sys

**Decision.** `github.com/goccy/go-yaml` parses the data files (pure Go, no
dependencies of its own). It replaces the `gopkg.in/yaml.v3` D-22 expected: its
author labelled that project unmaintained in April 2025. Step 4's catalogue
loader uses the same parser. `golang.org/x/sys/cpu` reads AVX2 and AVX-512F
by CPUID, including the OS's support for the wider registers, the same way on
all three OSes; `x/sys` was already in the module graph through
`modernc.org/sqlite`, so this adds nothing to the build.

**Consequences.** No cgo, still; `go.sum` gains one module. A future data
file uses the same parser and strict decoding (`DisallowUnknownField`).

## D-27. The Backend interface is frozen against three runtimes, not one

**Decision.** `internal/backend.Backend` (`Name`, `Detect`, `Models`, `Show`,
`Running`, `Pull`, `Generate`, `Unload`, `Install`, `Start`) was checked
against Ollama's HTTP API, LM Studio's local REST API (`/api/v1/models`,
`/models/load`, `/models/unload`, `/models/download`) and its `lms` CLI, and
llama-server's API (`/props`, `/v1/models`, `/slots`, and its newer
`--models-dir` router mode) before the Ollama adapter was written, not after
— D-3 asked for the interface to be defined once, for every runtime the PRD
names, and getting that wrong is a rewrite, not an edit, once a second
backend exists. The seam D-3 called out as most likely to leak an Ollama
assumption — what a model *is* to fetch — is `Pull`'s `ModelSource`: a
`SourceKind` (`ollama_tag` or `huggingface_gguf`) with only the fields for
that kind set, and `ErrUnsupportedSource` when a backend cannot fetch from a
kind, never a best-effort translation between an Ollama tag and a Hugging
Face repo. `Generate` stays deliberately thin (no chat history, tools,
images — D-4) because its only caller is the benchmark harness (step 6).
`ModelInfo.Details` carries a model's GGUF metadata with the architecture-
prefixed keys the file format itself uses (`<arch>.block_count`,
`<arch>.attention.key_length`, …), not Ollama's shape — Ollama's `/api/show`
already re-exposes those keys close to verbatim, so this is reading the
file format, not the runtime.

**Consequences.** `internal/backend/ollama` is the only implementation; nothing
in the interface references Ollama's Modelfile concept except the two fields
(`Modelfile`, `Template`) that only Ollama populates, left empty by any
backend without the concept. A llama.cpp or LM Studio adapter (step 4/18)
implements the same ten methods against its own API with no interface
change expected — the open item this closes.

## D-28. The Ollama adapter: HTTP first, filesystem as fallback only

**Decision.** `internal/backend/ollama` talks to Ollama over its HTTP API on
`OLLAMA_HOST` (default `http://127.0.0.1:11434`). `Detect` tries
`/api/version` first — the only way to learn the running version — and only
falls back to looking for the binary on `$PATH` or this app's managed
install location when that fails; the filesystem check never starts
anything, matching the interface's "Detect must be cheap and must never
start anything." `Pull` streams `/api/pull` and reports progress in bytes
(`completed`/`total` from Ollama's own NDJSON), so the UI can say "2.1 of
4.7 GB" per the task, not a percentage with no denominator until Ollama has
sized the download. `Unload` is a `/api/generate` call with `keep_alive: 0`
and no prompt — Ollama has no dedicated unload endpoint; that is the
documented way to free a model's memory now. The HTTP client, the wire
shapes, and the `model_info` parsing helpers (`miValue`/`toNum`, D-20's
"prefer `attention.key_length`, max over a per-layer array") are ported from
`scripts/probe0/main.go`, which validated them against real machines across
three runtime backends in step 0 — not re-derived from the docs alone.

**Consequences.** A `TestShowPrefersKeyLengthOverSpec` regression test keeps
D-20's finding enforced at the adapter boundary, not only in the estimator
that will consume it (step 5). `Detect`'s 2-second timeout means a stalled
Ollama process reads as unreachable quickly rather than hanging a page load.

## D-29. Install and Start: a button, per OS, no sudo, no system service

**Decision.** Per product rule 5, `Install` and `Start` only ever run from a
UI action that already said what it would do; the methods themselves change
nothing until called. macOS and Windows: download the official installer
over HTTPS to a temp folder with progress, verify what can be verified (the
host, TLS, a published checksum where Ollama publishes one — it does not,
today, and the code says so rather than pretending to check one), then
launch it and wait for `Detect` to change; the user still clicks through the
installer, because this app cannot and should not silently accept another
program's EULA for them. Linux: a genuine user-space install — the official
`ollama-linux-<arch>.tar.zst` extracted under this app's own data folder,
run later as a child process this daemon supervises (`ollama serve`,
output to a log file `runtimepath.go` also reads) — no `sudo`, no
`curl | sh`, no system service, matching the Linux convention CLAUDE.md
already sets. Both the version this app installed and the version `Detect`
subsequently reports are kept (`backends.installed_version` vs. the row's
`version`), so "what we put there" and "what is actually running" can
disagree visibly if the user upgrades or replaces it themselves.

**Consequences.** The user-space Linux install cannot start at boot or run
as a background service outside this app — a system service would need a
password prompt, and product rule 1 ("the user never needs a terminal") and
this product's customer (CLAUDE.md: someone who "has no idea what a GGUF
is") make never asking for a password the higher priority. `Install`'s
progress callback and `Pull`'s share the same shape (`Status`, `Completed`,
`Total` bytes) so the UI can reuse one progress-bar component for both.

## D-30. The inventory: four states, refreshed on every check, never deleted

**Decision.** `backend.State` has four values — `not_installed`,
`installed_not_running`, `running`, `unsupported` — so "installed but not
running" and "not installed" are different values the API returns, not a
boolean the UI has to combine with a guess (the task's own requirement).
`GET /api/backends` calls `Detect` (and, when a backend is running,
`Models`) fresh on every request rather than serving a cached value: `Detect`
is documented to be cheap and to never start anything, so re-checking on
every page load is the point. Each check is stored as a new `backends` row
(insert-only, mirroring `hardware_profiles` — D-23's history-not-update
shape), so a benchmark can still answer "what version of Ollama was this
run against" after an upgrade. `installed_models` is upserted the same way
on every successful `Models()` call: existing rows for that backend are
marked `present = 0`, then every model the runtime reports is inserted or
updated back to `present = 1` — a model the user removed from Ollama drops
out of `GET /api/models/installed` but its row, and its name, stay
referenceable by history (a past benchmark, a past estimate), never deleted.

**Consequences.** `GET /api/backends` on a `Detect` failure (a real error,
not "not installed") falls back to the last stored row rather than
reporting a guessed state — D-21 applies to a runtime's state exactly as it
does to a hardware value. `GET /api/models/installed?backend=<name>` filters
to one runtime; without the parameter it spans all of them, backend then
name ordered.

## D-31. The runtime path is a fact established after a load, never assumed

**Decision.** `hardware.RuntimePath` per GPU index on a backend's `Status`
(`cuda`/`metal`/`rocm`/`vulkan`/`cpu`) is empty until a model has actually
loaded — it is what happened, not `hardware.GPU.ExpectedBackend` (D-24's
rule-derived expectation) repeated in a new place. After a load, it is read
two ways and only trusted when they agree with what evidence exists:
`/api/ps`'s `size_vram` says whether anything is on a GPU at all (0 means
CPU, unambiguously), and the server's own log — read from the file this
daemon wrote when it started Ollama itself, or Ollama's documented default
location otherwise — is parsed for its device-discovery lines
(`library=cuda`, `ggml_vulkan: Found …`, `no compatible GPUs were
discovered`, …; the last matching line wins, since a failed probe of one
backend followed by a successful one is a real sequence Ollama's own log
produces). When VRAM is used but the log cannot confirm which named backend
did it, the path is left out of the map entirely — D-21 again: an omitted
entry, never a guessed one. The environment variables that steer which path
a runtime takes (`OLLAMA_VULKAN`, `GGML_VK_VISIBLE_DEVICES`,
`HSA_OVERRIDE_GFX_VERSION`) are captured on every `Detect` — read, never
set; this app has no UI to set them yet, and D-21 forbids guessing a good
value even if it did — and only variables actually present in the
environment are included.

**Consequences.** `GET /api/backends` returns `runtime_paths` beside
`expected_backend` (on the hardware profile), so the UI can show "expected
CUDA, actually ran on CUDA" or flag a mismatch, once step 11 builds that
screen. The log-parsing logic (`pathFromLog`, `runtimePathsFromPS`) is
ported from `scripts/probe0/main.go`'s validated detector, not re-derived.

---

---

Step 4 (the model catalogue, 2026-09-19) adds D-32 to D-37.

## D-32. The catalogue's schema: sizes carry what the estimator cannot guess

**Decision.** `families.yaml` keeps the shape step 1 sketched (family: id,
display name, maintainer, licence, purposes, reviewed date; size: parameters,
context length, Ollama tag, Hugging Face repo) and adds four things:

- a top-level `quants` list — the quant variants the advisor tracks
  (`Q3_K_M`, `IQ4_XS`, `Q4_K_M`, `Q5_K_M`, `Q6_K`, `Q8_0`, and `MXFP4` for
  models released in it). A repo publishes twenty-odd quants; a beginner is
  choosing between a handful, and every tracked quant is a header read;
- `source` per family — the model card the entry was checked against,
  beside `reviewed_at` (CLAUDE.md: data carries its date and its source);
- `active_parameters` per size — set for mixture-of-experts sizes and for
  Gemma's per-layer-embedding "E" sizes, omitted for dense ones;
- `notes` per family for the curator and for step 5 (hybrid attention,
  multi-head latent attention, sliding windows).

Mixture-of-experts and multimodal families are **in** the catalogue. The
step 1 header comment excluded them "until step 5 handles them"; that is
superseded here, because a careful person recommending local models in
September 2026 recommends several of them first, and the estimator cannot
learn to handle models the catalogue does not carry. They are marked
(`active_parameters`, purpose `vision`, the header's `expert_count`,
`full_attention_interval`, projector files), so step 5 can decide per
model, and D-20 finding 5 still keeps them out of its gate until it does.
The Llama 3 sizes stay although they are older than the rest: they are the
most-installed family and the models step 0 calibrated on.

`hf_repo` is an ungated repo publishing the tracked quants under llama.cpp's
standard names — bartowski's where one exists, else ggml-org or the
maintainer's own — with the vision encoder as an `mmproj` file. `load.go`
validates everything it can offline (`advisor catalog check`); whether each
repo resolves is the refresh's report.

**Consequences.** Adding a model is a YAML edit plus `advisor catalog
refresh`. The purpose enum is checked against `ui/src/api/types.ts` by a
test, and a test fails if any of the docs counts the catalogue (rule 8).

## D-33. A header read stops at the tokenizer

*Superseded in part by D-37: `general.file_type` is no longer a required
key, because the live gate showed current quantizers write it last.*

**Decision.** `internal/catalog/gguf` parses a GGUF header from any
`io.Reader`: magic, version (2 and 3; version 1 and big-endian files are
refused with a sentence), tensor count, then the key/value metadata in
order. `tokenizer.*` values are skipped, never stored. With
`StopAtTokenizer` the parse ends at the first `tokenizer.` key once every
required field (architecture, file type, block count, context length,
embedding length, head count, KV head count) has been read — llama.cpp's
writer puts all of them first, so a header read is one 64 KiB range of a
multi-gigabyte file. When a writer put a required key after the tokenizer
(Ollama's own files put `general.file_type` last), the parse reads on to
the end rather than stop without it: same answer, more bytes.

The tokenizer is most of a header — 6 to 11 MB of vocabulary and merges —
and none of the estimator's inputs. Reading every tracked quant's full
header would cost well over a gigabyte per first refresh on the customer's
connection; stopping costs about ten megabytes for the whole catalogue.

A header that is not GGUF fails loudly (`*gguf.FormatError`, wrapping
`ErrMalformed`, with the byte offset and key); one that is merely cut short
fails with `ErrTruncated`. Limits no real file reaches (64 Ki metadata
pairs, 64 MiB strings, 64 Mi-element arrays, 4 Mi tensors) keep a hostile
header from asking for gigabytes. The typed view (`Metadata`) takes the
largest value of a per-layer array (probe0's convention) and keeps the
list, uses `head_count` when `head_count_kv` is absent — llama.cpp's rule,
flagged `head_count_kv_stated: false` — and keeps `key_length` (D-20).

**Consequences.** Tests parse real headers captured from the fleet (the
first megabytes of Ollama blobs, `gguf/testdata/`), including one whose
file type comes after the tokenizer and one hybrid architecture, plus
synthetic malformed ones. `catalog_files.header_complete` says which kind
of read each row came from; `header_json` holds every pair that was kept.

## D-34. The Hugging Face client is polite, header-only and cached

**Decision.** `internal/catalog/hf` makes two kinds of request. The
model-info listing (`/api/models/{repo}?blobs=true`: files, sizes, LFS
hashes, the Hub's own parameter count) is sent with `If-None-Match` and its
body cached per repo (`hf_listing_cache`), so an unchanged repo is a 304.
Header reads go to `/{repo}/resolve/{commit}/{file}` — pinned to the
listing's commit — as range requests from byte 0 in doubling chunks
(64 KiB up to 8 MiB) that the parser consumes as they arrive. A server that
answers a range with the whole file is refused unread (unless the file is
smaller than the range asked for). Parsed headers are cached by (repo,
file, LFS sha256) in `hf_header_cache`: a file's content hash, not the
repo commit, so a README edit costs nothing.

Requests are sequential with a minimum gap; 429 and 5xx honour
`Retry-After` or the Hub's `RateLimit` header (`t=`), up to a bounded wait
and a bounded number of attempts; a longer requested wait fails with
`ErrRateLimited` rather than stall. Redirects are followed only to
`huggingface.co`, `hf.co` and their subdomains (the LFS and Xet CDNs), over
https. When every attempt fails below HTTP, the error is `ErrUnreachable`
and the refresh stops instead of failing every size slowly. No token is
ever sent: a gated repo is a curator error ("pick an ungated repo"). The
User-Agent is `local-llm-advisor/<version>`.

**Consequences.** A second refresh of an unchanged catalogue makes one
request per repo and reads nothing. The host list lives in
`hf.allowedHost`, which step 12's allow-list audit reads.

## D-35. Refresh: one row per tracked quant, never deleted, one at a time

**Decision.** `internal/catalog/refresh.Run` syncs the YAML into
`catalog_models` (one row per family size; sizes that left the YAML are
marked `present = 0`), then per size: listing → files grouped (split
`-0000N-of-0000M` parts summed, part 1's header read; imatrix, multi-token
prediction and draft files ignored) → one weights file per tracked quant and
one vision encoder (F16 preferred) → headers → `catalog_files`. Migration
0003 adds the columns step 5 needs beyond schema v0 (`value_length`,
`full_attention_interval`, `expert_used_count`, `head_count_kv_stated`),
the file's `role` (`model` | `projector`) and `parts`, `present` flags on
both tables, and the size's refresh state (`hf_sha`, `parameters_counted`,
`refreshed_at`, `refresh_error`). `bits_per_weight` is bytes × 8 over the
Hub's own parameter count when it gives one, else the model card's.
`file_type` is −1 when a header does not state it (0 is F32). A size that
fails keeps what an earlier refresh stored; its error is recorded in
words. Disagreements that do not stop a size — the YAML's context length
against the header's, a file named `Q8_0` whose header says `Q4_K_M` — are
warnings in the report. Each refresh is a `catalog_refreshes` row.

It runs from `advisor catalog refresh` (the curator's command; exits 1 if a
size did not resolve) and `POST /api/catalog/refresh`, one at a time (a
second gets 409), detached from the request so closing the tab does not
abandon it. The daemon syncs the YAML at every start (no network), so a new
build's catalogue exists before its first refresh. Step 10 calls the same
`Run` nightly.

**Dependency direction.** `store` imports `catalog` for its types (as it
does `hardware` and `backend`); `catalog/refresh` imports `store`, `hf` and
`gguf`; `catalog` itself imports neither `store` nor `hf`.

## D-36. Installed models map by family, size and quant; unknown is a signal

**Decision.** `catalog.MatchInstalled` maps an installed model onto the
catalogue: a Hugging Face pull (`hf.co/owner/repo:quant`) by its repo; else
the exact Ollama tag; else the library name before the colon as the family
and the size whose parameter count is nearest the runtime's
`parameter_size` (within 25%) — which is what makes `llama3.2:latest` and
`qwen3.5:9b-q8_0` land on the right size — and then the quant picks the
file. The result is `file` (known size and quant), `model` (known size, a
quant the catalogue does not track or has not read yet) or `unknown`, with a
sentence for the curator, stored on `installed_models` (`catalog_model_id`,
`catalog_file_id`, `catalog_match`, `catalog_note`). It runs after every
inventory refresh and every catalogue refresh, costs no network, and
`GET /api/catalog/unknown` lists the unknown ones — the curator's signal,
never an error (D-7).


## D-37. The file type is not worth the tokenizer (the gate's finding)

**Decision.** `general.file_type` leaves `gguf.RequiredKeys`. The first live
refresh (the M1 Pro, 2026-09-19: 22 of 22 sizes resolved, 139 files) read
1.5 GB of headers instead of the ~10 MB D-33 predicted: current llama.cpp
quantizers write `general.file_type` and `general.quantization_version`
after the tokenizer (setting a key removes it and appends it at the end), so
nearly every file failed D-33's early-stop condition and was read through
6–11 MB of vocabulary. The file type is a cross-check, not an estimator
input: the quant a user sees and pulls is the one in the file name, and the
bytes come from the listing. A read now stops at the tokenizer once the
structural keys are in (architecture, block count, context length,
embedding length, head count, KV head count — all written before the
tokenizer); a file type stated earlier is kept and checked against the name,
one stated later is not read and is `file_type = -1` ("not stated", D-21).
A required key after the tokenizer still makes the read go on.

**Consequences.** A first refresh of the whole catalogue is one 64 KiB range
per file again (about 10 MB); headers already cached by a full read stay
valid. The name-versus-header warning now fires only for files that state
their type early. The fixture whose writer puts the type last
(`minicpm-v4.6`) is the test.

---

Step 5 (fit estimator + recommendation engine, 2026-09-19) adds D-38 to D-43.

## D-38. The memory estimate is D-20's formula, generalised through a layer layout

**Decision.** `estimate.Fit` computes `weights + KV + overhead` exactly as
D-20 states it, and `internal/estimate/step0_test.go` replays every step 0
row through it: the recorded result is pinned (27 of 28 dense rows within
15%, worst row −20.8%), so a change to the formula is a change to D-20, not
a refactor. What D-20 could not cover is most of the catalogue: models whose
layers are not a plain stack of identical attention layers. `catalog.Layout`
(`internal/catalog/layout.go`) describes what each layer keeps as the
context grows, and the cache term sums over it:

- hybrid models keep a cache on some layers and a small fixed state (kept in
  32-bit floats) on the rest — `full_attention_interval`, a per-layer
  `recurrent_layers` list, or per-layer `head_count_kv` with zeros;
- sliding-window layers stop growing at `window + n_ubatch` cells, rounded up
  to 256 — per-layer `sliding_window_pattern` where the header states one
  (with `key_length_swa` for the shorter sliding heads), else the period
  llama.cpp hard-codes for the architecture;
- the last `shared_kv_layers` layers own no cache; `nextn_predict_layers`
  appended to a file are not run and keep nothing (this is the "block_count
  is one more than the model card" of step 4's gate);
- multi-head latent attention (`key_length_mla` present) caches one
  compressed key per token and no value;
- keys and values may differ in length: the term is `key_length +
  value_length`, which is D-20's `2 · head_dim` wherever they are equal.

Every rule follows llama.cpp's own loader and cache code (checked against
ggml-org/llama.cpp 5b59b83, 2026-09-19) — the runtime's arithmetic, not a
guess at it. A Layout is **derived on every read** from the typed columns
plus `header_json`, never stored, so a better reading of a header needs no
catalogue refresh and the rows a step 4 build wrote serve as they are. Its
`Basis` says how far it can be trusted: `uniform` (the shape step 0
measured), `stated`, `architecture` (the header states a window and the
pattern is the one llama.cpp hard-codes for the architecture), or
`incomplete` (something the layout needs is missing; what could be read is
used and the note says what could not). An architecture llama.cpp has no
sliding-window pattern for is run with full attention whatever window its
header mentions — step 0's phi3 rows — so the plain formula is exact there.

Around the formula: a vision encoder's bytes are added to the weights (the
runtime loads it beside them; step 0's one multimodal model had it
resident); Gemma's per-layer-embedding sizes keep their lookup tables in
system memory by design, so only `bytes × active ÷ total` counts against the
graphics memory and the rest is reported as living off the card without
making the model "split"; a mixture of experts needs every expert resident.
A quantised cache uses GGML's block sizes (34/32 and 18/32 bytes an element).

**The five categories, and the threshold that decided.** Against the
placement's budget (D-39): at or under `HeadroomFraction` (80%) fits with
headroom; at or under `FitsFraction` (92%) fits; over that, a shorter context
from the ladder that fits makes it `reduced_context_only` (with
`suggested_ctx`) — looked for *before* splitting, because a shorter context
keeps the whole model where it runs fastest; otherwise whole layers go to the
graphics until it is full and the rest must fit in RAM less the OS reserve
(`needs_cpu_offload`), else `not_recommended`. `Estimate.Threshold` is the
comparison in words. A sixth value, `unknown`, exists for a budget that
could not be read (D-21): never a fit, never a misfit.

**Every constant is in `estimate.Config`**, each marked MEASURED (fleet),
MEASURED (public) or CHOSEN, with what would settle the chosen ones. The
overheads are step 0's medians. The two fractions, the OS reserve and the
processor's speed range are CHOSEN and say so.

**Evidence beyond the gate.** Step 0 measured one hybrid model on Metal and
Vulkan and excluded it as multimodal; with the layout its four rows land
within 10% (with every layer counted the long-context rows over-predict by
40%), and the test holds them to 15%. Its Gemma rows on CUDA grow by 17 KB
per token of context, against 16 KB for a shared-cache, mostly-sliding
layout. That is support, not validation: `Basis.MemoryModel` is `validated`
only for the shape inside step 0's gate, `modelled` for everything above,
and the confidence (D-42) follows it.

## D-39. Placement: which memory, which path, and what the runtime was seen to do

**Decision.** `Estimator.Place` decides once per machine where a model would
run and what it is compared against, so Fit and the engine cannot disagree:
Apple Silicon against `gpu_usable_bytes`, never RAM (D-25); a graphics card
against the largest single device (D-20 finding 8); a machine whose models
run on the processor against RAM less `Config.OSReserve`; graphics built
into a processor against system memory too, at that memory's speed, and
without being called "a graphics card that is not used". The runtime path is
the one the backend **established** after a load (D-31) when there is one,
else the rule-derived expectation (D-24), and the estimate says which
(`Basis.PathSource`).

When a graphics card exists and the plan is nevertheless for the processor,
`Placement.Unused` says which of four things is true — seen running on the
processor, cannot be used as set up, not known yet, memory unreadable — with
the why as far as the facts go: the support rule's own sentence, Vulkan
switched off when `OLLAMA_VULKAN` says so, otherwise the usual causes named
as such. An operating system older than the runtime supports blocks
everything and the result carries the profile's sentence.

An observation outlives the load: most backend checks see no model loaded
and record no path, so the engine plans against the **last check that saw
one, on this hardware**. Migration 0004 adds `backends.hardware_fingerprint`
for that — what Ollama did with the old graphics card says nothing about the
new one, and timestamps at one-second resolution cannot tell a swap from a
restart. `GET /api/recommend` and the fit endpoint re-check the runtimes
first (Detect is cheap and starts nothing — D-30), so a model loaded a minute
ago in the user's chat app is what the answer is built on.

## D-40. Speed is a range, from memory bandwidth, and absent when the part is unknown

**Decision.** `generation tok/s ≈ bandwidth ÷ bytes read per token ×
efficiency`, both ends estimated: the high end reads only the weights a token
uses (an empty context), the low end adds the whole cache at the context
asked for (a full one). Prompt processing is estimated separately — it is
bound by arithmetic, so it is the part's generation speed on the reference
model scaled to this model's active parameters, times the path's measured
prompt-to-generation ratio. `estimate.Speed` carries both as `*figure.Rate`
with `Low < High`; when the part is not in the table they are **absent** and
`Unknown` is the sentence — a missing number is a sentence, never a zero and
never a default. `WithMeasurement` turns a rate into a measured point and
flips its source: product rule 4's second sentence, ready for step 6.

Bandwidth is data: `data/hardware/gpus.yaml`, embedded and strictly decoded
like the other data files. Graphics rows carry vendor, name patterns,
optional memory-size and GPU-core conditions (the same name ships with
different memory), the vendor's bandwidth, and — for GDDR parts — the data
rate and bus width it is derived from; the parser refuses a row whose
bandwidth is not `rate × width ÷ 8`, so a typo cannot survive. Rows are
ordered (laptop parts above desktop parts of the same name); a name that
lists several cards, which is how Linux's pci.ids names a chip id, takes the
slowest of every matching row and says so. `system_memory` rows give a
processor family's supported memory as a **range** — one module of the
slowest supported speed to every channel at the fastest — because the
advisor does not read what is installed; that alone makes the processor's
estimate the widest.

Efficiency and prompt ratio are ranges keyed by the runtime path
(`estimate.Config.Paths`), Vulkan refined by vendor because its three
populations disagree. As shipped they come from llama.cpp's own llama-bench
scoreboards (read 2026-09-19) and each entry's `Basis` lists the figures;
parts a scoreboard shows outside their path's range get a per-row override
in gpus.yaml with its source (the two-die Apple chips, the Max chips, an HBM
card). Mixture-of-experts models read `bytes × active ÷ total` per token and
are scaled by a measured factor. The processor's range is CHOSEN — nothing
public covers it — and is the first thing `scripts/calibrate` exists to
replace: it turns a llama-bench run on a fleet machine into the same two
factors, says whether they fall inside the configured range, and writes a
results file to commit. It is the dev-side instrument; the customer's
measurement is step 6's benchmark of Ollama itself.

## D-41. Recommendation is a product of four factors over the default download

**Decision.** `score = purposeFit^wP × fitFactor^wF × speedFactor^wS ×
sizeFactor^wZ`, every weight and threshold in `recommend.Config`. A product,
so a zero anywhere is a veto.

- *Purpose fit* reads the order of a family's `purposes` in families.yaml —
  most credible first, a gentle step per rank (the file's header now says the
  order is read) — averaged over the purposes asked for, zero for a purpose
  the machine cannot serve (long documents at a short context, images without
  an encoder), and scaled down when the context has to be shorter than the
  purpose wants. The engine chooses the context: the longest on the ladder,
  up to what the purposes want, that still fits wholly.
- *Fit* is the category's worth. Models that only run split compete only
  when nothing fits at all (or `allow_split` is asked for): a beginner is not
  steered to a model several times slower while one that fits exists.
- *Speed* is the geometric middle of the estimated range over a comfortable
  reading speed, capped at 1, falling all the way down below it and weighted
  1.5 — which is what sends small models to weak hardware (product rule 6)
  instead of the largest model that happens to fit in RAM.
- *Size* is the only quality proxy the catalogue carries until step 9b: log
  of effective parameters (total; √(total × active) for experts; the model
  card's effective count for per-layer-embedding sizes). It is what keeps a
  24 GB card from being handed a 1B model.

The engine recommends **one file per size: the one its Ollama tag pulls**
(`DefaultQuants`). The Ollama adapter cannot pull another quant yet, and
choosing quants automatically is PRD §18. At most three cards, one per
family. Every sentence on a card is templated from the facts the rules used
(`reasons.go`, D-8), in a fixed order — the graphics card that is not part of
the numbers first, with its explainer id and the why; then fit, purpose,
speed, cost, change — and a test holds the copy rule: none of the glossary's
terms reaches a reason.

If the user has a model, the one that serves the purposes best (or the one
named) is assessed like a candidate, and a recommendation must name a
noticeable change against it — size, speed, a purpose its family is not for,
a context at least twice as long, fitting where it does not — or be dropped;
the model itself is never recommended to its owner, and an installed model
that is a real change costs "nothing to download". When the list is empty
the result says why in words and as a code (`empty_code`); the UI offers the
one action that fixes it — fetching the model list, with its cost on the
button.

**Consequences.** `internal/recommend/recommend_test.go` holds the outcomes
the weights have to produce on the golden hardware profiles — the fleet's
top picks among them. A change to a weight is judged by those outcomes, and
by Itay reading `advisor recommend` on each machine.

## D-42. Confidence is derived, three-valued, and says what limits it

**Decision.** From `estimate.Basis`: **low** when something is unknown (no
speed estimate; a model description the memory arithmetic could not read in
full); **high** when the memory arithmetic has been measured
(step 0's shape, or a benchmark of this configuration), the runtime has been
seen taking this path, and the speed is measured or is an estimate on a path
whose parts behave alike (cuda, metal); **medium** otherwise — all inputs
known, at least one only expected, modelled or wide. `confidence_why` names
the limiting inputs in plain words and the UI shows both on every card. Most
of today's catalogue is therefore medium at best until step 6 measures it,
which is the honest reading of PRD §21's last risk.

## D-43. The API for step 5, and what is deliberately not stored yet

**Decision.** `GET /api/recommend?purposes=a,b[&current=][&min_context=]
[&gpu_only=1][&allow_split=1]` → `recommend.Result`; no purposes means
everyday chat. `GET /api/models/{id}/fit[?ctx=][&kv=]` → one estimate per
tracked weights file of a catalogue size, `{id}` being the size's id in
`GET /api/catalog`; without `ctx` the context is what Ollama itself would
use on this machine (4k, 32k or 256k by graphics memory — its
`server/routes.go`), and the response says which. Both wait for hardware
detection like `GET /api/hardware`. `advisor recommend` prints a running
daemon's answer as text for the developer; `scripts/verify.command` and CI
call it.

Estimates are computed per request and **not written to `estimates`**: the
table's rows exist to be flipped to `measured`, and writing on a GET would
make every page view a write. Step 6 owns the row: it inserts or updates it
when a benchmark completes, and passes what it measured to the engine through
`Engine.Measurements` / `Estimate.WithMeasurement`.

**Dependency direction** (D-11, made concrete): `estimate` imports `catalog`,
`hardware`, `figure` and `data`; `recommend` imports `estimate`; `server`
imports both; `scripts/calibrate` imports `estimate`. `hardware` gains exported
helpers (`MatchName`, `HumanGB`, `PrimaryGPU`, `AppleGPUCores`) and no
dependency.

---

Step 6 (benchmark harness, 2026-09-19) adds D-44 to D-49.

## D-44. The suite is data: the advisor's own text, sent raw, versioned and pinned

**Decision.** `data/bench/suite.yaml` (schema in its header, mirrored by
`bench.Suite`, decoded strictly) names the text (`text.txt`: original
English prose written for the suite — an allotment year, no markup, plain
ASCII so every tokenizer sees the same bytes), three prompts cut from it by
paragraph count (≈ 475, 2,047 and 7,408 tokens with Llama 3's tokenizer — a
reference count; each model's own count comes back from the runtime and is
kept), the answer's budget (256), temperature 0, a fixed seed, one warm-up
and three timed requests per prompt. Nothing the user typed is ever sent
(product rule 7): the harness has no input for text at all.

How a request is sent, and why (checked against Ollama v0.34.2's source,
`server/routes.go` and `llm/llama_server.go`, and the llama.cpp it ships,
b10969):

- **raw**: no chat template, no system prompt. Templates differ per model and
  would add a different number of tokens to each; raw times the model on the
  suite's text and nothing else. The text ends mid-essay, so the model
  continues the prose and uses the whole budget.
- **a numbered lead line** (`"{n}."`, n = the request's number in the run):
  Ollama runs llama-server with `cache_prompt: true`, which reuses the prefix
  a request shares with the previous one on the same slot — three identical
  prompts in a row would time one token of reading. The number makes every
  request differ at its first token; what is still reused (the begin-of-text
  token) is reported as `prompt_eval_cached_count` and left out of the rate.
- **`truncate: false`, `shift: false`**: with truncation on, Ollama cuts a
  prompt longer than the context without saying so, and the harness would time
  a different prompt. Off, the runtime refuses it, and the prompt is skipped
  with the runtime's words as the reason.
- Before a run, a prompt whose reference count plus the answer plus a margin
  (`bench.Config.ContextMargin`) exceeds the context is planned out; after the
  warm-up, the model's own tokens-per-word ratio re-checks the rest.

The suite's version is part of every run's comparability key (D-46), and
`suite_test.go` pins each version to a digest of the YAML and the text: an
edit without a version bump fails the build.

**Consequences.** At Ollama's default context on machines under 23 GiB of
graphics memory (4k), the long prompt does not run; a run at 8,192 or more
runs all three. The prompt lengths are a reference, not a promise, and the
results carry each model's own counts.

## D-45. A run: one at a time, timed by the runtime, and a cancel that unloads

**Decision.** `bench.Harness` runs one benchmark at a time (a second request
is 409, never queued), detached from the HTTP request that started it —
closing the page does not stop it; cancel does. A run is:

1. **prepare** — if this model is loaded (by the user's chat app, at another
   context) it is unloaded; other loaded models are noted on the run; after
   `Config.Settle` the sampler (D-47) takes its baseline;
2. **warm-up** — the first prompt, untimed: the load and everything done
   once. Its `load_duration` is kept as the run's load time;
3. **observe the load** — what the runtime said about it (D-46), where it put
   the model (`/api/ps` size vs size_vram → gpu, split or cpu; the log's
   "offloaded N/M layers" wins when the two disagree), and a fresh `Detect`,
   recorded in `backends` like any check, so the path a benchmark's load
   established is what the recommendation engine plans against next (D-39);
4. **timed requests** — three per prompt; generation = `eval_count /
   eval_duration`, prompt = `(prompt_eval_count − cached) /
   prompt_eval_duration`, both Ollama's own counters; time to first token at
   the client, request sent to the first streamed chunk of answer (or of
   reasoning — it is output too). Each prompt's result is the median and the
   spread, (max − min) ÷ median, with notes when the runs disagree by more
   than 5% (the gate's tolerance), the answer stopped early, or the cache was
   reused;
5. **unload** — with a context of its own, so a cancelled run still does it:
   ask, then poll `/api/ps` until the model is absent on two readings in a
   row, asking again whenever it reappears — a load still in progress when a
   run is cancelled finishes and shows up after the first request. The run
   records `unloaded` true, or false with why.

The headline of a run is the shortest prompt's result: a short prompt and an
almost empty cache are what the estimator's speed figures describe
(llama-bench's 512-token prompt). A daemon that stops mid-run leaves a row
that says "running"; the next start marks it failed (`Recover`).

**The backend interface grows by two fields and one optional interface**
(D-27 said `Generate` stays thin; it does): `GenerateRequest.Raw` and
`NoTruncate`, which every runtime D-27 checked has (llama-server's
`/completion`, LM Studio's `/v1/completions`), and `GenerateEvent.Thinking`
and `PromptEvalCached`. `backend.LoadObserver` is optional: a backend that
can read its own load (Ollama: the server log) implements it; one that
cannot leaves the run's path, cache type and flash attention unknown.

## D-46. What makes two runs comparable, read from the runtime, never assumed

**Decision.** `bench.RunConfig` is the whole context of a run, and
`RunConfig.Key()` is a digest of the part that decides comparability: the
hardware fingerprint, the backend and its version, the runtime path the load
took, the model's digest (else name, quant and size), the context asked for,
the cache type, flash attention (unknown is its own value), the parallel
slots, and the suite's version and digest. The daemon's version is stored,
not keyed: what the harness does to time a request belongs to the suite's
version. Runs are compared — "−0.4% against run 12" — only when their keys
are equal; a vulkan run and a rocm run on one card are two configurations.

The runtime path, the cache type, flash attention, the layers offloaded and
the context the runtime was started with are **read** from the runtime's own
account of the load: Ollama v0.34 runs llama-server with `--log-verbosity 4`,
so its load lines reach Ollama's server log — `offloaded N/M layers to GPU`,
`<device> model buffer size` (CUDA0, ROCm0, MTL0, Vulkan0, CPU_Mapped: the
device every part of the weights went to), `flash_attn = auto` and
`resolve_fused_ops: Flash Attention enabled` (auto alone decides nothing),
`llama_kv_cache: … K (f16) … V (f16)`, and Ollama's `starting llama-server
… -c N -np P`. `ObserveLoad` marks the log's length before the warm-up and
reads only what follows; on Linux under systemd it reads the journal from
the same moment. Where nothing can be read, each value is `unknown` (D-21):
the run is stored and compared as unknown, and does not replace an estimate
(D-48). `pathFromLog` (D-31) learned the model-buffer lines too, and — when
`/api/ps` already says the model is in graphics memory — ignores the
processor's lines, which builds that load backends as libraries print last.

**Consequences.** Migration 0005 adds what schema v0 lacked:
`hardware_fingerprint`, `model_digest`, `flash_attention_known`,
`config_key`, `config_json` (the whole RunConfig and the run-level figures),
`model_json` (the header facts the estimator used, D-48), `estimate_json`
(the estimate the run is shown against), `notes_json`, `resident`, the
runtime's own `ps_size_bytes` / `ps_size_vram_bytes` (its estimate, kept as
context and as evidence for where "fits" ends — D-20 finding 4),
`effective_ctx`, `memory_source`, `unloaded`, `measure_anyway`, and a
`device` column on samples.

## D-47. The resource sampler: the counters that exist, without root, and words where none do

**Decision.** Once a second (`bench.Config.SampleInterval`) every probe the
machine has reads its tool, through a `sysEnv` seam the tests answer from
fixtures in each tool's real format (`internal/bench/testdata/sampler/`):

| | graphics memory (the footprint's counter) | also |
|---|---|---|
| NVIDIA (Windows, Linux) | `nvidia-smi` memory.used, summed over cards | utilisation, temperature, power |
| AMD on Linux | amdgpu sysfs `mem_info_vram_used` + `mem_info_gtt_used` (D-20 finding 6: part of a Vulkan model lands in GTT) | `gpu_busy_percent`, hwmon temperature and power |
| Apple Silicon | `vm_stat` wired memory (D-20 finding 6: it tracks a model; ioreg's figure does not) | ioreg's "Device Utilization %", memory in use |
| everything | — | system memory (`/proc/meminfo`, `vm_stat`, `GlobalMemoryStatusEx`), and the runtime's `/api/ps` |

rocm-smi and amd-smi read the same sysfs counters; reading them directly
needs no tool, no output format and no root. `powermetrics` (a Mac's
temperature and power) needs root and is not used. Where nothing reads the
graphics memory — AMD and Intel cards on Windows (D-6), Intel on Linux,
graphics built into the processor, an NVIDIA card without nvidia-smi — the
run's `sampler_note` says so in words; nothing is shown as zero.

The model's footprint (`peak_vram`) is the counter's peak rise over its
baseline before the load, summed over devices: step 0's measurement, taken
continuously. It is not reported when another model came or went during the
run (`/api/ps` is watched for that), and is marked partial when the model was
split. Utilisation and power are means over the timed requests; temperature
is the hottest reading; system memory is the peak in use (absolute: a mapped
model sits in the page cache, which Linux counts as available).

## D-48. A measurement replaces its estimate, and narrows the others

**Decision.** Product rule 4's second sentence, twice over:

- **The configuration measured.** When a run finishes, the `estimates` row for
  (this hardware profile, the catalogue file, num_ctx, the cache type read,
  the path read) is inserted or updated in place with `source = 'measured'`,
  the measured rates (low = high), the measured footprint where the model was
  wholly on the graphics, and `measured_run_id` — D-13 and D-43 said step 6
  owns the row; it does. Only a configuration the estimator can be asked
  about is written: a model the catalogue maps to a file (step 4), and a
  cache type and path that were read. `GET /api/recommend` and
  `GET /api/models/{id}/fit` read the latest measurement of every
  configuration on this hardware (by fingerprint, across daemon starts) and
  pass it through `Estimate.WithMeasurement`: the speed becomes a point, the
  memory the measured figure, the confidence high (D-42).
- **Everything similar.** `estimate.Calibrate` turns every finished,
  wholly-resident, dense run into what this machine achieved: memory
  bandwidth while answering (tok/s × the bytes a token reads, the cache at the
  run's average fill included) and the rate it reads a prompt (tok/s ×
  parameters). Other models on the same path are then estimated from the
  measurement nearest in size, ± `CalibrationMargin` (5%, the gate's own
  tolerance) + `CalibrationMarginPerDoubling` (6%) for every doubling of size
  between them, capped at 30% and never wider than the published range was. A
  part that is not in gpus.yaml — which had no estimate at all — gets one;
  the processor's range, the widest of all, collapses to this machine's. The
  estimate stays an estimate (`Speed.Calibrated`, `CalibratedFrom`), the
  reason on the card says which test it came from, and the confidence treats
  it as a tight path. Mixture-of-experts and split runs do not calibrate. The
  constants are CHOSEN in `estimate.Config`, with what settles them.

**Consequences.** One benchmark of llama3.1:8b on a Windows PC makes every
other model's range on that card this card's, not the population's. The
measured model itself, benchmarked through a name the catalogue does not
know, still calibrates — `model_json` keeps the header facts from Ollama's
`/api/show` (`catalog.HeaderFromRuntime`).

## D-49. The API, the refusal, and the screen

**Decision.**

- `GET /api/bench/plan?model=&num_ctx=&prompts=` — what a run would do,
  without loading anything: the prompts that fit, the estimate, the duration
  (estimated, from the speed range, the load and the settle), and the
  **refusal** (step 6, item 5). Not in the build plan's list; added because
  product rule 5 needs the button to say how long it takes, and a refusal is
  better shown before the click than after it.
- `POST /api/bench` `{model, num_ctx, prompts, measure_anyway}` → 202 and the
  run. Refused with 409 (`would_spill`, `not_recommended`) when the estimate
  says the configuration spills onto the processor, only fits at a shorter
  context (the plan names it), or does not fit at all — unless
  `measure_anyway`; a run made anyway says so in its notes. A model planned
  for the processor because there is no usable graphics is not spilling
  (product rule 6), and a budget the advisor cannot read refuses nothing:
  measuring is what it needs. 422 `nothing_fits` when no prompt fits the
  context; 404 for a model not installed; 409 while a run is in progress.
- `GET /api/bench/{id}` — the run with its samples; with `Accept:
  text/event-stream` (a browser's `EventSource`) its progress as server-sent
  events (`event: progress`, each carrying the whole run, so a reader that
  falls behind loses nothing), a keep-alive comment every 15 s, and one event
  for a run already finished.
- `POST /api/bench/{id}/cancel` — stops the run, unloads the model (D-45),
  and answers with the run as it ended, `unloaded` included.
- `GET /api/bench/history[?model=][&limit=]` — newest first, each run
  compared with the previous run of its configuration.

The literal paths beside the `{id}` wildcard are registered with
`Server.apiLiteral`: the wildcard's own fallback already answers a wrong
method with 405. `advisor bench` is the developer's text client
(`-runs 2`: the gate's repeatability; `-cancel-after`: the gate's cancel,
checked against Ollama's own `/api/ps` as well as the daemon's word);
`scripts/verify.command` runs both. A minimal **Benchmarks screen** plans,
runs, follows, cancels and lists — so the gate runs on the Windows PC without
a terminal; step 8 builds the full screen (compare two runs side by side).

## D-50. The fleet's first runs: prompts cut mid-sentence, short answers not timed, a gate model that the machine paces

Supersedes parts of D-44 (how prompts are cut), D-45 (the headline) and
D-49 (the CLI's cancel), after the step 6 gate's first runs on the fleet
(2026-09-19, Ollama 0.34.2, suite 1).

**What the runs showed.**

- Repeatable where the machine paces the model: the RTX 5070 Ti agreed
  within 0.4% (llama3.2:1b at 4k and 32k, qwen3:14b at 32k), the Mac Pro's
  D700s on Vulkan within 0.1% (llama3.2:1b). The M1 Pro did not:
  llama3.2:1b ran 109.5, 101.8 and 103.7 tok/s in three runs, while each
  run's own three timings agreed within 3.4%. At 100+ tok/s a 1B model is
  paced by the processor's work per token (and Ollama's two HTTP hops per
  token to llama-server), not by memory, and a laptop with 13 GB of its
  16 GB in use moves that between one load and the next.
- Suite 1 cut prompts at paragraph ends, and its longest prompt was the
  whole essay. Models answered it with an end-of-text after 1 token, and
  stopped after 24 and 46 tokens on others. An answer of 24 tokens at
  430 tok/s is 56 ms, which a rate cannot be built on. The M1 Pro's 46-token
  answers spread 7% and 36%, against 2.5–3.4% for its 256-token ones.
- The cancel check fired after a fixed 20 s. On the M1 Pro a whole run of
  llama3.2:1b took 20 s, so the run had finished and there was nothing left
  to cancel.
- The plan shown above a finished run still carried the estimate the run
  had just replaced (the Mac Pro's screen: ≈ 100–176 tok/s above a measured
  53.2). The estimated ranges also printed decimals they do not have
  ("≈ 47.0–57.0").

**Decision.**

1. **Suite 2.** Each prompt is the text's first N `words`, cut mid-sentence;
   the loader refuses a cut after punctuation. The text gains four
   paragraphs in the second February (the seed swap, the annual meeting,
   the work party, a late snow), so the long prompt ends in the middle of
   the narrative, well before the essay's two closing paragraphs. The
   prompts are ≈ 501, 1,970 and 7,472 tokens with Llama 3's tokenizer
   (7,472 + 256 + the margin fits 8,192). Temperature 0 cannot be told to
   ignore the end-of-text token: Ollama 0.34.2 forwards no `ignore_eos` to
   llama-server (checked in `llm/llama_server.go`). So a model can still stop
   early, but it has to finish a sentence first.
2. **Short answers time the reading, not the answering.** An answer under
   `bench.Config.MinAnswerTokens` (64) is left out of the answering median.
   When every answer of a prompt is that short, the prompt keeps its reading
   speed and time to first token, and its answering speed is absent with
   the reason in words (`generation_unknown`). The run's headline is the
   shortest prompt that has an answering speed. A run where no prompt has
   one finishes, says so, and replaces no estimate.
3. **The gate measures a model the machine paces.** Without `-model`,
   `advisor bench` picks the smallest installed curated model of at least
   3 billion parameters whose plan is not refused. If there is none, it
   takes the largest smaller one and says why. On the M1 Pro that is
   llama3.1:8b. The customer's screen is unchanged: any installed model,
   with the spread shown.
4. **The cancel check follows the run.** `-cancel-during loading|measuring`
   (replacing `-cancel-after`) cancels when the progress stream reaches that
   phase. If the run ends first, the check reports that it was not tested,
   rather than passing or failing it.
5. **The plan shows the measurement.** `Plan.Measured` is the latest
   finished run of the same model file, context, runtime version and suite
   on this machine. The screen shows it in the estimate's place (product
   rule 4) and asks for the plan again when a run ends. Estimated ranges
   print whole numbers from 10 up.

**Consequences.** Runs of suite 1 and suite 2 are never compared, because
the suite version is in the key. The M1 Pro's gate has to be run again on
suite 2 with llama3.1:8b. The Windows and Mac Pro results stand as the first
evidence for repeatability, but they were measured on suite 1.

## D-51. Step 6's gate is met: the M1 Pro on suite 2, and what the margin leaves open

Closes the step 6 gate that D-50 reopened, after the M1 Pro's re-run
(2026-09-19, `scripts/verify.command`, Ollama 0.34.2, suite 2, daemon
`3ca6b70`).

**What the run showed.**

- **Repeatable, with 0.6 points to spare.** Two consecutive runs of
  llama3.2:3b at a context of 4,096 answered at 54.9 and 52.5 tok/s: 4.4%
  against the 5% limit. Both read the metal path and the f16 cache from
  Ollama's log and unloaded afterwards, and both replaced their estimate.
  The second run's plan had already narrowed onto the first run's
  measurement — 44–60 tok/s, where run 1's plan had said 53–85 — which is
  build-plan step 6's item 6 working on a real machine rather than in a
  test.
- **The gate model is llama3.2:3b, not llama3.1:8b.** D-50 §3 named
  llama3.1:8b as this laptop's pick. The rule it states — the smallest
  installed curated model of at least 3 billion parameters whose plan is
  not refused — picks llama3.2:3b, which families.yaml puts at 3.21
  billion. Both models are installed here. The code
  (`gateMinParameters = 3e9` in `cmd/advisor/bench.go`) does what D-50
  decided; D-50's example of it was wrong.
- **The margin belongs to the short prompt.** The headline is the shortest
  prompt that has an answering speed (D-50 §2), here the 501-token one. Its
  three timings inside the second run disagreed by 20.6%, and the harness
  said so in words. The 1,970-token prompt, which nothing headlines, moved
  51.1 to 51.5 tok/s across the same two runs — 0.8%.
- **Both cancel phases pass.** `-cancel-during loading` and
  `-cancel-during measuring` each ended with the daemon and Ollama's own
  `/api/ps` agreeing that nothing was left loaded. The check D-50 §4 put in
  place of the fixed 20 s is exercised in both phases, on the machine whose
  short runs defeated the old one.
- **Suite 2's answers are long enough to time.** Every timed answer ran to
  the 256-token budget, so nothing fell under `MinAnswerTokens` and no
  prompt lost its answering speed. The 7,472-token prompt was skipped at a
  context of 4,096, with the reason in words.
- **Wired memory moves between runs.** The same model reported 2.8 GB and
  then 3.2 GB of graphics memory taken while Ollama's own size stayed at
  2.4 GB. On Apple Silicon that figure is wired memory (D-47), which counts
  what else the machine has wired, so it carries a few hundred megabytes of
  other processes with it.

**Decision.** The step 6 gate is met and step 6 is closed. Nothing about
one laptop's run is evidence enough to move a constant or a rule.

**Consequences.**

- The fleet's evidence is one machine on suite 2 and two on suite 1: the
  RTX 5070 Ti's pass (+0.4%, −0.1%, +0.2%) and the Mac Pro's (−0.1%) were
  measured before the suite changed, and suites are never compared. A
  Windows run on suite 2 is a confirmation for step 7 to take when that
  machine is next up, not a gate that is owed.
- **The thin margin and the headline's noisy prompt are one open item, not
  two fixes.** Raising the 3 billion bar so the gate lands on a model the
  memory paces, or headlining the longest prompt that has an answering
  speed instead of the shortest, would each likely tighten 4.4%. Neither is
  worth a change on a single run. Every run stores its per-prompt spread,
  so the fleet's next benchmarks say whether 4.4% was this laptop that
  afternoon or the pace of a 3B model.
- The Apple Silicon memory reading carries other processes' wired pages, so
  a `measure_anyway` run at the edge of "fits" is weaker evidence for the
  92% threshold on a Mac than on a machine with its own graphics memory.
  The NVIDIA machine is where that constant should be settled.

## Open items, for the steps that own them

- **Port.** `server.DefaultPort = 27182` with fallback to an OS-chosen port.
  Step 11 decides whether the port is persisted so "open the app" always
  lands on the same URL.
- **Logging to a file** in the data folder: step 11, with the tray.
- **Notarisation and code signing**: Itay's decision in step 11; changes
  install-page copy, not code.
- **llama.cpp and LM Studio adapters** (step 4/18): D-27 expects no
  interface change; `ModelSource{Kind: huggingface_gguf}` is exercised for
  the first time by whichever comes first.
- **A UI for Install/Pull progress and the backends/models inventory**: step
  11; step 3 only adds a temporary shell status line.
- **What step 5 read from the catalogue** — closed by D-38. What stays open
  is measurement: no model with a sliding window, shared layers, experts or
  latent attention is inside a gate yet. `scripts/probe0` on the fleet with
  the catalogue's sizes of those kinds is the check, and
  `advisor recommend` prints each card's layout notes so a wrong reading of a
  real header is visible.
- **The constants that are CHOSEN** (estimate.Config says which): where
  "fits" ends (92%), the OS reserve, the processor's efficiency range and the
  no-AVX2 factor. Every benchmark run now stores Ollama's size against
  size_vram, the log's layers offloaded, and the peak of system memory
  (D-46, D-47): a `measure_anyway` run at the border of "fits" is the
  evidence for 92%. `scripts/calibrate` with `-ngl 0` on the Windows PC and
  the Mac Pro still settles the processor's population range; on a machine
  that has run a benchmark, calibration (D-48) already replaces it. The Mac
  Pro's D700s on Vulkan are older than anything in the public scoreboard —
  benchmark them first.
- **llama.cpp is not Ollama.** The population ranges come from llama-bench;
  on a machine that has been benchmarked, Ollama's own rates replace them
  (D-48). The fleet's benchmark rows against their uncalibrated estimates are
  now the measurement of the gap.
- **Where Ollama's log is** (D-46): `~/.ollama/logs/server.log` on a Mac,
  `%LOCALAPPDATA%\Ollama\server.log` on Windows (checked against Ollama's
  troubleshooting page, not yet on the Windows PC), the journal on a systemd
  Linux install (readable only by a user in the `systemd-journal` or `adm`
  group). Where it cannot be read, runs keep path and cache type unknown and
  replace no estimate — the first Windows run is the check.
- **The step 6 gate is met** (D-51) — two consecutive runs agree within 5%
  on generation speed on the NVIDIA machine and the Apple Silicon one; a
  cancelled run leaves nothing loaded. The RTX 5070 Ti passed on suite 1
  (+0.4%, −0.1%, +0.2%; a cancelled run left nothing loaded) and the Mac Pro
  too (−0.1%); the M1 Pro passed on suite 2 with llama3.2:3b (−4.4%, and a
  cancel in each phase), through `scripts/verify.command`. What stays open is
  confirmation rather than the gate: a Windows run on suite 2 when that
  machine is next up, and whether a memory-paced gate model or a longer
  headline prompt would tighten the M1 Pro's 4.4%. The per-prompt spreads
  every run stores are the evidence for both.
- **Load time.** Two Windows runs at different contexts both reported a
  load of 1,675 ms. That is Ollama's `load_duration` for the warm-up,
  stored per run; the Mac's two runs differed (1,865 and 2,800 ms). It is
  probably a coincidence, and `GET /api/bench/{id}` shows each request's
  `load_ms` if it happens again.
- **The quant an Ollama tag pulls.** The engine assumes the usual default;
  at least one small size in Ollama's library defaults to a larger quant.
  A per-size field in families.yaml is the fix when it matters.
- **Quality beyond size.** Until step 9b brings public signals, a larger
  model of an older family can out-rank a smaller one of a newer family;
  the purposes order is the curator's only lever.
- **The live gate** passed on the M1 Pro (2026-09-19, `verify.command`):
  every size resolved, no weights downloaded. `advisor catalog refresh` in
  `scripts/verify.command` stays the live check for catalogue edits.
