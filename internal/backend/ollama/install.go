package ollama

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"advisor/internal/backend"
)

// downloadHost is the only host Install downloads from — CLAUDE.md's
// Network convention names "the Ollama download host" as an allow-listed
// destination as of this step; this constant is that host (step 12 moves
// it into the allow-list file the convention describes).
const downloadHost = "ollama.com"

// downloadFile fetches rawURL (which must resolve to downloadHost over
// https) to destPath, reporting progress in bytes.
//
// What this verifies, and what it does not: the host and scheme are
// checked before any request is made, and net/http's default TLS
// verification is never disabled (CLAUDE.md's proxy guidance: never skip
// it). It does not check a published checksum. Checked 2026-09-18:
// ollama.com's /download/Ollama.dmg, /download/OllamaSetup.exe and
// /download/ollama-linux-<arch>.tar.zst carry no checksum or signature
// file at a predictable, fixed URL to verify against. That is stated here
// rather than silently skipped (D-21: unknown is unknown) — if Ollama
// starts publishing one, this is where to add checking it.
func downloadFile(ctx context.Context, rawURL, destPath string, progress func(backend.InstallProgress)) (int64, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("ollama: install: bad URL %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return 0, fmt.Errorf("ollama: install: refusing a non-https URL: %s", rawURL)
	}
	if u.Hostname() != downloadHost {
		return 0, fmt.Errorf("ollama: install: refusing to download from %q, only %q is allowed", u.Hostname(), downloadHost)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	// No client timeout: an installer download can be a few hundred MB on a
	// slow line; ctx is still the way to cancel it.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return 0, fmt.Errorf("ollama: install: downloading %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("ollama: install: %s: %s", rawURL, resp.Status)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("ollama: install: creating %s: %w", destPath, err)
	}
	defer f.Close()

	total := resp.ContentLength
	var written int64
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return written, fmt.Errorf("ollama: install: writing %s: %w", destPath, werr)
			}
			written += int64(n)
			if progress != nil {
				progress(backend.InstallProgress{Status: "downloading", Completed: written, Total: total})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return written, fmt.Errorf("ollama: install: downloading %s: %w", rawURL, rerr)
		}
	}
	return written, nil
}

// Install downloads and sets up Ollama itself. Product rule 5: this only
// ever runs from a UI button that already told the user what will happen;
// Install changes nothing on the machine until it is called. The per-OS
// approach is osInstall (install_darwin.go, install_windows.go,
// install_linux.go): on macOS and Windows it downloads the official
// installer and opens it — the user still clicks through it, same as
// downloading it by hand — and Detect() picks up the result afterwards; on
// Linux it is a full user-space install (no sudo, no curl | sh, no system
// service), because there is no installer to click through in the first
// place.
func (b *Backend) Install(ctx context.Context, progress func(backend.InstallProgress)) error {
	return b.osInstall(ctx, progress)
}

// Start launches Ollama (`ollama serve`) so a subsequent Detect can report
// StateRunning. Its output is captured to this daemon's own data folder so
// runtimePaths (runtimepath.go) can read this exact run's device-discovery
// lines instead of guessing at Ollama's default log location.
func (b *Backend) Start(ctx context.Context) error {
	return b.osStart(ctx)
}

// dataDir mirrors store.DefaultDataDir's per-OS convention
// (ADVISOR_DATA_DIR override, then each OS's application-data folder). It
// is deliberately duplicated here rather than imported: ARCHITECTURE.md
// D-11's dependency direction is one way — server imports store and
// backend, backend never imports store — and this OS-folder convention is
// the one place backend needs to agree with store on where the daemon's
// data lives (D-16: the Linux user-space Ollama install goes under the same
// data folder store.DefaultDataDir() returns).
func dataDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("ADVISOR_DATA_DIR")); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("ollama: cannot find the home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Advisor"), nil
	case "windows":
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "Advisor"), nil
		}
		return filepath.Join(home, "AppData", "Local", "Advisor"), nil
	default:
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			return filepath.Join(v, "advisor"), nil
		}
		return filepath.Join(home, ".local", "share", "advisor"), nil
	}
}

// startServeProcess launches "<binary> serve" detached from this process,
// with stdout/stderr captured to a log file under this daemon's data
// folder. It is shared by every OS's osStart: Ollama's `serve` subcommand
// is the one documented, cross-platform way to run the server directly,
// whether or not the platform also has its own auto-starting background
// app.
func (b *Backend) startServeProcess(ctx context.Context) error {
	path, ok := b.findBinary()
	if !ok {
		return fmt.Errorf("ollama: start: not installed")
	}
	dir, err := dataDir()
	if err != nil {
		return fmt.Errorf("ollama: start: %w", err)
	}
	logDir := filepath.Join(dir, "ollama", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("ollama: start: %w", err)
	}
	logPath := filepath.Join(logDir, "server-supervised.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("ollama: start: opening %s: %w", logPath, err)
	}

	cmd := exec.Command(path, "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("ollama: start: launching %s serve: %w", path, err)
	}
	// This is Ollama's own long-running process, not a child we wait on
	// here; reap it in the background so it never becomes a zombie once it
	// eventually exits, and close the log file at that point.
	go func() {
		_ = cmd.Wait()
		logFile.Close()
	}()
	b.setSupervisedLog(logPath)
	return nil
}
