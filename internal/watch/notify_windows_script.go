// This file is deliberately not build-tagged, unlike notify_windows.go
// beside it: the PowerShell it generates is pure string-building with no
// OS dependency of its own, so it is tested on every host (notify_windows
// itself — the //go:build windows file that actually shells out — can
// only run on a Windows CI runner).
package watch

import (
	"encoding/xml"
	"strings"
)

// toastScript is the PowerShell that shows a real Windows.UI.Notifications
// toast — the one that appears in Windows' own notification settings and
// history, unlike the legacy tray "balloon tip" (claude/backlog.md item
// (k)) — attributed to aumid (internal/winapp.AUMID, registered by
// cmd/advisor at startup). If the toast API refuses (an older Windows, or
// the AUMID registration not having taken effect for some reason), it
// falls back to the balloon tip in the same script, so Notify never has to
// decide which one will work — PowerShell finds out and falls back live.
func toastScript(aumid string, note Notification) string {
	toastXML := "<toast><visual><binding template=\"ToastGeneric\"><text>" +
		xmlEscape(note.Title) + "</text><text>" + xmlEscape(note.Body) + "</text></binding></visual></toast>"
	return "try { " +
		"[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] | Out-Null; " +
		"[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType=WindowsRuntime] | Out-Null; " +
		"$xml = New-Object Windows.Data.Xml.Dom.XmlDocument; " +
		"$xml.LoadXml(" + psString(toastXML) + "); " +
		"$toast = New-Object Windows.UI.Notifications.ToastNotification $xml; " +
		"[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier(" + psString(aumid) + ").Show($toast) " +
		"} catch { " + balloonScript(note) + " }"
}

// balloonScript is the legacy tray balloon tip — notify_windows.go's only
// mechanism before step 11, kept here as the toast's fallback and unit
// tested here directly (it used to be the whole file).
func balloonScript(note Notification) string {
	return "Add-Type -AssemblyName System.Windows.Forms; " +
		"$n = New-Object System.Windows.Forms.NotifyIcon; " +
		"$n.Icon = [System.Drawing.SystemIcons]::Information; " +
		"$n.Visible = $true; " +
		"$n.BalloonTipTitle = " + psString(note.Title) + "; " +
		"$n.BalloonTipText = " + psString(note.Body) + "; " +
		"$n.ShowBalloonTip(10000); " +
		"Start-Sleep -Seconds 10; " +
		"$n.Dispose()"
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

// xmlEscape escapes s for use as XML character data (toastXML above) —
// separate from, and applied before, psString's own PowerShell-literal
// escaping: the toast body is XML embedded inside a PowerShell string, and
// each layer only knows how to escape for itself.
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
