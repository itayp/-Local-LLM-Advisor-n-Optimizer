//go:build !darwin && !windows && !linux

// This file exists only so the package builds on an operating system the
// advisor is never shipped for (D-2: linux, darwin, windows are the three
// build targets) — the same reason internal/chatapps/locations_other.go and
// internal/backend/ollama/install_other.go exist. No notification is ever
// shown there; the watch log still has the line.
package watch

import (
	"context"
	"errors"
)

func (n *osNotifier) Notify(ctx context.Context, note Notification) error {
	return errors.New("watch: desktop notifications are not supported on this operating system")
}
