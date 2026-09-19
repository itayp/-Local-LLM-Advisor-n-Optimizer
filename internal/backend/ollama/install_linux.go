//go:build linux

package ollama

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"advisor/internal/backend"
)

// findBinary checks $PATH first (a distro package, or an install the user
// made themselves outside this app), then this app's own managed install
// under its data folder.
func (b *Backend) findBinary() (string, bool) {
	if p, err := b.env.lookPath("ollama"); err == nil && p != "" {
		return p, true
	}
	dir, err := dataDir()
	if err != nil {
		return "", false
	}
	p := filepath.Join(dir, "ollama", "bin", "ollama")
	if size, isDir, ok := b.env.statSize(p); ok && !isDir && size > 0 {
		return p, true
	}
	return "", false
}

// goarchForTest exists so TestOllamaArchRejectsUnshippedArchitectures can
// exercise the error path without needing to actually run on an
// unsupported architecture (the same seam internal/hardware uses for
// runtime.GOOS/GOARCH).
var goarchForTest = runtime.GOARCH

func ollamaArch() (string, error) {
	switch goarchForTest {
	case "amd64", "arm64":
		return goarchForTest, nil
	default:
		return "", fmt.Errorf("ollama: install: %s is not a Linux architecture Ollama ships", goarchForTest)
	}
}

// osInstall is a full user-space install: the official tarball, extracted
// under this app's own data folder, run later as a child process this
// daemon supervises (osStart). No sudo, no curl | sh, no system service —
// CLAUDE.md's Linux convention and product rule 1 ("the user never needs a
// terminal"). The trade-off, stated where it is made: there is no
// start-at-boot outside the app, because that would need a system service
// and a password prompt. For the customer this product is for — someone
// who "has heard you can run AI on their own computer... has no idea what
// a GGUF is" (CLAUDE.md) — never asking for a password wins over surviving
// a reboot unattended.
// installURL is where osInstall downloads from, and what InstallSize
// (install.go) asks about before the button is ever clicked (product rule 5).
func (b *Backend) installURL() (string, error) {
	arch, err := ollamaArch()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://ollama.com/download/ollama-linux-%s.tar.zst", arch), nil
}

func (b *Backend) osInstall(ctx context.Context, progress func(backend.InstallProgress)) error {
	arch, err := ollamaArch()
	if err != nil {
		return err
	}
	url, err := b.installURL()
	if err != nil {
		return err
	}
	dir, err := dataDir()
	if err != nil {
		return fmt.Errorf("ollama: install: %w", err)
	}
	installDir := filepath.Join(dir, "ollama")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("ollama: install: %w", err)
	}

	archivePath := filepath.Join(os.TempDir(), "ollama-linux-"+arch+".tar.zst")
	if progress != nil {
		progress(backend.InstallProgress{Status: "downloading Ollama for Linux (" + arch + ")"})
	}
	if _, err := downloadFile(ctx, url, archivePath, progress); err != nil {
		return err
	}
	defer os.Remove(archivePath)

	if progress != nil {
		progress(backend.InstallProgress{Status: "extracting"})
	}
	// The official archive lays out ./bin/ollama and ./lib/ollama/* (docs.
	// ollama.com/linux's manual-install instructions extract it over /usr
	// for exactly that reason); GNU tar 1.31+ and bsdtar both understand
	// --zstd, so no separate decompression step or dependency is needed.
	cmd := exec.CommandContext(ctx, "tar", "--zstd", "-xf", archivePath, "-C", installDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ollama: install: extracting %s: %w: %s", archivePath, err, truncate(string(out), 500))
	}

	bin := filepath.Join(installDir, "bin", "ollama")
	if size, isDir, ok := b.env.statSize(bin); !ok || isDir || size == 0 {
		return fmt.Errorf("ollama: install: extracted the archive but %s is missing", bin)
	}
	if progress != nil {
		progress(backend.InstallProgress{Status: "installed"})
	}
	return nil
}

func (b *Backend) osStart(ctx context.Context) error {
	return b.startServeProcess(ctx)
}
