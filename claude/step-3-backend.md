# Step 3 — Backend interface, Ollama adapter, install/start, runtime inventory

**Status: GATE PASSED (2026-09-18). Step 3 is done; step 4 may start — in a
new session.**

Commits on `main`: `6940f42` step 3 (pushed — confirmed in the repo's own
push reflog, `refs/remotes/origin/main@{0}: update by push`), on top of
step 2's `6813dd5`. If CI hasn't been checked yet, worth a glance before
step 4 — it wasn't verified from this session (no way to reach GitHub's
Actions UI from here).

## The gate, as it ran

1. `scripts/verify.command` (or equivalent `go build`/`go test`) run on all
   three machines against the real `modernc.org/sqlite` and embedded UI:
   ALL GREEN, per Itay.
2. Live checks against `GET /api/backends` and `GET /api/models/installed`
   on all three:
   - **Ubuntu box**: `running` 0.34.2, five models listed with correct
     digests/sizes/quantizations.
   - **Mac Pro (2× AMD FirePro D700, Ubuntu)**: `running` 0.34.2, same five
     models, and — after loading a model — `runtime_paths: {"0": "vulkan"}`
     with `detail` quoting the real log line (`msg="gpu memory" id=0
     library=Vulkan …`). This matches `GET /api/hardware`'s
     `expected_backend: "vulkan"` for the D700 exactly (`amd-vulkan` rule:
     "Ollama drives AMD cards outside its ROCm list with Vulkan"). Notably,
     the real log line uses `library=Vulkan` (capital V) and
     `msg="gpu memory"`, not the lowercase `library=vulkan` /
     `msg="inference compute"` form the unit tests and probe0 were built
     against — `pathFromLog`'s case-insensitive match caught it correctly,
     which is a stronger confirmation than the tests alone gave.
   - **Windows (RTX 5070 Ti)**: `running` 0.34.2, eight models listed, and
     after loading a model `runtime_paths: {"0": "cuda"}`, confirmed from
     the log (`library=CUDA`).
   - Across all three: `installed_not_running` vs. `not_installed` was not
     separately exercised (Ollama was already running everywhere in this
     fleet), but the `state` values returned were correct for what was
     actually running.
3. `Install`/`Start` were not exercised live — nothing in the running app
   calls them yet (no UI button; that's step 11's job), and all three
   machines already have Ollama installed and running, so there was nothing
   for them to do even if wired up. Confirmed only at the unit-test level
   this session.

## What exists

- `internal/backend/backend.go`: `Backend` interface frozen — `Name`,
  `Detect`, `Models`, `Show`, `Running`, `Pull`, `Generate`, `Unload`,
  `Install`, `Start` — checked against Ollama's HTTP API, LM Studio's local
  REST API (`/api/v1/models`, `/models/load`, `/models/unload`,
  `/models/download`) + the `lms` CLI, and llama-server's API (`/props`,
  `/v1/models`, `/slots`, its `--models-dir` router mode) before being
  frozen, per D-3. `Pull` takes a `ModelSource{Kind: ollama_tag |
  huggingface_gguf, ...}`, not a string — the seam most likely to leak an
  Ollama assumption. `ModelInfo.Details` carries GGUF metadata with the
  file format's own architecture-prefixed keys (`<arch>.block_count`,
  `<arch>.attention.key_length`, …), the same shape D-20's estimator
  findings expect, regardless of which backend produced it.
- `internal/backend/ollama/`: the only implementation.
  - `ollama.go` / `client.go`: HTTP client on `OLLAMA_HOST` (default
    `127.0.0.1:11434`); `Detect` tries `/api/version` first, falls back to
    a filesystem check (never starts anything); model_info parsing
    (`miValue`/`toNum`, preferring `attention.key_length` — D-20 — with a
    regression test) ported from `scripts/probe0/main.go`, which validated
    the wire format against real machines in step 0.
  - `Pull` streams `/api/pull` progress in bytes (`completed`/`total`) so
    the UI can say "2.1 of 4.7 GB". `Unload` is `/api/generate` with
    `keep_alive: 0` — Ollama's documented way to free memory now, no
    dedicated endpoint exists.
  - `install.go` + `install_{darwin,windows,linux,other}.go`: per-OS
    Install/Start, product rule 5 (button-only, states what it does first).
    macOS/Windows download the official installer over HTTPS (host + TLS
    verified; Ollama publishes no checksum, and the code says so rather
    than faking a check) and open it — `open Ollama.dmg` / launching
    `OllamaSetup.exe` — the user still clicks/drags through the installer's
    own UI, same as downloading it by hand. Linux extracts the official
    `ollama-linux-<arch>.tar.zst` into the app's own data folder and runs
    `ollama serve` as a supervised child process: no sudo, no `curl | sh`,
    no system service. `findBinary` per OS checks `$PATH` and the platform's
    known install spot (Homebrew/`Ollama.app`, `%LOCALAPPDATA%\Programs\
    Ollama`, this app's managed folder) before assuming nothing's there.
  - `runtimepath.go`: after a load, reads `/api/ps` (VRAM used or not) and
    the server's own log (the file this daemon wrote when it started
    Ollama, or Ollama's default location) for device-discovery lines
    (`library=cuda`, `ggml_vulkan: Found …`, …; last match wins, matched
    case-insensitively). A path is recorded per GPU index only when the log
    confirms it — never guessed from VRAM alone (D-21). Relevant env vars
    (`OLLAMA_VULKAN`, `GGML_VK_VISIBLE_DEVICES`, `HSA_OVERRIDE_GFX_VERSION`)
    are captured on every `Detect`, read only, never set.
- `internal/store/backend.go` + `models.go`: `RecordBackend`/`LatestBackend`
  (insert-only history, mirrors `AddHardwareProfile`'s never-update shape);
  `UpsertInstalledModels` marks a backend's existing rows absent then
  present again per the current report — a model removed from Ollama drops
  out of the API but its row stays for history, never deleted.
- `internal/server/backend.go`: `GET /api/backends` re-detects on every
  request (Detect is cheap and starts nothing, so this *is* "on demand");
  `installed_not_running` and `not_installed` are distinct `State` values in
  the response, not a boolean plus a guess. `GET /api/models/installed`
  (optional `?backend=name`). A `Detect` error falls back to the last
  stored row rather than guessing a state.
- `cmd/advisor/main.go`: blank-imports `internal/backend/ollama`, detects
  registered backends in the background alongside hardware, prints a
  temporary shell status line (`advisor: backend ollama: running 0.34.2 at
  http://127.0.0.1:11434`, etc.) — no UI screen; that is step 11's job.
- Tests: `internal/backend` (interface/registry), `internal/backend/ollama`
  (~19 tests: Detect/Models/Show/Running/Pull/Generate/Unload against
  `httptest.Server`, the D-20 key_length regression, install/runtime-path
  logic with a fixture-style fake `env`), `internal/store` (history +
  upsert-and-mark-absent semantics), `internal/server` (state
  distinguishability, inventory refresh on a running backend, installed
  version carrying forward across checks, detect-failure fallback).
- ARCHITECTURE.md D-27 (interface frozen against three runtimes), D-28
  (Ollama adapter shape), D-29 (Install/Start per OS), D-30 (four-state
  inventory, insert-only/mark-absent history), D-31 (runtime path as
  established fact). Open items updated: the interface item is closed; a
  UI for install/pull progress and the inventory is now explicitly step
  11's.

## Decisions worth knowing next session

- `Backend` is genuinely one interface for three runtimes now — step 4/18's
  llama.cpp or LM Studio adapter should need zero interface changes. If one
  turns out to be needed, that is a signal to re-read D-27, not just patch
  around it.
- `ModelSource{Kind: huggingface_gguf}` has never been exercised end-to-end
  yet (Ollama rejects it with `ErrUnsupportedSource`) — the first adapter
  that accepts it is the real test of that half of the seam.
- `GET /api/backends` does a live `Detect` on every call by design (cheap,
  never starts anything) rather than serving a cached value — if a future
  backend's `Detect` turns out not to be cheap, that backend broke its own
  interface contract, not this endpoint.
- The Linux install is deliberately unable to start at boot (no system
  service, no password prompt) — a real trade-off, not an oversight; see
  D-29 if it's ever questioned.
- Ollama publishes no checksum for its installers today; `install.go`'s
  `downloadFile` verifies host + TLS and says plainly that a checksum isn't
  checked, rather than a comment or a TODO. Revisit if that changes upstream.
- `pathFromLog`'s needles matched real-world log lines that differ in case
  and wording from what the unit tests/probe0 assumed (`library=Vulkan` +
  `msg="gpu memory"` vs. the lowercase `library=vulkan` +
  `msg="inference compute"` form) — confirmed working on the Mac Pro and
  Windows box, not just in tests.
- `Install`/`Start` remain entirely unreachable from outside Go — no route,
  no button — by design, until step 11. Don't be surprised there's nothing
  to click.

## Session notes

- Network: `modernc.org`, `proxy.golang.org`, `golang.org`, `gitlab.com`
  are still policy-blocked in this cloud environment (checked again this
  session via `$HTTPS_PROXY/__agentproxy/status`, same as step 2 found).
  `github.com` is open. Used the same workarounds step 2 already
  established, both container-only, neither committed:
  - A self-built Go 1.27.1 toolchain (`git clone --branch go1.27.1
    https://github.com/golang/go.git` + `GOROOT_BOOTSTRAP=/usr/local/go
    ./src/make.bash`), since `GOTOOLCHAIN`'s auto-download would itself hit
    `go.dev`.
  - `replace golang.org/x/sys => github.com/golang/sys v0.47.0` (this one
    *is* in the repo's `go.mod`, carried over from step 2 — it's a real,
    permanent replace, not a dev-only swap).
  - For compiling/testing `internal/store` and `internal/server` (which
    import `modernc.org/sqlite`, whose own dependency tree is all under
    `modernc.org`/`cznic` and not reachable): a **container-only** dev
    swap of `go.mod` + one line of `internal/store/store.go` (driver name
    `"sqlite"` → `"sqlite3"`) to `github.com/ncruces/go-sqlite3` instead,
    with `replace golang.org/x/text => github.com/golang/text v0.42.0` for
    one of *its* test-only transitive deps. Built, vetted, and tested
    everything (`internal/store`, `internal/server`, `internal/backend`,
    `internal/backend/ollama`, cross-compiled darwin/amd64, darwin/arm64,
    windows/amd64, linux/amd64, linux/arm64) with the swap in place, then
    reverted `go.mod`, `go.sum` and `store.go` byte-for-byte to their
    original (`modernc.org/sqlite`) content before writing anything back to
    the real repo — diffed against a backup to confirm. Nothing of this
    swap is in any committed file.
  - **Itay: this is the third session in a row that's had to route around
    the same blocked hosts.** If you want every future step to skip this
    entirely, the fix is the environment's Network access setting (gear →
    Network access → Trusted, or Custom with at least `proxy.golang.org`,
    `sum.golang.org`, `modernc.org`, `go.dev`, `gitlab.com` checked) —
    step 1 apparently had it open at some point, then it reverted.
- `go build ./...` (embedded UI) fails on `pattern all:ui/dist: no matching
  files found` in a fresh clone/container until `make ui` has run once —
  expected (D-18, noted in step 1's doc too); all verification here used
  `-tags noui`.
- `internal/hardware`'s test suite fails in this container
  (`testdata/<os>/*.txtar` fixtures: no such file or directory) — the
  fixtures were never staged into this session's workspace, since step 3's
  task scope explicitly excludes hardware/estimator/catalogue. Not a
  regression; confirmed by checking the fixture directory doesn't exist in
  the container at all. `internal/store`, `internal/server`,
  `internal/backend`, `internal/backend/ollama` all pass.
- Git on the connected folder: same as step 2 — `git status`/`add`/`commit`
  leave stale `.git/objects/tmp_obj_*` and lock files unless the session
  has delete permission for the folder; requested it this session and
  cleaned them up. This session's own `git push` attempt failed with "Host
  key verification failed" — the device shell (an isolated VM bridging only
  the mounted folder) has no SSH key/known_hosts for `github.com`. The push
  that landed `6940f42` on `origin/main` was done from Itay's own Terminal
  or Git client, confirmed via the repo's push reflog.
- Commit author/committer: `Claude <noreply@anthropic.com>` (local repo
  config on the connected folder, matching step 2's precedent), with a
  `Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>` trailer and a
  `Claude-Session:` link per this session's own attribution convention.
- An untracked file, `probe0-macOS-20260918-210222.json`, sits in the repo
  root (from running `scripts/probe0` or similar by hand) — left alone,
  unrelated to this step; worth a `.gitignore` entry or a move if it's not
  meant to be there.

## Next: step 4 — the catalogue

Paste BUILD_PLAN.md's step 4 prompt as the whole first message of a new
session. Worth a glance first, not a blocker: confirm CI actually ran green
on `6940f42` (this session had no way to check GitHub's Actions UI).
