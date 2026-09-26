#!/usr/bin/env bash
# build_appimage.sh — wraps the linux/amd64 advisor binary in a single
# self-contained AppImage: download it, `chmod +x`, double-click, no
# terminal, no package manager, no install step at all (CLAUDE.md Rule 1
# — this is the option for someone on a distro with no .deb support, or
# who would rather not install anything system-wide).
#
# Needs no linuxdeploy-style shared-library bundling: the binary is a
# CGO_ENABLED=0 build (ARCHITECTURE.md D-26), so it has no dynamic
# library dependencies beyond the Linux syscall ABI itself — the AppDir
# below is only the binary, its .desktop file, and its icon.
#
# Usage:
#   packaging/linux/build_appimage.sh <binary-path> <out-appimage-path>
#
#   <binary-path>       a linux/amd64 advisor binary (CGO_ENABLED=0 build).
#   <out-appimage-path> where to write the .AppImage (parent dir created
#                       if needed; an existing file there is replaced).
#
# Requires `appimagetool` on PATH — this script never downloads or
# installs it; the CI job that calls this fetches it first, from
# AppImageKit's "continuous" release (RELEASING.md explains why that's
# the one to use — the project has not cut a new dotted-version release
# in years). Only that CI run (or a real Linux desktop with appimagetool
# installed) can prove the resulting AppImage actually launches under
# FUSE/AppImage's own runtime; see claude/step-11-packaging.md for exactly
# what this script's own checks cover versus what only CI confirms.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <binary-path> <out-appimage-path>" >&2
  exit 1
fi

BIN_PATH="$1"
OUT_APPIMAGE="$2"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
APP_NAME="local-llm-advisor"

if [[ ! -f "$BIN_PATH" ]]; then
  echo "build_appimage.sh: no binary at $BIN_PATH" >&2
  exit 1
fi
if ! command -v appimagetool >/dev/null 2>&1; then
  echo "build_appimage.sh: appimagetool not found on PATH (RELEASING.md says which release to install)." >&2
  exit 1
fi

ICON_SRC="$ROOT/packaging/linux/icons/hicolor/256x256/apps/$APP_NAME.png"
if [[ ! -f "$ICON_SRC" ]]; then
  echo "build_appimage.sh: missing $ICON_SRC — run scripts/gen_icon.py first." >&2
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

APPDIR="$WORK/AppDir"
mkdir -p "$APPDIR/usr/bin" "$APPDIR/usr/share/applications" "$APPDIR/usr/share/icons/hicolor/256x256/apps"

cp "$BIN_PATH" "$APPDIR/usr/bin/advisor"
chmod +x "$APPDIR/usr/bin/advisor"

cp "$ROOT/packaging/linux/$APP_NAME.desktop" "$APPDIR/usr/share/applications/$APP_NAME.desktop"
# appimagetool additionally requires a copy of the .desktop file and the
# icon at the AppDir root (the AppImage/AppDir spec, not a Debian
# convention) — same files, two required locations.
cp "$ROOT/packaging/linux/$APP_NAME.desktop" "$APPDIR/$APP_NAME.desktop"
cp "$ICON_SRC" "$APPDIR/usr/share/icons/hicolor/256x256/apps/$APP_NAME.png"
cp "$ICON_SRC" "$APPDIR/$APP_NAME.png"

cat > "$APPDIR/AppRun" <<'EOF'
#!/bin/sh
HERE="$(dirname "$(readlink -f "$0")")"
exec "$HERE/usr/bin/advisor" "$@"
EOF
chmod +x "$APPDIR/AppRun"

mkdir -p "$(dirname "$OUT_APPIMAGE")"
rm -f "$OUT_APPIMAGE"

echo "==> appimagetool: building $OUT_APPIMAGE"
ARCH=x86_64 appimagetool "$APPDIR" "$OUT_APPIMAGE"
chmod +x "$OUT_APPIMAGE"

echo "==> built: $OUT_APPIMAGE"
