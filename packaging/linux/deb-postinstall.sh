#!/bin/sh
# deb-postinstall.sh — nfpm's postinstall script for the .deb (wired up in
# .goreleaser.yaml's nfpms.scripts.postinstall). Refreshes the desktop
# menu and icon caches so "Local LLM Advisor" shows up with its own icon
# right away, with no login/logout needed. Never touches the user's data
# directory, never starts the daemon, never asks for anything beyond what
# dpkg itself already required (this file only runs as a normal part of
# `apt install`/`dpkg -i`, already root by then) — CLAUDE.md Rule 5 is
# about the *running app* changing the machine, not the installer putting
# its own files where every .deb's files go.
set -e

if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database -q /usr/share/applications || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
	gtk-update-icon-cache -q -f /usr/share/icons/hicolor 2>/dev/null || true
fi

exit 0
