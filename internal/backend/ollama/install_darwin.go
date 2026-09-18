//go:build darwin

package ollama

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"advisor/internal/backend"
)

// findBinary checks $PATH first (a Homebrew install, or the Mac app's own
// "install the ollama command" offer, which places a symlink), then the
// binary Ollama.app carries internally — which works whether or not the
// user ever accepted that offer.
func (b *Backend) findBinary() (string, bool) {
	if p, err := b.env.lookPath("ollama"); err == nil && p != "" {
		return p, true
	}
	candidates := []string{
		"/Applications/Ollama.app/Contents/Resources/ollama",
		"/usr/local/bin/ollama",
	}
	if home, err := b.env.userHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "Applications", "Ollama.app", "Contents", "Resources", "ollama"))
	}
	for _, c := range candidates {
		if size, isDir, ok := b.env.statSize(c); ok && !isDir && size > 0 {
			return c, true
		}
	}
	return "", false
}

// osInstall downloads Ollama.dmg and opens it. macOS installers are not run
// silently: product rule 5 puts the explanation on the button before this
// is ever called, and from here the user still drags Ollama into
// Applications and opens it themselves, same as if they had downloaded it
// by hand. Detect() reports the result once they do.
func (b *Backend) osInstall(ctx context.Context, progress func(backend.InstallProgress)) error {
	dest := filepath.Join(os.TempDir(), "Ollama.dmg")
	if progress != nil {
		progress(backend.InstallProgress{Status: "downloading Ollama for macOS"})
	}
	if _, err := downloadFile(ctx, "https://ollama.com/download/Ollama.dmg", dest, progress); err != nil {
		return err
	}
	if progress != nil {
		progress(backend.InstallProgress{Status: "opening the installer — drag Ollama into Applications, then open it"})
	}
	if err := exec.CommandContext(ctx, "open", dest).Start(); err != nil {
		return fmt.Errorf("ollama: install: opening %s: %w", dest, err)
	}
	return nil
}

func (b *Backend) osStart(ctx context.Context) error {
	return b.startServeProcess(ctx)
}
