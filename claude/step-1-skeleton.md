# Step 1 — Architecture record, repo skeleton, three-OS CI

**Status: GATE PASSED. Step 1 is done at commit `5d0f1ac` on `main`
(2026-09-18). Step 2 may start — in a new session, with Opus.**

Itay ran the `make build` binaries from that tree on all three machines
(the M1 Pro, the Windows/NVIDIA PC, the Ubuntu Mac Pro — headless, reached
through an SSH tunnel to 127.0.0.1:27182 with `-no-browser`); each started,
opened/served the shell and answered `/api/health`. Commits: `bf0d770`
skeleton, `705ca5e` tidy-up, `5d0f1ac` verified + go mod tidy. Still to do,
a formality: `git push` so the CI matrix runs once and uploads the
artifacts.

Repo: `/Users/itay.pollak/Local LLM Advisor n Optimizer`, remote
`git@github.com:itayp/-Local-LLM-Advisor-n-Optimizer.git`.

## Verified (2026-09-18 15:03 CEST, M1 Pro, Go 1.27.1, Node 26.4)

`scripts/verify.command` ran the CI sequence end to end: `go mod tidy`
(go.sum unchanged — the seeded hashes were the published ones; two indirect
requirements dropped), `make test` (`go test ./...` against the real
`modernc.org/sqlite` with the UI embedded, 17 UI tests), `go test -tags
noui ./...`, `make build` (all four targets), and a smoke test of the
darwin/arm64 binary: `/api/health` → `{"version":"705ca5e-dirty","os":
"darwin","arch":"arm64","go_version":"go1.27.1"}`, the SPA served at
`/settings`, foreign `Host` header → 421. Log: `verify.log` (ignored).

In the cloud session before that: UI build + tests, Go tests for figure /
hardware / backend / server (embedded and `-tags noui`), vet + cross-compile
of all four targets with the SQLite driver stubbed, schema v0 executed in
SQLite with CHECK/FK exercised.

## What exists now

- `ARCHITECTURE.md` — ADR. D-1..D-10 restate BUILD_PLAN's decisions with
  consequences; D-11..D-22 are step 1's own: package layout and dependency
  direction; loopback as a constant enforced three ways (constant, `Serve`
  refuses non-loopback listeners, Host/Origin checks); one SQLite file with
  numbered embedded migrations and schema v0; provenance in the type system
  (`internal/figure`); `go:embed` with a `noui` build tag; data folder per
  OS; versioning via `-ldflags -X`; build + CI; hand-mirrored Go/TS types;
  step 0's estimator constraints (D-20); unknown-never-defaulted; few named
  dependencies.
- `CLAUDE.md` — the eight product rules verbatim, the two enforced rules,
  layout, how to run/test/build (including `scripts/verify.command`),
  conventions (Go, API, store, UI, copy, data files, dependencies, network),
  git.
- Go: `cmd/advisor`, `internal/{server,store,hardware,backend,catalog,
  estimate,recommend,bench,watch,figure,version}`. `/api/health` is the whole
  API. `server.LoopbackHost` is a const; `server.DefaultPort = 27182`.
- `internal/figure`: `Source ∈ {estimated, measured}` refuses to (un)marshal
  anything else including the zero value; `Bytes`, `Rate` (range for
  estimates, point for measurements); `figure.Check` walks API types and
  `server.APITypes()` + `TestEveryUserFacingNumberHasASource` make a bare
  numeric field a failing build unless tagged `source:"n/a"`.
- Schema v0 (`internal/store/migrations/0001_schema_v0.sql`): the eleven
  tables from the prompt + `schema_migrations`; `estimates.source` is
  `CHECK`ed; public data and local measurements never share a table.
- UI: Vite 8 + React 19 + TS 6 + Vitest 5; screens Home / Your computer /
  Ollama / Models / Recommend / Benchmarks / New models / Settings generated
  from one list; Advanced toggle in `SettingsProvider` (localStorage now,
  `settings` table in step 8); `<Figure>` renders the two treatments; all
  strings in `ui/src/copy/en.ts`. 17 tests.
- `Makefile`: dev / ui / build / test / test-go / check. `make build` →
  `dist/advisor-{linux-amd64,darwin-arm64,darwin-amd64,windows-amd64.exe}`.
- CI: `.github/workflows/ci.yml` — ubuntu / macos / windows; npm ci, build,
  UI tests, gofmt (not on Windows), vet, `go test ./...` embedded and
  `-tags noui`, native build, smoke test (health, SPA, foreign Host → 421),
  ubuntu also `make build`; artifacts uploaded.
- `scripts/probe0/` — step 0 moved unchanged (`git mv`), README paths
  updated, reports in `scripts/probe0/results/` (only the Mac's so far; the
  Windows/NVIDIA and Mac Pro JSONs should be added there).
- `scripts/verify.command` — the CI sequence as a double-clickable macOS
  script; output in `verify.log`.
- `data/catalog/families.yaml` — empty, schema in the header comment.

## Decisions worth knowing next session

- The Ubuntu machine is a headless server. The daemon is loopback-only by
  design (D-12); it is reached with `ssh -L 27182:127.0.0.1:27182 <server>`
  and run with `-no-browser`. LAN exposure would be a product decision that
  reopens D-12 with authentication, never a bind flag.
- `modernc.org/sqlite v1.57.0` (not the newest 1.59) because that was the
  version with independently published hashes when the proxy was blocked;
  bump with `go get -u modernc.org/sqlite && go mod tidy` once.
- A fresh clone needs `make ui` once before plain `go build ./...`
  (go:embed); `-tags noui` otherwise.
- The Vite dev proxy uses `changeOrigin: true` because the daemon rejects a
  foreign Host header.
- `Makefile` and `.github/workflows/ci.yml` are "protected files" for the
  remote file tools — write them through device_bash, not
  device_commit_files.
- Cowork grants Terminal in click-only mode: a session cannot type into
  Terminal on the Mac. `scripts/verify.command` exists for that reason.
- Network: the step 1 session's environment blocked proxy.golang.org and
  sum.golang.org (and go.dev, gitlab.com). Itay changed the environment's
  network access afterwards; a session reads that once at startup, so step
  2's session should have Go modules — check with `go mod download` first
  thing. If not: environment selector → gear → Network access → Trusted (or
  Custom with those two hosts and the package-manager defaults checked).

## Next: step 2 (Opus) — hardware detection

Paste BUILD_PLAN.md's step 2 prompt as the whole first message of a new
session, model Opus. It reads `CLAUDE.md`, `ARCHITECTURE.md` (D-5, D-6,
D-20, D-21), `scripts/probe0/main.go` (the detection code there is the
starting point; note its findings about Windows registry VRAM, macOS wired
memory, per-device budget), PRD §6 step 1. Fills `hardware.Detect`, adds
`data/hardware/runtime-support.yaml`, fixture tests per parser, `GET
/api/hardware`, stores the profile on every start (`hardware_profiles`).
