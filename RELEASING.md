# Releasing Local LLM Advisor

This is the maintainer-facing half of build-plan step 11 — INSTALL.md is
the customer's. Read `.goreleaser.yaml` and `.github/workflows/ci.yml`
alongside this; this file explains *why* they're shaped the way they are
and gives you the exact commands, but the workflow file is the ground
truth for what actually runs.

## Cutting a release

```
git tag v0.3.1
git push --tags
```

That's the whole manual step. Pushing a tag matching `v*` to this repo
triggers everything below by itself — no button to click on GitHub, no
separate "publish" step.

**The version-tag contract.** Tag names must start with `v` followed by
at least `MAJOR.MINOR.PATCH` (`v0.3.1`, or `v0.3.1-beta.1` if you ever want
a pre-release — the extra suffix is preserved in what gets built but
ignored by the update check's comparison). `internal/update.Check`
compares the *running* binary's version against the newest release's
`tag_name` using exactly this prefix (`^v?(\d+)\.(\d+)\.(\d+)`) — a tag
that doesn't parse this way still creates a release fine, but "Check for
updates" won't be able to compare against it correctly. Never re-push a
tag that already shipped; cut a new patch version instead, even to fix a
packaging mistake — `git tag -f` after the fact rewrites history a
downloaded release already points at.

## What CI does, in order

Pushing a `v*` tag runs, in this sequence (`.github/workflows/ci.yml`):

1. **`build`** (the existing per-OS matrix — ubuntu-latest, macos-latest,
   windows-latest) runs the full test suite natively on each OS, builds
   and smoke-tests that OS's own binary, and — on the ubuntu runner only
   — cross-compiles all four targets (`make build`) and uploads them as
   the `advisor-<os>` artifacts the later jobs consume. Nothing here is
   new to step 11.
2. **`release`** (needs `build`, tag pushes only) runs
   `goreleaser release --clean`: builds all four targets itself (a second,
   independent build — this is normal GoReleaser practice, not wasted
   work, since it's what actually gets archived and checksummed), merges
   the two macOS binaries into one universal binary, archives everything,
   builds the Linux `.deb` via `nfpm`, writes `checksums.txt`, and creates
   the GitHub Release with all of it attached.
3. **`package-macos`**, **`package-windows`**, **`package-linux-appimage`**
   (each needs `build` and `release`, tag pushes only) run in parallel.
   Each downloads its OS's already-built, already-smoke-tested binary from
   step 1 (not a rebuild), runs that OS's hand-rolled packaging script
   (`packaging/{macos,windows,linux}/`), and attaches the result to the
   *same* release with `gh release upload --clobber`. These are the four
   things GoReleaser OSS cannot build itself — see `.goreleaser.yaml`'s
   own comment on why.

Once step 3's three jobs finish, the release has: four raw
archives (linux/darwin-universal/windows), the `.deb`, `checksums.txt`,
the `.dmg`, the Windows installer, and the AppImage. INSTALL.md tells the
customer which file is theirs.

## Testing a release without pushing a tag

**The GoReleaser side** (archives, `.deb`, checksums, universal binary) —
this is genuinely safe to run locally, it publishes nothing:

```
goreleaser release --snapshot --clean --skip=publish,announce,sign,validate
```

This was run for real while building step 11 (not just reviewed): it
cross-compiled all four targets, merged the darwin pair into one real
universal Mach-O binary, built a real installable `.deb` (verified with
`dpkg-deb -c`/`-I`), and produced matching checksums, entirely on a Linux
machine with no macOS or Windows in sight — that's what makes this
particular check possible without either OS. `goreleaser check` alone
validates just the YAML's shape, faster, when that's all you need.

**Each OS's hand-rolled piece** — `make dmg`, `make installer`,
`make appimage` each wrap one packaging script and only ever work on their
own OS (a Mac for `dmg`, needing Xcode's command line tools; anywhere with
Inno Setup's `iscc` on PATH for `installer`; any Linux with `appimagetool`
on PATH for `appimage` — that one, unlike the other two, even runs inside
a plain Linux container, which is how it was actually built and
end-to-end verified for this step: a real `appimagetool` build, then the
resulting `.AppImage` actually launched under FUSE and answered
`/api/health`). `make dmg` and `make installer` could only be reviewed,
not run, outside their real OS — `claude/step-11-packaging.md` says
exactly which checks did run for each of the three.

## The secrets table

None of these exist yet — every job above already runs the unsigned path
today, and stays that way until you add the matching secret in this
repo's **Settings → Secrets and variables → Actions**. Adding a secret is
the entire activation step; no workflow file changes are needed on that
day.

| Secret | Enables | Where it comes from |
| --- | --- | --- |
| `APPLE_CERT_P12_BASE64` | Real macOS codesigning | A **Developer ID Application** certificate, exported from Keychain Access as a `.p12` and base64-encoded (`base64 -i cert.p12 \| pbcopy`). Requires an Apple Developer Program membership — **$99/year** ([developer.apple.com/programs](https://developer.apple.com/programs/)). |
| `APPLE_CERT_PASSWORD` | (paired with the above) | The password you set when exporting that `.p12`. |
| `APPLE_TEAM_ID` | Notarization | The 10-character Team ID on your membership page. |
| `APPLE_ID` | Notarization | The Apple ID (email) that belongs to that membership. |
| `APPLE_APP_SPECIFIC_PASSWORD` | Notarization | An **app-specific password** for that Apple ID, generated at [appleid.apple.com](https://appleid.apple.com) — never your real Apple ID password. |
| `WINDOWS_CERT_BASE64` | Real Windows codesigning | A code-signing certificate exported as a `.pfx`, base64-encoded. Either a traditional OV/EV certificate from a public CA (DigiCert, SSL.com, Sectigo, and others — cost and identity-verification requirements vary by provider and by certificate type, so compare current offers rather than trust a number here), or Microsoft's newer certificate-less **Azure Trusted Signing**, recently renamed **Azure Artifact Signing** (see [its pricing page](https://azure.microsoft.com/en-us/pricing/details/trusted-signing/) for current rates) — **note:** this repo's current `package-windows` job only knows how to sign with a local `.pfx` file via plain `signtool sign /f /p`; Azure's service signs differently (its own CLI plugin, no local private key), so picking it means updating that job's signing step, not just adding this secret. |
| `WINDOWS_CERT_PASSWORD` | (paired with the above) | The `.pfx`'s own export password. |

Nothing here is a repo variable or a checked-in file — they're GitHub
Actions secrets specifically, masked in every log line that would
otherwise print them.

## Identifiers that must never change once shipped

Each of these is how an OS recognizes "this is the same app" across
versions — changing one after a real release orphans whatever the old
identity already set up on a user's machine (a LaunchAgent that silently
stops matching, a Start Menu upgrade that installs side-by-side instead of
replacing, notification permissions Windows has to ask for again). If one
ever must change, that's a new decision in `ARCHITECTURE.md`, not a quiet
edit.

| Identifier | Value | Where |
| --- | --- | --- |
| macOS bundle ID / LaunchAgent label | `io.github.itayp.localllmadvisor` | `packaging/macos/Info.plist.tmpl`, `internal/autostart/darwin.go` |
| Windows installer `AppId` (GUID) | `{EE047489-1E0E-4A31-A1F7-2E7F1A5A4132}` | `packaging/windows/setup.iss` |
| Windows autostart Run-key value name | `LocalLLMAdvisor` | `packaging/windows/setup.iss`, `internal/autostart/windows_logic.go` |
| Windows AUMID | `ItayPollak.LocalLLMAdvisor` | `internal/winapp/winapp.go` |
| Linux systemd user unit | `local-llm-advisor.service` | `internal/autostart/linux.go` |
| Linux desktop/icon ID | `local-llm-advisor` | `packaging/linux/local-llm-advisor.desktop`, the hicolor icon paths |

## The icon

`scripts/gen_icon.py` (needs Pillow: `pip install pillow --break-system-packages`)
is the one source for every derived icon file — the 1024px master, the
tray PNGs `data/icon` embeds, the Windows multi-size `.ico`, and the Linux
hicolor set. If the design ever changes, edit that script, rerun it, and
commit everything it wrote — nothing downstream regenerates these on its
own (deliberately: Pillow is a one-time image-generation dependency, not
part of the Go/Node toolchain `CLAUDE.md` promises is all a build needs).

## AppImage tooling

`appimagetool` is fetched from AppImageKit's `continuous` release tag, not
a dotted version — the project hasn't cut a new numbered release in
years, and `continuous` is what its own README and most consumers point
at instead. It's a rolling tag, not an immutable artifact, which is a real
(if small) reproducibility gap; pinning to a specific release's asset
checksum instead is a reasonable hardening step if that ever matters more
than it does today.
