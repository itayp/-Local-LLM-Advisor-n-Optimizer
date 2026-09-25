//go:build windows

package watch

import "context"

// Notify posts a Windows tray balloon notification via a short PowerShell
// script — no new dependency (PowerShell and .NET's NotifyIcon ship with
// every supported Windows), the same idiom cmd/advisor's openBrowser uses
// for "start" on this OS. A modern toast (Windows.UI.Notifications) needs
// an app identity registered through an installer, which arrives in
// build-plan step 11; the balloon works from a bare binary today.
func (n *osNotifier) Notify(ctx context.Context, note Notification) error {
	script := "Add-Type -AssemblyName System.Windows.Forms; " +
		"$n = New-Object System.Windows.Forms.NotifyIcon; " +
		"$n.Icon = [System.Drawing.SystemIcons]::Information; " +
		"$n.Visible = $true; " +
		"$n.BalloonTipTitle = " + psString(note.Title) + "; " +
		"$n.BalloonTipText = " + psString(note.Body) + "; " +
		"$n.ShowBalloonTip(10000); " +
		"Start-Sleep -Seconds 10; " +
		"$n.Dispose()"
	return n.run(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
}

// psString quotes s as a PowerShell single-quoted string literal (the only
// escape a single-quoted literal needs is doubling an embedded quote).
func psString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	out = append(out, '\'')
	return string(out)
}
