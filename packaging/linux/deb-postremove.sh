#!/bin/sh
# deb-postremove.sh — nfpm's postremove script for the .deb. The mirror
# image of deb-postinstall.sh: refreshes the same two caches so the menu
# entry and icon actually disappear instead of lingering stale until the
# next unrelated cache rebuild. Does not touch
# ~/.local/share/systemd/user/local-llm-advisor.service (any user who
# turned on "start at login" disables it themselves from the tray menu
# first, same as internal/autostart always requires an explicit click —
# an uninstall is not that click, and guessing wrong here would silently
# leave a dangling ExecStart after the binary is gone).
set -e

if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database -q /usr/share/applications || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
	gtk-update-icon-cache -q -f /usr/share/icons/hicolor 2>/dev/null || true
fi

exit 0
