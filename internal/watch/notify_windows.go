//go:build windows

package watch

import (
	"context"

	"advisor/internal/winapp"
)

// Notify posts a real Windows.UI.Notifications toast — the one that shows
// up in Windows' own notification settings and history, with a permission
// entry the legacy tray "balloon tip" never had (claude/backlog.md item
// (k): Itay noticed the balloon popped up with no OS permission prompt,
// and this was the point where that changed). It falls back to the
// balloon tip in the same PowerShell script when the toast API refuses —
// see notify_windows_script.go, tested there since it has no OS dependency
// of its own; this file is only the shelling-out, which only a Windows
// host can run.
//
// The toast is attributed to winapp.AUMID, which cmd/advisor registers for
// this process (and its display name in the registry) on every start —
// HKCU-only, no installer or admin rights required.
func (n *osNotifier) Notify(ctx context.Context, note Notification) error {
	return n.run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", toastScript(winapp.AUMID, note))
}
