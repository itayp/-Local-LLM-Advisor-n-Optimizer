# Step 11: packaging, installers, run-at-login, tray — open

**Status (2026-09-26): everything is written, and everything that could be
run for real without a real macOS or Windows machine was run for real —
but this session had neither.** The device link available to it (the
`mcp__remote-devices__*` tools) turned out, on inspection (`uname -a`:
`Linux ... aarch64 GNU/Linux`; no `/Applications`, no `iconutil`,
`hdiutil`, `codesign`, `xcrun` or `plutil` anywhere on it), to be a
generic Linux sandbox, not Itay's actual Mac hardware — the original plan
for this step assumed real, live macOS verification through that link,
and that assumption was wrong. This doc says exactly what ran for real,
what only got a careful read, and what genuinely needs a real macOS
runner, a real Windows runner, or Itay's own machine before step 11 can
close.

## What exists

- **Go.** `internal/tray` (`Run(ctx, Options) error` wrapping
  `github.com/gogpu/systray`, D-61; `runner.go` the real backend,
  `tray_test.go` a fake one), `internal/autostart` (`Enabled`/`Enable`/
  `Disable`, one file per OS — `darwin.go`/`linux.go` with no build tag,
  `windows_logic.go`'s pure decision logic plus the build-tagged
  `autostart_windows.go` real registry code and its `!windows` stub),
  `internal/update` (`Check`, D-62, `PermittedHost = "api.github.com"`),
  `internal/winapp` (`RegisterIdentity`, the AUMID, D-63). `cmd/advisor`
  gained `-tray`, the `app.launched_before` settings-table gate for the
  one-time browser open, and `winapp.RegisterIdentity()` on every start.
  `internal/watch/notify_windows.go` now posts a real toast (falling back
  to the balloon), with the string-building split into
  `notify_windows_script.go` so it can be unit tested without Windows.
  `internal/server` gained `GET /api/update/check` (`s.checkUpdate` seam,
  `update.Info` added to `APITypes()`).
- **UI.** Settings' "coming in step 11" update placeholder is now a real
  `UpdateCheck` component: idle → checking → up to date / a version and a
  download link / failed, the same pattern `OpenButton` already used.
- **Icon.** `scripts/gen_icon.py` (Pillow) draws one original mark (three
  ascending bars on a rounded square — no borrowed logo) and derives
  everything from it: `data/icon/{tray,tray_template}.png` (embedded),
  `packaging/icon/master.png` (1024px source), `packaging/windows/icon.ico`
  (multi-size), `packaging/linux/icons/hicolor/*/apps/local-llm-advisor.png`
  (16 through 512px). Regenerating and committing the output is the only
  step if the design ever changes (RELEASING.md).
- **Packaging scripts.** `packaging/macos/{Info.plist.tmpl,build_app.sh,
  build_dmg.sh}` — ad-hoc signs by default, real Developer ID sign +
  `notarytool submit --wait` + `stapler staple` (on both the `.app` and
  the `.dmg` separately) when the five Apple secrets exist.
  `packaging/windows/setup.iss` — Program Files, a Start Menu shortcut, an
  optional desktop shortcut, an optional "start at login" checkbox writing
  the exact registry value `internal/autostart` reads, a fixed `AppId`
  GUID, an uninstaller; unsigned unless invoked with `/DSIGN` and a
  `/Ssigntool` value (CI supplies both once the Windows secret exists).
  `packaging/linux/{local-llm-advisor.desktop,build_appimage.sh,
  deb-postinstall.sh,deb-postremove.sh}`.
- **Release automation.** `.goreleaser.yaml` (four cross-compiled targets,
  the darwin universal binary, archives, the `.deb` via `nfpm`, checksums,
  the GitHub Release — everything GoReleaser OSS can build, D-60).
  `.github/workflows/ci.yml` gained four jobs on a `v*` tag push: `release`
  (`goreleaser release --clean`), then `package-macos`, `package-windows`,
  `package-linux-appimage` in parallel, each downloading the existing
  `build` job's already-smoke-tested binary and attaching one more
  artifact to the same release with `gh release upload`. `Makefile` gained
  `make dmg`/`installer`/`appimage`, each OS-locked, each a thin wrapper
  around the scripts above.
- **Docs.** `INSTALL.md` (customer-facing, per OS, with the honest
  Gatekeeper/SmartScreen click-through since both are unsigned for now —
  including GNOME's tray-icon extension requirement, a real Linux desktop
  limitation this app cannot work around). `RELEASING.md` (the release
  sequence, the full secrets table with real current prices where they
  could be checked, the version-tag contract, the "never change once
  shipped" identifier table). `ARCHITECTURE.md` D-60 through D-63.
  `CLAUDE.md`'s Layout, build instructions and Dependencies sections.
  `claude/backlog.md` item (k) marked resolved.

## Verification matrix — what actually ran, versus what only got reviewed

| Piece | How it was checked | Genuinely run? |
| --- | --- | --- |
| `internal/tray`, `internal/autostart`, `internal/update`, `internal/winapp` — the Go logic | `go test` (all four OSes' seams, since the decision logic itself has no build tag), cross-compiled with `go build`/`go vet` for all four `GOOS/GOARCH` targets | Yes, except the real `gogpu/systray` backend and the real Windows registry/AUMID calls, which only a real OS can execute |
| The full daemon, `-tray -no-browser` | Built for linux/amd64, run for real, `/api/health` and `/api/update/check` (a real network call to `api.github.com`) both answered correctly, clean shutdown | Yes, on Linux |
| `scripts/gen_icon.py` output | Regenerated for real; every PNG checked with Pillow (size, RGBA, no ICC profile — exactly what `sips`/`iconutil` expect); the `.ico` checked as a real 6-frame Windows icon resource | Yes |
| `packaging/macos/{Info.plist.tmpl,build_app.sh,build_dmg.sh}` | `bash -n` + `shellcheck` (clean); the plist template's `sed` substitution checked against three representative version strings, each result parsed as well-formed XML | Reviewed only — no `iconutil`/`hdiutil`/`codesign`/`xcrun`/`plutil` exists anywhere this session could reach |
| `packaging/windows/setup.iss` | A Python check for balanced `#ifdef`/`#endif`, every `{#Macro}` resolving to a real `#define`, a valid GUID shape, every `[Section]` present | Reviewed only — Inno Setup's compiler (`iscc`) is Windows-only and was not available |
| `packaging/linux/{local-llm-advisor.desktop,build_appimage.sh,deb-postinstall.sh,deb-postremove.sh}` | `desktop-file-validate` (real tool, passed); `shellcheck`/`sh -n`; **and a full real build**: a real linux/amd64 binary, a real downloaded `appimagetool`, a real AppImage produced, then actually executed under FUSE and its `/api/health` answered correctly | Yes, completely — the one OS-specific packaging path this environment could prove end to end |
| `.goreleaser.yaml` | `goreleaser check` (schema-valid) **and** a real `goreleaser release --snapshot --clean --skip=publish,announce,sign,validate`: cross-compiled all four targets, merged the darwin pair into a genuine universal Mach-O (`file` confirms two architectures), built and inspected a real installable `.deb` (`dpkg-deb -c`/`-I`), wrote matching checksums, then ran the resulting linux binary for real | Yes, fully — a real GoReleaser binary was installed and used |
| `.github/workflows/ci.yml`'s new jobs | `actionlint` (a real, current binary, downloaded and run) against the whole file — caught one real bug (`secrets` context isn't readable from a step's `if:`; fixed with a job-level `env:` instead) and confirmed clean afterward; YAML parses; every `${{ }}` expression was hand-traced | Reviewed + linted, not executed — that needs a real GitHub Actions run on a pushed tag |
| `INSTALL.md`, `RELEASING.md` | Rendered with `python-markdown` (tables parse, no unclosed emphasis); the Apple Developer Program price ($99/year) and the Azure Artifact Signing rename were checked against current pages rather than assumed from training | Reviewed, and the facts in them were checked, not just the prose |

The honest summary: Linux's whole packaging path (AppImage, and the parts
of GoReleaser this machine could run) got real, end-to-end proof. macOS
and Windows packaging got careful review, static validation, and every
tool's own linter where one existed — but neither could actually run
`build_app.sh`, `build_dmg.sh`, or `iscc` here, and neither can until a
real macOS runner and a real Windows runner (or Itay's own machines) try
them, which is exactly what pushing a real tag does.

## Decisions worth knowing next session

- **The device link is a Linux sandbox, not Itay's Mac.** Don't plan
  future work assuming this session (or one like it) can drive real
  macOS-only or Windows-only tools through it. If Itay's actual desktop
  ever needs driving, that has to come through whatever the real
  device-bridge tools report as connected, verified with a command like
  `uname -a` before trusting it — the same check that caught this.
- **Two build paths for the macOS/Linux binaries, on purpose** (D-60):
  GoReleaser's own build (for the raw archive) and the packaging jobs'
  download of the `build` job's already-smoke-tested binary (for the
  `.dmg`/AppImage). Don't try to unify them — each side stays simple to
  reason about alone, and RELEASING.md says why.
- **`gogpu/systray` does not auto-toggle a checkbox's `Checked` state on
  click** — confirmed by reading `platform_linux.go`'s `Event()` handler,
  not assumed from its doc comments (which mention a `Quit()` method that
  does not actually exist — `Remove()` is what unblocks `Run()`).
  `internal/tray`'s autostart toggle does the invert-and-confirm itself.
- **`VersionInfoVersion` was deliberately left out of `setup.iss`** — it
  demands a strict `a.b.c.d` numeric format, and the version string this
  project actually stamps (`git describe`, e.g.
  `v0.3.1-2-gabc1234-dirty`) very often is not that. `AppVersion` (a
  free-form display string) carries the real value without the
  constraint.
- **`appimagetool` is fetched from AppImageKit's `continuous` tag, not a
  dotted version** — the project has not cut a new numbered release in
  years; RELEASING.md explains this so it doesn't read as an oversight
  later.

## What the gate still needs

1. **A real tag, pushed to a real remote**, so `release`,
   `package-macos`, `package-windows` and `package-linux-appimage` actually
   run on GitHub's own macOS and Windows runners. RELEASING.md suggests
   `v0.0.0-test` (or `goreleaser release --snapshot`, already proven
   locally) as the safe first try. This is the single biggest open item —
   nothing macOS- or Windows-specific in this step has run on real
   hardware yet.
2. **The done-when itself, on all three OSes**: a fresh machine, a
   download, and the Welcome screen — no terminal at any point. That can
   only be judged by hand, on real hardware, once (1) has produced real
   installers to try.
3. Once a real Mac is reachable (by Itay, or by a session with an actual
   device link to one — not this session's Linux sandbox), the `.app`'s
   tray icon, its "Open"/"Start at login"/"Quit" menu, and a real toast on
   Windows are the parts of D-61/D-63 that remain genuinely unseen.

## Carried forward

- No LICENSE file exists in this repo yet; `.goreleaser.yaml`'s `nfpms`
  entry has no `license:` field because of it. Worth a decision (even "no
  license, all rights reserved" is a decision) before the first real
  public release — it doesn't block a test tag.
- Windows signing only supports a local `.pfx` via plain `signtool sign
  /f /p` right now. If Itay picks Azure Artifact Signing instead of a
  traditional certificate, `package-windows`'s signed step needs a real
  rewrite (a different CLI plugin, no local private key) — RELEASING.md
  flags this so it isn't a surprise.
- `internal/tray`'s real backend, the real Windows toast, and the real
  macOS/.dmg and Windows/installer scripts all still want a first real
  run — see the verification matrix above for exactly which.
