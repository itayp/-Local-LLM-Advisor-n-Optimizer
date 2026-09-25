//go:build linux

package watch

import "context"

// Notify posts a desktop notification via notify-send (the
// org.freedesktop.Notifications convention every Linux desktop environment
// implements) — no new dependency. A machine with no notification daemon
// running (some window managers, a headless box) simply has nothing to
// show; the run still succeeds and the watch log still has the line.
func (n *osNotifier) Notify(ctx context.Context, note Notification) error {
	return n.run(ctx, "notify-send", "--app-name=Advisor", note.Title, note.Body)
}
