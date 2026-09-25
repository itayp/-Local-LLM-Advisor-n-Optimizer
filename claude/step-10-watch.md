# Step 10: the new-model watch — open

**Status (2026-09-25): code complete, not yet run through `scripts/verify.command`.**
This session has no working Go toolchain for the full module graph — network
egress to `proxy.golang.org` is blocked at the org level, confirmed again
this session (a direct `go build` gets a 403, and the agent proxy's own
status endpoint shows the block is not a local misconfiguration) — so
`internal/watch`, `internal/store` and `internal/server` were written and
checked by `gofmt`, `go vet`/`go build` of the zero-dependency subset
(`internal/catalog/hf`, `internal/catalog/gguf`, `internal/figure`,
`internal/version`, all passing), and careful manual cross-referencing of
every signature against the real source. The UI half has real, iterative
verification: `npm run check` (tsc), `npm test` (vitest, 103/103), and
`npm run build` all pass. **Itay's run of `scripts/verify.command` on a real
machine is the next step** — see "What the gate needs" below.

## What exists

- **Data.** Migration `0007_watch.sql` adds `watch_runs` (one row per
  scheduled or manual check, plain columns plus the full report as JSON —
  `catalog_refreshes`' shape). `watch_state` itself was already reserved by
  the skeleton (migration `0001_schema_v0.sql`, step 1) — this step is the
  first to read and write it.
- **Go.** `internal/watch` (new package): `watch.go` (`Outcome`, `State`,
  `Notification`, `LogEntry`, `NotifyMode` with `on`/`quiet`/`never`,
  `Settings`, `Report`), `config.go` (`Config`: `DefaultInterval` 24h,
  `Jitter` 0.1, `MaintainerRepoLimit` 100 — all CHOSEN, all said why),
  `run.go` (`Run`: refresh already done by the caller, every curated size
  not yet `NotifiedAt` through the same `Engine.Recommend` `GET
  /api/recommend` uses, checked by membership in `Result.Recommendations`
  rather than re-scored — ARCHITECTURE.md D-57), `reasons.go`
  (`notificationFor`, PRD §12's shape), `maintainers.go` (new repos from a
  curated family's own maintainer, flagged for the curator, never scored),
  `notify.go` + `notify_darwin.go`/`notify_linux.go`/`notify_windows.go`/
  `notify_other.go` (desktop notifications via per-OS `exec.Command`, no new
  dependency). `internal/store/watch.go`
  (`UpsertWatchState`/`WatchStates`/`RecordWatchRun`/`RecentWatchRuns`;
  `notified_at` is `COALESCE`d so it is never cleared once set — enforced in
  SQL, not only by the Go caller remembering to check first).
  `internal/server/watch.go` (`RunWatch` — the scheduler's and a manual run's
  shared entry point: refresh, build the same engine `GET /api/recommend`
  would, run `watch.Run`; `WatchScheduler` — a jittered daily background
  loop from `cmd/advisor/main.go`, the same shape `RecordHardware` runs in;
  `GET /api/watch/log`, `POST /api/watch/run`, both registered in
  `routes()`). `internal/server/recommend.go` gained `machineFor(ctx)`,
  extracted from the HTTP-bound `machine(w, r)`, so the headless scheduler
  can build an `estimate.Machine` without a request. `internal/server/
  settings.go` gained durable `purposes` and `watch` settings sections
  (`SettingsUpdate`'s new fields are pointers — nil leaves that section
  unchanged, so one section's PUT cannot clobber another's). `apitypes.go`
  lists the three new API types for `TestEveryUserFacingNumberHasASource`.
- **UI.** `screens/Watch.tsx` at `/watch` (the last placeholder screen
  `index.ts` names): the watch log, newest first, with a "Check now" button,
  a paused notice when `Settings.watch.enabled` is off, and the refresh/
  maintainer error from the latest run shown without hiding its entries.
  `Settings.tsx` gained a Notifications section (the master switch, and the
  three-way mode). `Recommend.tsx`'s purposes are now durable: seeded once
  from the daemon's saved choice, written back on every change, so the
  watch has something to check candidates against even with nobody looking
  at the screen. `api/types.ts`/`api/client.ts` mirror the new API shapes.
- **Tests.** `internal/watch/reasons_test.go` (new): the duplicate-bullet
  bug this session found and fixed (below) stays fixed — the versus-current
  sentence appears exactly once; the bullet order matches PRD §12's example
  (fit, public, change, speed); `publicBullet`'s gate (nil, unscored, and
  below the top third all produce no bullet; a purpose with no short word
  yet falls back to "Strong benchmark results" rather than a blank or
  garbled line). `ui/src/screens/Watch.test.tsx` (6 tests: empty state,
  entries listing across all three outcomes, a refresh error shown beside
  real entries, check-now reloading the log, "already running" instead of
  failing silently, the paused notice). `ui/src/screens/Recommend.test.tsx`
  gained a test that purposes seed from a saved choice and persist a change.
  `ui/src/screens/Settings.test.tsx` gained a real test of the watch
  settings UI (replacing a placeholder). None of `internal/store/watch.go`,
  the per-OS notifiers, or `internal/server/watch.go` has a test yet — see
  "Carried forward".
- **Seen rendered.** Not this session — no daemon could be started (Go
  cannot build here). Itay's `scripts/verify.command` run is what shows the
  Watch screen, the Settings notifications section and a real notification
  for the first time.

## The PRD §12 fidelity check, and what it found

The task asks for the notification body to state its reasons "exactly as
PRD §12's example shows" — reading that example against the
already-written `notificationFor` (from earlier in this session, before
this doc) turned up two real problems, both fixed this session:

1. **A duplicate bullet.** `recommend/reasons.go`'s `card()` already folds
   the versus-current sentence into `Recommendation.Reasons` as a
   `Kind: "change"` entry (item 6) *and* sets `VersusCurrent` to that same
   string. `notificationFor` read both — every notification with a current
   model said "Compared with X, which you have: …" twice. Fixed: the loop
   over `Reasons` now skips `Kind == "change"`, and `VersusCurrent` is
   added once, in its PRD-ordered place.
2. **A missing bullet.** PRD §12's own example has a public-data line,
   "Strong coding benchmark results," that nothing in `internal/recommend`
   or `internal/watch` produced — `Recommendation.Public` was never turned
   into a reason (deliberately, for the API's card shape — P-2 keeps public
   and local data out of one struct), and nothing populated it at all
   inside a watch run, since `Engine.Recommend` runs deep inside
   `watch.Run`, not in the server layer where `GET /api/recommend` calls
   `view.Line(...)` after the fact. Fixed with a new `publicBullet` (gated
   on `Scored` and on `Position` already starting "Among the strongest" —
   the exact wording and bar `external.position()` uses for the same
   size's own "Public data" block, reused rather than re-derived) and a new
   `Options.PublicLine` closure so the server can hand `internal/watch` the
   same lookup without a new package dependency. ARCHITECTURE.md D-57 is
   amended with both.

## Decisions worth knowing next session

- **Membership, not re-scoring** (already the design before this doc; see
  D-57): `Run` asks `Engine.Recommend` once per run and checks each
  candidate's place in the answer. This is why `checkOne` needs no memory
  arithmetic of its own, and why a future change to `recommend`'s
  versus-current rule changes what gets notified without touching
  `internal/watch`.
- **`watch.Options.PublicLine` is a closure, not an import.** Keeps
  `internal/watch` exactly as free of `internal/catalog/external` as
  `internal/recommend` is — `Recommendation.Public` is something the server
  fills, in both `GET /api/recommend` and here.
- **The public bullet reuses `Position`'s own wording** ("Among the
  strongest") rather than a second `Rank`/`Rated` threshold, so the
  notification and the model's own Details page can never disagree about
  what counts as "strong."
- **`SettingsUpdate.Purposes`/`.Watch` are pointers; `.Advanced` stays a
  required `bool`** — the existing contract, every caller already sends it.
  A settings PUT for one section (Recommend's purposes checkboxes, say)
  must not silently erase another (the watch mode radio buttons).
- **`Recommend.tsx`'s purposes-persist effect is a plain closure, not a
  functional `setState` updater** — React StrictMode double-invokes a
  functional updater in development, which would have called the
  side-effecting `persistPurposes` twice.

## What the gate needs

`scripts/verify.command` on a real Go toolchain, then the "done when":
add a model to the catalogue that qualifies (fits this machine, beats the
current one on a selected purpose) and confirm it produces exactly one
notification with a reason; add one that does not qualify and confirm it
produces a log line — visible on `/watch` — and nothing else (no popup, no
second log line on the next run). Also worth checking by hand:
`advisor recommend`'s own top pick, if it has a public score in the top
third, should show the new bullet consistently with what `/models/{id}`'s
Details already says.

## Carried forward

- `internal/store/watch.go`, the per-OS notifiers, and
  `internal/server/watch.go` have no tests yet — constrained the same way
  every other Go test in this session was (no working `proxy.golang.org`),
  but worth writing once a real toolchain is available; `run_test.go`'s
  existing `TestRunFlagsANewMaintainerRepoOnceEver` may already cover
  `maintainers.go` adequately, not fully decided.
- A packaged, identified desktop app (build-plan step 11) is what would let
  a notification's click actually open the daemon on that model's card;
  until then every notifier states the URL as text in the body, and it
  works by hand.
