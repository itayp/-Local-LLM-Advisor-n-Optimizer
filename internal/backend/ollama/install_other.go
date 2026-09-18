//go:build !darwin && !windows && !linux

// This file exists only so the package builds on an operating system the
// advisor daemon itself is never shipped for (D-2: linux, darwin, windows
// are the four build targets). Detect still works — the HTTP probe in
// ollama.go needs nothing OS-specific — it simply never finds a binary on
// disk, so it reports "not installed" rather than crashing.

package ollama

import (
	"context"
	"fmt"

	"advisor/internal/backend"
)

func (b *Backend) findBinary() (string, bool) { return "", false }

func (b *Backend) osInstall(context.Context, func(backend.InstallProgress)) error {
	return fmt.Errorf("ollama: install: this operating system is not supported")
}

func (b *Backend) osStart(context.Context) error {
	return fmt.Errorf("ollama: start: this operating system is not supported")
}
