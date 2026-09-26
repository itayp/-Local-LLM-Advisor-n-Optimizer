// Package tray shows the daemon's system-tray icon and menu (build-plan
// step 11) — "Open <app>," a "Start at login" checkbox, and "Quit."
//
// The whole point of this package is that cmd/advisor never imports a
// third-party tray library directly. Today that library is
// github.com/gogpu/systray, chosen because it is the only cross-platform
// option with zero cgo (Windows: Shell_NotifyIconW via the golang.org/x/sys
// this repo already depends on; macOS: AppKit via a pure-Go FFI, no
// Objective-C compiler; Linux: the D-Bus StatusNotifierItem protocol) —
// keeping ARCHITECTURE.md D-26 ("nothing that needs cgo, ever") true for
// this feature too. It is also, as of this writing, about four months
// old with a small community (ARCHITECTURE.md D-61 records the risk and
// why it was accepted anyway). If it turns out to be flaky, only runner.go
// changes — Options and Run below are the whole surface cmd/advisor talks
// to.
package tray

import "context"

// Icon is the tray icon's image. PNG is a plain, colored PNG (Windows and
// Linux); Template, if set, is a macOS "template image" — a monochrome PNG
// whose alpha channel macOS recolors to match light/dark menu bars — used
// there instead of PNG when present.
type Icon struct {
	PNG      []byte
	Template []byte
}

// Options is everything Run needs. cmd/advisor builds one of these at
// startup; nothing in this package has an opinion about what the menu text
// says or what "start at login" means on this OS — those are the caller's
// job (internal/autostart, ui/src/copy's sibling on the Go side).
type Options struct {
	// Tooltip is the icon's hover text.
	Tooltip string
	// AppName is the identity gogpu/systray's Linux backend uses for
	// notifications; Windows and macOS decide this themselves (the
	// library's own doc comment).
	AppName string
	Icon    Icon

	// OpenLabel and Open are the first menu item. Open is nil-safe: a nil
	// Open simply omits the item.
	OpenLabel string
	Open      func()

	// AutostartLabel is the checkbox's text. AutostartEnabled reports the
	// current state when the menu is built (read once — nothing else in
	// this process changes it concurrently); AutostartSet is called with
	// the checkbox's new value on a click. Both nil omits the item
	// entirely (rather than showing a checkbox that does nothing).
	AutostartLabel   string
	AutostartEnabled func() bool
	AutostartSet     func(enabled bool) error

	// QuitLabel and Quit are the last menu item. Quit is expected to
	// cancel whatever context the daemon is shutting down on; nil omits
	// the item (there is always meant to be one — cmd/advisor always sets
	// it — but Run does not enforce that so tests can build a bare menu).
	QuitLabel string
	Quit      func()

	// Log receives anything Run cannot surface any other way — a menu
	// action's error, the backend refusing to start, a recovered panic —
	// in the same shape as (*slog.Logger).Warn, so cmd/advisor passes that
	// method directly. Nil drops the lines.
	Log func(msg string, args ...any)
}

// Run shows the tray icon and blocks, pumping the OS's tray message loop,
// until ctx is cancelled or the user clicks Quit. It must be called from
// the process's main goroutine, never a spawned one: gogpu/systray locks
// the OS thread in its own init() (the goroutine that runs a package's
// init functions is always the one that will go on to run main()), because
// both Cocoa's and Windows' event loops require the thread the process
// started on.
//
// A backend that cannot start at all (no desktop session, no D-Bus, some
// other platform refusal) or that panics is logged and Run returns nil —
// a tray icon that cannot show itself must never take the daemon down
// with it. The caller (cmd/advisor) falls back to serving headless.
func Run(ctx context.Context, opts Options) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logWarn(opts.Log, "tray: recovered from a panic in the tray backend; continuing headless", "panic", r)
			err = nil
		}
	}()
	return newRunner(ctx, opts)
}

// newRunner is the seam tests replace with a fake that never touches a
// real OS tray (the real backend needs an actual desktop session and
// cannot run in CI). defaultRunner, in runner.go, is the real
// github.com/gogpu/systray integration.
var newRunner = defaultRunner

func logWarn(log func(msg string, args ...any), msg string, args ...any) {
	if log != nil {
		log(msg, args...)
	}
}

// autostartToggle is the pure decision behind the "Start at login"
// checkbox's click handler, pulled out of runner.go so it is unit-testable
// without a real menu item: given the checkbox's state before the click,
// try to apply the flip, and report the state to show and whether it
// stuck. A failure leaves currentlyChecked exactly as it was — the click
// must never show a state that was not actually applied (product rule 5).
func autostartToggle(currentlyChecked bool, set func(bool) error, log func(msg string, args ...any)) (newChecked bool, applied bool) {
	want := !currentlyChecked
	if err := set(want); err != nil {
		logWarn(log, "tray: could not change \"start at login\"", "err", err)
		return currentlyChecked, false
	}
	return want, true
}
