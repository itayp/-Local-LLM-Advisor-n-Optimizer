// Command advisor is the Local LLM Advisor & Optimizer daemon.
//
// It opens the local database, starts the HTTP server on 127.0.0.1, opens
// the user's browser at it once, and runs until interrupted. Everything the
// product does happens behind that address; this file only wires it up.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"advisor/internal/backend"
	"advisor/internal/hardware"
	"advisor/internal/server"
	"advisor/internal/store"
	"advisor/internal/version"

	_ "advisor/internal/backend/ollama" // registers itself with internal/backend on import
)

func main() {
	if isCatalogCommand(os.Args) {
		os.Exit(runCatalog(os.Args[2:], os.Stdout, os.Stderr))
	}
	if isRecommendCommand(os.Args) {
		os.Exit(runRecommend(os.Args[2:], os.Stdout, os.Stderr))
	}
	os.Exit(run())
}

func run() int {
	var (
		flagPort      = flag.Int("port", server.DefaultPort, "port on 127.0.0.1 to listen on (0 = let the OS choose); the address itself is not configurable")
		flagDataDir   = flag.String("data-dir", "", "where the daemon keeps its data (default: the OS's application-data folder, or $ADVISOR_DATA_DIR)")
		flagNoBrowser = flag.Bool("no-browser", false, "do not open the browser on start (make dev sets this; Vite opens its own)")
		flagVersion   = flag.Bool("version", false, "print the version and exit")
		flagVerbose   = flag.Bool("v", false, "debug logging")
	)
	flag.Parse()

	if *flagVersion {
		fmt.Printf("advisor %s (%s/%s, %s)\n", version.Version, runtime.GOOS, runtime.GOARCH, version.GoVersion())
		return 0
	}

	level := slog.LevelInfo
	if *flagVerbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Data directory and database.
	dbPath := ""
	if *flagDataDir != "" {
		dbPath = filepath.Join(*flagDataDir, "advisor.db")
	} else {
		p, err := store.DefaultPath()
		if err != nil {
			log.Error("cannot decide where to keep data", "err", err)
			return 1
		}
		dbPath = p
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		log.Error("opening the database", "path", dbPath, "err", err)
		return 1
	}
	defer st.Close()
	schema, _ := st.SchemaVersion(ctx)
	log.Info("database ready", "path", dbPath, "schema", schema)

	// Listen on the loopback address — the only one there is.
	l, err := server.Listen(*flagPort)
	if err != nil && *flagPort != 0 {
		log.Warn("port is taken; asking the OS for another", "port", *flagPort, "err", err)
		l, err = server.Listen(0)
	}
	if err != nil {
		log.Error("cannot listen on the loopback address", "err", err)
		return 1
	}
	url := "http://" + l.Addr().String() + "/"
	log.Info("advisor is listening", "url", url, "version", version.Version)

	srv := server.New(log, st)
	// The curated catalogue (families.yaml, embedded) into catalog_models:
	// no network, so a new build's catalogue and the installed-model
	// mapping are current from its first start. Resolving it against
	// Hugging Face is `advisor catalog refresh` / POST /api/catalog/refresh.
	if err := srv.SyncCatalogue(ctx); err != nil {
		log.Warn("syncing the curated catalogue", "err", err)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ctx, l) }()

	// The hardware profile: detected in the background once the listener is
	// up (system_profiler and PowerShell take seconds, and the browser should
	// not wait for them), stored as a new row on every start so benchmarks
	// stay attributable to the hardware they ran on, then served at
	// GET /api/hardware.
	go func() {
		resp, err := srv.RecordHardware(ctx, hardware.Detect)
		if err != nil {
			log.Warn("hardware detection did not finish", "err", err)
			return
		}
		p := resp.Profile
		log.Info("machine", "tier", p.Tier, "summary", p.Summary, "profile_id", resp.ProfileID, "changed", resp.Changed)
		for _, problem := range p.Problems {
			log.Debug("hardware detection", "problem", problem)
		}
	}()

	// The runtime inventory: detected in the background on the same
	// schedule as hardware (every backend registered with internal/backend
	// — "ollama" for now), stored, and served at GET /api/backends and
	// GET /api/models/installed (step 3, item 4). This log line is
	// temporary — a UI screen for it is a later step's job, not this one's.
	go func() {
		resp := srv.RecordBackends(ctx)
		for _, b := range resp.Backends {
			switch b.State {
			case backend.StateRunning:
				fmt.Fprintf(os.Stderr, "advisor: backend %s: running %s at %s\n", b.Name, b.Version, b.Host)
			case backend.StateInstalledNotRunning:
				fmt.Fprintf(os.Stderr, "advisor: backend %s: installed but not running (%s)\n", b.Name, b.Detail)
			case backend.StateNotInstalled:
				fmt.Fprintf(os.Stderr, "advisor: backend %s: not installed\n", b.Name)
			default:
				fmt.Fprintf(os.Stderr, "advisor: backend %s: %s (%s)\n", b.Name, b.State, b.Detail)
			}
			log.Info("backend", "name", b.Name, "state", b.State, "version", b.Version,
				"installed_version", b.InstalledVersion, "runtime_paths", b.RuntimePaths)
		}
	}()

	// Open the browser exactly once, after the listener is up. If that fails
	// (no desktop, no browser), the URL is in the log and the daemon runs on.
	if !*flagNoBrowser {
		if err := openBrowser(url); err != nil {
			log.Warn("could not open a browser; open the address yourself", "url", url, "err", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "advisor: listening at %s (browser not opened)\n", url)
	}

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		err = <-errc
	case err = <-errc:
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		log.Error("server stopped", "err", err)
		return 1
	}
	return 0
}

// openBrowser asks the OS to open url in the default browser. It returns
// once the opener has been launched; it does not wait for the browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the opener in the background; a hung opener must not hang us.
	go func() {
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()
	return nil
}
