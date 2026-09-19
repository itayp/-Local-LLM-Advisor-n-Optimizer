# Step 2 — Hardware detection

**Status: GATE PASSED (2026-09-18). Step 2 is done; step 3 may start — in a
new session, with Sonnet.**

Commits on `main`: `4e34907` step 2 (pushed; CI green on ubuntu, macos and
windows), `6813dd5` M1 Pro fixture confirmed on the machine (local, one ahead
of `origin/main` — push it).

## The gate, as it ran

1. M1 Pro, `scripts/verify.command` (build 4e34907): ALL GREEN. `go mod tidy`
   downloaded `goccy/go-yaml` v1.19.2 and changed nothing, so the go.sum
   hashes computed in the session were the published ones. Live profile:
   tier `gpu_medium`, Metal `recommendedMaxWorkingSetSize` = 12,713,115,648
   bytes (11.84 GiB, the "11.8 GiB" Ollama logs), `iogpu.wired_limit_mb`
   unset, 10/10 cores, 16 GiB, 16-core GPU, MacBook Pro, macOS 26.6.2 (25G83).
   The osascript/JXA Metal query works.
2. `git push` → CI green on all three runners (fixture tests + each runner's
   live `/api/hardware`).
3. Windows PC (RTX 5070 Ti): right, per Itay.
4. Ubuntu Mac Pro (2× D700): right, per Itay.
5. The M1 Pro fixture's Metal value turned out to be exactly the real one;
   `6813dd5` relabels it from "illustrative" to "confirmed" (fixture header,
   macprobe.go derivation comment, ARCHITECTURE.md D-25). Goldens unchanged.
   The M2 Air (8 GB) and M4 Max fixtures' values are still chosen, not
   captured, and say so.

## What exists

- `internal/hardware`: Detect behind an `env` seam; `winprobe.go` (one
  PowerShell query via -EncodedCommand: CIM + display-class registry for
  present controllers only, qwMemorySize, RadeonSoftwareVersion; nvidia-smi
  authoritative, merged by PCI id; Basic Display = card without driver;
  virtual/remote displays filtered), `macprobe.go` (sysctl, sw_vers,
  system_profiler JSON, Metal via osascript JXA, launchctl OLLAMA_MODELS),
  `linuxprobe.go` (PCI class 0x03 walk, uevent driver, amdgpu mem files +
  Ollama's iGPU signal, KFD gfx targets, pci.ids incl. subsystem names,
  nvidia-smi merged by bus id, systemd OLLAMA_MODELS), `support.go`,
  `derive.go` (order, budget, tier, summary, notes, Fingerprint v1),
  `storage.go`.
- `data/hardware/runtime-support.yaml` + `data/data.go` (embeds it): ordered
  rules checked against Ollama v0.34.2's CMakePresets and docs; amd_gfx name
  table; integrated-by-name table. Build wins over docs (Windows ROCm ships
  gfx1030/115x/120x though the docs list only RX 7000).
- Tests: 21 fixture machines (`testdata/<os>/*.txtar`), 24 scenarios with
  golden profiles (`testdata/golden/`), per-tool parser tests, the support
  table's contract, store history, API, and `TestDetectOnThisMachine` (each CI
  runner's real OS path).
- Store: migration 0002 (`daemon_version`), `AddHardwareProfile` every start,
  history by fingerprint. API: `GET /api/hardware` (waits for background
  detection), `/api/hardware/history`, `/api/hardware/profiles/{id}`.
- UI: "Your computer" screen (sentence, facts, Show details, Advanced
  technical table), types mirrored in `ui/src/api/types.ts`. 22 UI tests.
- ARCHITECTURE.md D-23..D-26; CLAUDE.md layout/conventions/dependencies;
  CI + verify.command print `/api/hardware`.

## Decisions worth knowing next session

- Tier thresholds: small < 7 GiB, medium < 14, large < 30, xl ≥ 30 (best
  usable device; Apple = GPU budget). OS below Ollama's floor → all GPUs
  expect none, tier unknown.
- Apple Silicon budget is never a ratio: the M1 Pro under macOS 26.6.2 gets
  11.84 of 16 GiB (0.74), not the folklore two-thirds (D-25) — now measured
  by the advisor itself, not only read from Ollama's log.
- Fingerprint = OS, arch, CPU model, RAM and GPU PCI id + VRAM rounded to
  GiB; excludes hostname, OS version, drivers, disk, budget.
- New deps: `github.com/goccy/go-yaml` (yaml.v3 is unmaintained since April
  2025 — step 4 reuses it) and `golang.org/x/sys` direct (CPUID).
- Step 3 records the runtime path actually taken per GPU beside
  `expected_backend`; the rule id and reason are already in the profile, and
  `hardware.RuntimePath` is the shared type.

## Session notes

- Network: the cloud environment still 403s proxy.golang.org (checked again
  after the session restarted). Worked around by building Go 1.27.1 from its
  GitHub source, fetching GitHub-hosted modules with GOPROXY=direct, and a
  container-only dev modfile that swaps modernc.org/sqlite for
  ncruces/go-sqlite3. Nothing of that is in the repo. Fix the environment's
  Network access (Trusted, or Custom with proxy.golang.org + sum.golang.org)
  before step 3.
- Git on the connected folder: `git status` leaves a stale `.git/index.lock`
  unless the session has delete permission for the folder — grant it first,
  or use `git --no-optional-locks` for read-only commands. Commits travel as
  a git bundle: create in the container, write into the folder, `git fetch`
  + `merge --ff-only` there, delete the bundle.
- The cloud environment's stop hook wants commits authored and committed as
  `Claude <noreply@anthropic.com>` (GitHub "Verified"). In the container
  clone, set `git config user.name Claude` and `user.email
  noreply@anthropic.com` before committing. Step 1's commits stay Itay's.

## Next: step 3 (Sonnet) — backend interface, Ollama adapter, install, runtime path

Paste BUILD_PLAN.md's step 3 prompt as the whole first message of a new
session, model Sonnet.
