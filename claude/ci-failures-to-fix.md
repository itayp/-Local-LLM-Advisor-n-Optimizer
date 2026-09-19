# CI Failures to Fix

Collected from GitHub Actions runs. These block moving forward but can be batched and fixed together.

## Step 3: Backend Interface, Ollama Adapter — FIXED 2026-09-19

**Platform:** Windows only
**Test:** `internal/backend/ollama` → `TestDetectInstalledNotRunning`
**Error:** State detected as `not_installed`, expected `installed_not_running`
**File:** `ollama_test.go:125`

**Actual root cause (confirmed by reading the code, not a guess):** `findBinary()`
is genuinely OS-specific by design — `install_windows.go` looks up
`"ollama.exe"` on `$PATH`, while `install_darwin.go`/`install_linux.go` look
up `"ollama"`. `TestDetectInstalledNotRunning` lives in the shared,
no-build-tag `ollama_test.go` and runs on every OS CI tests on, but its
`fakeEnv` only registered the fake PATH entry under the key `"ollama"`. On
the Windows runner, `lookPath("ollama.exe")` missed that entry, `findBinary`
fell through to the (also-unset) `LOCALAPPDATA` check, and `Detect` reported
`StateNotInstalled` instead of `StateInstalledNotRunning`. Not a Windows
detection bug — a test fixture that didn't account for Windows's different
lookup key.

**Fix:** `fakeEnv.paths` in `TestDetectInstalledNotRunning` now registers
both `"ollama"` and `"ollama.exe"`, so the fake PATH satisfies whichever
OS-specific `findBinary()` the build compiles in. No production code
changed.

**Verification:** Full `go test` wasn't runnable end-to-end in the fixing
session (no network access to `proxy.golang.org` for `internal/hardware`'s
deps in that sandbox, no Windows/wine to execute a cross-compiled binary).
Verified instead by: (1) a full manual trace of `findBinary` in all four
`install_<os>.go` files against the exact `fakeEnv` the test constructs, and
(2) an isolated, dependency-free reproduction that copies the real
`env`/`fakeEnv`/`findBinary` logic verbatim and executes both the old and
new fixture against Windows's lookup path — confirmed the old fixture
returns `ok=false` (reproducing the reported failure) and the new one
returns `ok=true`. `gofmt -l` on the changed file is clean. Recommend a real
`go test ./internal/backend/ollama/...` on the next Windows CI run to close
the loop.

---

## Node.js 20 Deprecation (all platforms) — FIXED 2026-09-19

**Issue:** GitHub Actions used in `.github/workflows/ci.yml` were pinned to
actions running on the deprecated Node 20 runtime.

**Fix applied** (checked each action's release notes for the minimum
major version that moved to `node24`, and for breaking changes against
this repo's actual usage — none affect our inputs):
- `actions/checkout@v4` → `@v5` (v5.0.0: node24 runtime; no new required
  inputs)
- `actions/setup-go@v5` → `@v6` (v6.0.0: node24 runtime; no change to
  `go-version-file`/`cache` behavior)
- `actions/setup-node@v4` → `@v5` (v5.0.0: node24 runtime; also adds
  opt-in auto-cache keyed on a `packageManager` field in `package.json` —
  `ui/package.json` has no such field, so this is a no-op here)
- `actions/upload-artifact@v4` → `@v6` (v6.0.0: node24 runtime; no change
  to `name`/`path`/`if-no-files-found`)

All four only require GitHub Actions Runner ≥ 2.327.1, which GitHub-hosted
`ubuntu-latest`/`macos-latest`/`windows-latest` runners already satisfy.

---

## Pending: Additional failures from other steps

Waiting for user to provide errors from next steps...
