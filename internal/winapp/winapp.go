// Package winapp gives the daemon a real identity on Windows — build-plan
// step 11, resolving claude/backlog.md item (k): a Windows toast
// notification (as opposed to the legacy tray "balloon tip",
// internal/watch/notify_windows.go's old implementation) needs an
// Application User Model ID (AUMID) the OS can attribute the process to,
// which is also what lets the Start Menu shortcut, the "start at login"
// registry entry (internal/autostart) and the daemon's own notifications
// all read as one app instead of three unrelated things. Nothing here does
// anything on macOS or Linux.
package winapp

// AUMID is fixed — like internal/autostart's windowsRunValueName and
// internal/tray's launchAgentLabel-equivalent, it must never change once
// shipped: changing it orphans whatever notification settings a user
// already granted the old identity (RELEASING.md).
const AUMID = "ItayPollak.LocalLLMAdvisor"

// RegisterIdentity tells Windows who this process is:
//   - its own explicit AUMID (SetCurrentProcessExplicitAppUserModelID), so
//     a notification is attributed correctly whether the process was
//     launched from the Start Menu, the autostart Run-key entry, or a bare
//     double-click;
//   - a friendly display name for that AUMID under
//     HKCU\Software\Classes\AppUserModelId, so Windows' own notification
//     settings show "Local LLM Advisor" rather than the AUMID string.
//
// Both are HKCU-only: no admin rights, no installer required — cmd/advisor
// calls this unconditionally on every start, and it is a no-op (nil error)
// on any OS but Windows.
func RegisterIdentity() error {
	return registerIdentity()
}
