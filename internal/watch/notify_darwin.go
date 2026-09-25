//go:build darwin

package watch

import (
	"context"
	"strings"
)

// Notify posts a macOS Notification Center banner via osascript — no new
// dependency, the same idiom cmd/advisor's openBrowser uses for "open" on
// this OS. AppleScript's "display notification" takes plain text only: no
// markup, no clickable link (Notification.URL is in the body as text
// instead — see notify.go).
func (n *osNotifier) Notify(ctx context.Context, note Notification) error {
	script := "display notification " + appleScriptString(note.Body) +
		" with title " + appleScriptString(note.Title)
	return n.run(ctx, "osascript", "-e", script)
}

// appleScriptString quotes s as an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
