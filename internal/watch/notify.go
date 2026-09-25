package watch

import (
	"context"
	"os/exec"
)

// Notifier shows a desktop notification. Run calls it only for a candidate
// that qualified, and only when Settings.Mode is NotifyOn — a nil Notifier,
// or NotifyQuiet/NotifyNever, means the check still runs and still logs; it
// simply never pops a notice (BUILD_PLAN.md step 10, item 3).
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Default is the desktop notifier for this OS (notify_darwin.go,
// notify_linux.go, notify_windows.go; notify_other.go for anything else).
// Each shells out to the OS's own notifier — the same idiom cmd/advisor's
// openBrowser and server's openInFileManager use for per-OS commands —
// rather than a new dependency: nothing here needs cgo, and the
// dependency list CLAUDE.md pins stays the same.
//
// None of the three can hand the OS a click handler that reopens this
// specific daemon on this specific model's card — that needs a packaged,
// identified app (build-plan step 11); a bare binary launched from a
// terminal is not one. Until then every notifier still shows
// Notification.URL as text in the body, so clicking through by hand always
// works, and nothing here is silently dropped.
func Default() Notifier {
	return &osNotifier{run: runCommand}
}

// osNotifier is the per-OS notifier; Notify is implemented in the
// build-tagged files beside this one. run is a seam: tests replace it to
// capture what would have been run without launching a real notification.
type osNotifier struct {
	run func(ctx context.Context, name string, args ...string) error
}

func runCommand(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}
