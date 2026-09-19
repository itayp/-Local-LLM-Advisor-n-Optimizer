# Step 7: first-run onboarding — closed

**Status (2026-09-20): the gate is met and step 7 is closed.** Itay ran the
actual "watched, not helped" walk on two real machines — his Mac (Ollama
already installed, so the "found" branch) and a Windows machine with Ollama
and its models removed first, so the "not installed" branch, the install
button and its progress, and the rest of the flow all ran for real. Both
worked. `verify.log` is the Mac's `scripts/verify.command` run.

Built the eight-screen first-run flow BUILD_PLAN.md specifies, end to end:
Welcome → Checking your computer → Ollama → What will you use AI for? →
Recommendations → Getting it → Try it → Use it. `main.tsx` shows it instead
of the working app (`OnboardingGate`) until `POST /api/onboarding/complete`
has run once. ARCHITECTURE.md D-52 is the record.

## What exists

- **Go:** `internal/store/settings.go` (a `settings` key/value store, no
  history — step 1's table, no new migration needed);
  `internal/server/onboarding.go` (`GET /api/onboarding`,
  `POST /api/onboarding/complete`); `internal/backend.InstallSizer` (an
  optional capability interface, `LoadObserver`'s shape) plus
  `internal/backend/ollama`'s `InstallSize` (a `HEAD` request against each
  OS's installer URL) and `internal/server/install.go`
  (`GET .../install-size`, `POST/GET .../install`, `POST .../start`);
  `internal/server/pull.go` (`POST/GET /api/models/pull`,
  `POST /api/models/pull/cancel`); the new `internal/chatapps` package
  (Ollama's own app, LM Studio, the Open WebUI desktop app carrying its
  maturity note, Jan, AnythingLLM — detected by well-known install location
  per OS, never installed or driven — D-4) and
  `internal/server/chatapps.go` (`GET /api/chatapps`).
- **UI:** `ui/src/onboarding/` — `OnboardingGate`, `Onboarding` (the step
  controller) and the eight screens; `ui/src/copy/glossary.ts` (the seven
  terms CLAUDE.md names, one line each) and `ui/src/components/Term.tsx`
  (a button + span, not `<details>` — it sits inside ordinary sentences);
  `ui/src/onboarding/Progress.tsx` and `format.ts` shared between the
  install and pull steps. `api/types.ts` and `api/client.ts` gained
  `BackendInfo`/`BackendsResponse` (missing until now — nothing had needed
  them) alongside the onboarding types proper.
- **Tests:** `ui/src/onboarding/Onboarding.test.tsx` — `OnboardingGate`'s
  three states (completed, not completed, fails open when the daemon can't
  be asked), a full walkthrough from Welcome to a measured `<Figure>`
  shown next to the estimate it replaces (Ollama already running, model
  already installed, to keep the polling deterministic — the download
  step is exercised separately), `OllamaStep`'s two actionable states,
  `Purposes`'s default, and `Getting`'s progress + cancel. 48 UI tests
  total (was 40); none of the existing ones needed touching —
  `OnboardingGate` lives in `main.tsx`, outside `App.tsx`'s own route
  tree, and every existing test renders `<App>` directly.
  `internal/server/onboarding_test.go` and `internal/store/settings_test.go`
  on the Go side.

## Decisions worth knowing next session

- **No SSE for install or pull** (unlike benchmarks): a short, one-viewer,
  foreground action polls `GET` once a second instead. Neither is written
  to the store — D-13's history is evidence the app is built on, and a
  one-time install isn't that.
- **`internal/chatapps` is a sibling of `internal/backend`, not a member.**
  Detected, never driven, never stored — D-4 draws that line and step 7's
  package respects it structurally, not just by convention.
- **`OnboardingGate` wraps `<App>` in `main.tsx`.** This is what let every
  existing screen test (Computer, Recommend, Benchmarks, App itself) pass
  completely untouched — they render `<App>` directly, which never sees
  the gate. Fails open (shows the working app) if `GET /api/onboarding`
  can't be answered at all, rather than risking a door that never opens.
- **Term is a toggle button + span, not `<details>`/`<summary>`.** The
  first draft used `<details>` and React logged real DOM-nesting warnings
  (`<summary>`/`<p>` aren't valid inside a `<p>`, and Term sits inline in
  onboarding's sentences) — caught by the test suite, not by eye.
- **Not all seven glossary terms had to appear in onboarding's own copy.**
  The rule is that none of the seven is ever shown bare; Recommend's and
  Benchmarks' existing Advanced tables already carry inline explainers for
  quantization, KV cache and offload. Onboarding's plain-language screens
  use `<Term>` for VRAM (the Ollama step), GGUF (Getting it), and tok/s and
  context window (Try it and the recommendation cards) — the ones with a
  natural sentence in this flow. `glossary.ts` holds all seven either way,
  for step 8 to draw on without redefining them.

## Verification

Go: `gofmt -l`, `go vet -tags noui ./...`, `go build -tags noui ./...`, and
the full `go test -tags noui ./...` all clean across the whole module —
run through the same container-only Go-1.27.1 + `ncruces/go-sqlite3`
workaround steps 2–6 documented (this session's addition: the driver also
needed importing as `.../go-sqlite3/driver`, registering under Go's
`database/sql` as `"sqlite3"` — the bare package registers nothing).
UI: `tsc -b` clean, `vitest run` 48/48, `vite build` produces a working
bundle.

**The real gate, run 2026-09-20:** Itay walked both branches by hand.
- **Mac** (Ollama already installed): `make build` → `./dist/advisor-darwin-arm64`
  ran the daemon for real, opened the browser itself, and onboarding showed
  the "found" branch — matches `scripts/verify.command`'s green `verify.log`.
- **Windows** (Ollama and its models removed first, specifically to exercise
  the untested branch): the same build-and-run walk went through "Install
  Ollama (about N MB)" with real progress, "Start Ollama," a real
  recommendation, a real pull with progress, and a real one-minute
  benchmark end to end.

Both worked with nothing beyond what the screens themselves say — the gate
BUILD_PLAN.md sets ("watched by Itay and not helped") is met.

## Carried into step 8

1. The full Ollama and Models screens reuse `GET /api/backends` and
   `GET /api/chatapps` as they stand today — neither needs to change shape.
2. `OllamaStep`'s polling intervals (1.5 s / 1 s) are local constants, not
   `estimate.Config`/`bench.Config` material; worth promoting to one shared
   UI constant if step 8 grows the same polling pattern more than once.
3. Home and Settings are still placeholders (step 8's own scope, per
   `screens/index.ts`'s own comment — onboarding was built strictly "in
   front of" the existing screen list, not into it).
4. Three small UX asks that came out of the real walkthrough, plus one
   hardware-limitation note — logged in `claude/backlog.md` rather than
   here since none of them are onboarding-specific: a copy button
   wherever a model name is shown, a tok/s explainer that says what a
   speed is actually good for, multi-GPU setup help (and checking Ollama
   agrees with what the advisor detected), and a pointer to llama.cpp's
   multi-GPU pooling on the Mac Pro for whenever the llama.cpp backend
   (PRD §15/§18) gets scoped.
