//go:build windows

package ollama

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"advisor/internal/backend"
)

// findBinary checks $PATH first (the installer adds it there), then the
// installer's own default location under %LOCALAPPDATA%.
func (b *Backend) findBinary() (string, bool) {
	if p, err := b.env.lookPath("ollama.exe"); err == nil && p != "" {
		return p, true
	}
	if lad := b.env.getenv("LOCALAPPDATA"); lad != "" {
		p := filepath.Join(lad, "Programs", "Ollama", "ollama.exe")
		if size, isDir, ok := b.env.statSize(p); ok && !isDir && size > 0 {
			return p, true
		}
	}
	return "", false
}

// osInstall downloads OllamaSetup.exe and launches it. Windows installers
// are not run silently: product rule 5 puts the explanation on the button
// before this is ever called, and the installer's own UI (Inno Setup) is
// what the user clicks through from here — the same as running the
// download by hand. Detect() reports the result once they finish.
func (b *Backend) osInstall(ctx context.Context, progress func(backend.InstallProgress)) error {
	dest := filepath.Join(os.TempDir(), "OllamaSetup.exe")
	if progress != nil {
		progress(backend.InstallProgress{Status: "downloading Ollama for Windows"})
	}
	if _, err := downloadFile(ctx, "https://ollama.com/download/OllamaSetup.exe", dest, progress); err != nil {
		return err
	}
	if progress != nil {
		progress(backend.InstallProgress{Status: "opening the installer — follow its steps to finish"})
	}
	if err := exec.Command(dest).Start(); err != nil {
		return fmt.Errorf("ollama: install: launching %s: %w", dest, err)
	}
	return nil
}

func (b *Backend) osStart(ctx context.Context) error {
	return b.startServeProcess(ctx)
}
