// Package autostart turns "start at login" on and off for the daemon
// (build-plan step 11) — the mechanism the tray menu's checkbox and the
// Windows installer's own "run at login" task both drive, so there is one
// source of truth instead of two ways of writing the same intent
// (RELEASING.md, internal/tray).
//
// One file per OS, in the same spirit as internal/hardware's per-OS probes:
// the darwin and linux implementations are plain file/exec code with no
// build tag, so their logic runs and is tested on any host; only the
// Windows registry access needs a build tag, because
// golang.org/x/sys/windows/registry itself only compiles under
// GOOS=windows — its pure logic (the command string written, the value
// name) is still split out into a portable file so it, too, is tested
// everywhere (windows_logic.go).
package autostart

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// Target is what should start automatically at login.
type Target struct {
	// Name is a short, human display name: the systemd unit's
	// Description= line on Linux. It plays no part in identifying the
	// entry (see the fixed identifiers below) — only in what a user
	// inspecting the unit/plist/registry value would read.
	Name string
	// ExecPath is the absolute path to the installed binary.
	ExecPath string
	// Args are appended after ExecPath, each its own argument — quoted
	// correctly for the OS's own autostart mechanism, never shell-joined
	// by the caller.
	Args []string
}

// errUnsupported is returned on any OS this package has no implementation
// for. cmd/advisor only offers the tray's "start at login" checkbox when
// this is nil (main.go), so a customer never sees it fail.
var errUnsupported = errors.New("autostart: not supported on this operating system")

// Enabled reports whether Target is currently set to start at login.
func Enabled(ctx context.Context, t Target) (bool, error) {
	switch runtime.GOOS {
	case "darwin":
		return darwinEnabled(os.UserHomeDir)
	case "linux":
		return linuxEnabled(ctx, realRunner{})
	case "windows":
		return windowsEnabled(t)
	default:
		return false, errUnsupported
	}
}

// Enable turns it on.
func Enable(ctx context.Context, t Target) error {
	switch runtime.GOOS {
	case "darwin":
		return darwinEnable(os.UserHomeDir, t)
	case "linux":
		return linuxEnable(ctx, os.UserHomeDir, realRunner{}, t)
	case "windows":
		return windowsEnable(t)
	default:
		return errUnsupported
	}
}

// Disable turns it off. Disabling something that was never enabled is not
// an error on any OS this package implements.
func Disable(ctx context.Context, t Target) error {
	switch runtime.GOOS {
	case "darwin":
		return darwinDisable(os.UserHomeDir)
	case "linux":
		return linuxDisable(ctx, realRunner{})
	case "windows":
		return windowsDisable()
	default:
		return errUnsupported
	}
}

// cmdRunner is the seam linux.go runs systemctl through; tests replace it
// with a fake so nothing here ever shells out for real.
type cmdRunner interface {
	run(ctx context.Context, name string, args ...string) (output string, err error)
}

type realRunner struct{}

func (realRunner) run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
