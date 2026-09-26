// Package icon embeds the daemon's tray icon (build-plan step 11) — a
// small, original mark (three ascending bars: "measured performance"), no
// borrowed logo. The full-resolution source these were rendered from, and
// the per-OS installer icons (.icns, .ico, Linux hicolor) converted from
// it, are packaging/icon/master.png — that one is used only by the
// packaging scripts, never embedded in the binary, so it is not part of
// this package.
package icon

import _ "embed"

// Tray is a plain, colored 32x32 PNG — the Windows and Linux tray icon
// (internal/tray's Icon.PNG).
//
//go:embed tray.png
var Tray []byte

// TrayTemplate is a 32x32 monochrome silhouette with alpha, no background
// — macOS's "template image" convention (internal/tray's Icon.Template),
// which the OS recolors to match the light or dark menu bar.
//
//go:embed tray_template.png
var TrayTemplate []byte
