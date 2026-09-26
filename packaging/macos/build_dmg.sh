#!/usr/bin/env bash
# build_dmg.sh — wraps an already-built "Local LLM Advisor.app" (see
# build_app.sh) in a plain drag-to-Applications .dmg, the form INSTALL.md
# tells the customer to download.
#
# Usage:
#   packaging/macos/build_dmg.sh <app-path> <out-dmg-path>
#
#   <app-path>      path to "Local LLM Advisor.app" (build_app.sh's output).
#   <out-dmg-path>  where to write the .dmg (parent dir created if needed;
#                   an existing file there is replaced).
#
# Signing: hdiutil's own image format needs no signature to mount and run
# — the .app inside carries whatever signature build_app.sh gave it. When
# the same five Apple secrets build_app.sh looks for are present, this
# script additionally signs, notarizes, and staples the .dmg file itself,
# so that even the disk image — not just the app inside it — passes
# Gatekeeper with no network round-trip on the end user's machine. See
# RELEASING.md for the secrets table.
#
# Requires a real macOS host (hdiutil, codesign, xcrun) — see
# claude/step-11-packaging.md for what could and couldn't be verified
# outside of CI.
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "build_dmg.sh: this must run on macOS (needs hdiutil)." >&2
  exit 1
fi

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <app-path> <out-dmg-path>" >&2
  exit 1
fi

APP_PATH="$1"
OUT_DMG="$2"
APP_NAME="$(basename "$APP_PATH" .app)"
VOLUME_NAME="Local LLM Advisor"

if [[ ! -d "$APP_PATH" ]]; then
  echo "build_dmg.sh: no app bundle at $APP_PATH — run build_app.sh first." >&2
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

STAGING="$WORK/staging"
mkdir -p "$STAGING"
ditto "$APP_PATH" "$STAGING/$APP_NAME.app"
ln -s /Applications "$STAGING/Applications"

mkdir -p "$(dirname "$OUT_DMG")"
rm -f "$OUT_DMG"

echo "==> hdiutil: building $OUT_DMG"
hdiutil create \
  -volname "$VOLUME_NAME" \
  -srcfolder "$STAGING" \
  -ov -format UDZO \
  "$OUT_DMG"

have_apple_secrets=1
for v in APPLE_CERT_P12_BASE64 APPLE_CERT_PASSWORD APPLE_TEAM_ID APPLE_ID APPLE_APP_SPECIFIC_PASSWORD; do
  if [[ -z "${!v:-}" ]]; then
    have_apple_secrets=0
    break
  fi
done

if [[ "$have_apple_secrets" -eq 1 ]]; then
  echo "==> Apple signing secrets present: signing + notarizing + stapling the .dmg itself"

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
    echo "build_dmg.sh: no 'Developer ID Application' identity found in the imported certificate." >&2
    exit 1
  fi

  codesign --force --sign "$SIGN_IDENTITY" --keychain "$KEYCHAIN" --timestamp "$OUT_DMG"

  xcrun notarytool submit "$OUT_DMG" \
    --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_SPECIFIC_PASSWORD" \
    --wait
  xcrun stapler staple "$OUT_DMG"
  xcrun stapler validate "$OUT_DMG"
else
  echo "==> No Apple signing secrets — .dmg left unsigned (the .app inside carries its own ad-hoc signature)"
fi

echo "==> built: $OUT_DMG"
