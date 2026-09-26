#!/usr/bin/env bash
# build_app.sh — assembles "Local LLM Advisor.app" from already-built
# binaries: the icon, the Info.plist, one or two architecture slices
# (lipo'd into a universal binary when both are given), and — when the
# Apple secrets described in RELEASING.md are present in the environment —
# a real Developer ID signature, notarization, and a stapled ticket so
# Gatekeeper never needs a network round-trip on the end user's machine.
#
# Usage:
#   packaging/macos/build_app.sh <version> <out-dir> <arm64-binary> [<amd64-binary>]
#
#   <version>       the release tag (e.g. v0.3.1) or "dev" for a local build.
#   <out-dir>       directory "Local LLM Advisor.app" is written into
#                   (created if needed; any existing app there is replaced).
#   <arm64-binary>  a darwin/arm64 advisor binary (CGO_ENABLED=0 build).
#   <amd64-binary>  optional darwin/amd64 binary — when given, the two are
#                   lipo'd into one universal executable; when omitted, the
#                   app ships arm64-only.
#
# Signing (see RELEASING.md's secrets table for how to obtain each one):
#   unset  APPLE_CERT_P12_BASE64 / APPLE_CERT_PASSWORD / APPLE_TEAM_ID /
#          APPLE_ID / APPLE_APP_SPECIFIC_PASSWORD
#     -> ad-hoc signs (`codesign --sign -`). The app runs, but a fresh
#        download gets Gatekeeper's "unidentified developer" prompt —
#        INSTALL.md walks the user through the one-time right-click-Open.
#   all five set
#     -> imports the Developer ID Application certificate into a
#        throwaway keychain, signs for real with the hardened runtime and
#        a secure timestamp, submits to Apple's notary service
#        (`xcrun notarytool submit --wait`), and staples the ticket onto
#        the .app. A notarized, stapled app opens with no warning at all.
#
# Requires a real macOS host with the Xcode Command Line Tools (iconutil,
# sips, codesign, xcrun, ditto, security) — this script never installs
# them. It cannot be run or verified from a Linux sandbox; only CI on a
# macos-latest runner (or a real Mac) proves it works. See
# claude/step-11-packaging.md for exactly what was and wasn't verified.
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "build_app.sh: this must run on macOS (needs iconutil, sips, codesign, xcrun)." >&2
  exit 1
fi

if [[ $# -lt 3 || $# -gt 4 ]]; then
  echo "usage: $0 <version> <out-dir> <arm64-binary> [<amd64-binary>]" >&2
  exit 1
fi

VERSION_RAW="$1"
OUT_DIR="$2"
BIN_ARM64="$3"
BIN_AMD64="${4:-}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
APP_NAME="Local LLM Advisor"
EXECUTABLE_NAME="advisor"
APP_DIR="$OUT_DIR/$APP_NAME.app"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# --- version strings ---------------------------------------------------
#
# CFBundleShortVersionString/CFBundleVersion want dotted-integer strings.
# `make build`'s VERSION comes from `git describe` (e.g. "v0.3.1",
# "v0.3.1-2-gabc1234", "v0.3.1-2-gabc1234-dirty", or "dev" for an
# unstamped developer build) — take just the leading MAJOR.MINOR.PATCH,
# falling back to 0.0.0 so a "dev" build still produces a valid plist
# rather than failing here.
version_stripped="${VERSION_RAW#v}"
if [[ "$version_stripped" =~ ^([0-9]+\.[0-9]+\.[0-9]+) ]]; then
  VERSION_SHORT="${BASH_REMATCH[1]}"
else
  VERSION_SHORT="0.0.0"
fi
VERSION_FULL="$VERSION_SHORT"

echo "==> Local LLM Advisor.app  version $VERSION_RAW  (bundle version $VERSION_SHORT)"

# --- assemble the bundle skeleton --------------------------------------
rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"

if [[ -n "$BIN_AMD64" ]]; then
  echo "==> lipo: combining arm64 + amd64 into a universal binary"
  lipo -create -output "$APP_DIR/Contents/MacOS/$EXECUTABLE_NAME" "$BIN_ARM64" "$BIN_AMD64"
else
  echo "==> single-architecture app (arm64 only — no amd64 binary given)"
  cp "$BIN_ARM64" "$APP_DIR/Contents/MacOS/$EXECUTABLE_NAME"
fi
chmod +x "$APP_DIR/Contents/MacOS/$EXECUTABLE_NAME"

# --- icon: master.png -> AppIcon.icns -----------------------------------
ICON_SRC="$ROOT/packaging/icon/master.png"
if [[ ! -f "$ICON_SRC" ]]; then
  echo "build_app.sh: missing $ICON_SRC — run scripts/gen_icon.py first." >&2
  exit 1
fi
ICONSET="$WORK/AppIcon.iconset"
mkdir -p "$ICONSET"
for size in 16 32 128 256 512; do
  double=$((size * 2))
  sips -z "$size" "$size" "$ICON_SRC" --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
  sips -z "$double" "$double" "$ICON_SRC" --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP_DIR/Contents/Resources/AppIcon.icns"

# --- Info.plist ----------------------------------------------------------
sed \
  -e "s/@EXECUTABLE@/$EXECUTABLE_NAME/" \
  -e "s/@VERSION_SHORT@/$VERSION_SHORT/" \
  -e "s/@VERSION_FULL@/$VERSION_FULL/" \
  "$ROOT/packaging/macos/Info.plist.tmpl" > "$APP_DIR/Contents/Info.plist"
plutil -lint "$APP_DIR/Contents/Info.plist"

# --- sign (+ notarize when the Apple secrets are set) -------------------
have_apple_secrets=1
for v in APPLE_CERT_P12_BASE64 APPLE_CERT_PASSWORD APPLE_TEAM_ID APPLE_ID APPLE_APP_SPECIFIC_PASSWORD; do
  if [[ -z "${!v:-}" ]]; then
    have_apple_secrets=0
    break
  fi
done

if [[ "$have_apple_secrets" -eq 1 ]]; then
  echo "==> Apple signing secrets present: real Developer ID sign + notarize + staple"

  KEYCHAIN="$WORK/build.keychain-db"
  KEYCHAIN_PASSWORD="$(openssl rand -base64 24)"
  ORIGINAL_KEYCHAINS="$(security list-keychains -d user | sed 's/^ *"//; s/" *$//')"

  security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
  security set-keychain-settings -lut 21600 "$KEYCHAIN"
  security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"
  # shellcheck disable=SC2086
  security list-keychains -d user -s "$KEYCHAIN" $ORIGINAL_KEYCHAINS

  CERT_PATH="$WORK/developer_id.p12"
  echo "$APPLE_CERT_P12_BASE64" | base64 --decode > "$CERT_PATH"
  security import "$CERT_PATH" -k "$KEYCHAIN" -P "$APPLE_CERT_PASSWORD" -T /usr/bin/codesign
  security set-key-partition-list -S apple-tool:,apple: -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN" >/dev/null

  SIGN_IDENTITY="$(security find-identity -v -p codesigning "$KEYCHAIN" \
    | grep -m1 'Developer ID Application' \
    | sed -E 's/^[[:space:]]*[0-9]+\)[[:space:]]+[A-F0-9]+[[:space:]]+"(.*)"$/\1/')"
  if [[ -z "$SIGN_IDENTITY" ]]; then
    echo "build_app.sh: no 'Developer ID Application' identity found in the imported certificate." >&2
    exit 1
  fi

  codesign --force --deep --options runtime --timestamp \
    --sign "$SIGN_IDENTITY" --keychain "$KEYCHAIN" "$APP_DIR"
  codesign --verify --deep --strict --verbose=2 "$APP_DIR"

  NOTARIZE_ZIP="$WORK/notarize.zip"
  ditto -c -k --keepParent "$APP_DIR" "$NOTARIZE_ZIP"
  xcrun notarytool submit "$NOTARIZE_ZIP" \
    --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_SPECIFIC_PASSWORD" \
    --wait
  xcrun stapler staple "$APP_DIR"
  xcrun stapler validate "$APP_DIR"
else
  echo "==> No Apple signing secrets — ad-hoc signing only (unsigned for Gatekeeper's purposes)"
  codesign --force --deep --sign - "$APP_DIR"
fi

echo "==> built: $APP_DIR"
