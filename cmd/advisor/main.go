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

	"advisor/data/icon"
	"advisor/internal/autostart"
	"advisor/internal/backend"
	"advisor/internal/hardware"
	"advisor/internal/server"
	"advisor/internal/store"
	"advisor/internal/tray"
	"advisor/internal/version"
	"advisor/internal/winapp"

	_ "advisor/internal/backend/ollama" // registers itself with internal/backend on import
)

// appDisplayName is what the tray menu and "start at login" entries call
// this program — the same name ui/src/copy/en.ts's app.title uses, kept
// here as a literal rather than shared code because there is nothing to
// import: this is native OS text, never rendered by the browser UI.
const appDisplayName = "Local LLM Advisor"

// appLaunchedBeforeKey gates the one-time-only browser open in tray mode
// (BUILD_PLAN.md step 11: "first launch opens the browser at the app;
// later launches just start the daemon") — the same generic settings
// table internal/server/settings.go's own keys use, so no new migration.
// It is only ever consulted when -tray is set: a plain terminal invocation
// (make dev, a developer's own build) always opens the browser, exactly as
// it always has.
const appLaunchedBeforeKey = "app.launched_before"

func main() {
	if isCatalogCommand(os.Args) {
		os.Exit(runCatalog(os.Args[2:], os.Stdout, os.Stderr))
	}
	if isRecommendCommand(os.Args) {
		os.Exit(runRecommend(os.Args[2:], os.Stdout, os.Stderr))
	}
	if isBenchCommand(os.Args) {
		os.Exit(runBench(os.Args[2:], os.Stdout, os.Stderr))
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
		flagTray      = flag.Bool("tray", false, "show a tray icon and menu instead of a plain terminal process (build-plan step 11: the packaged app's launchers set this; make dev and a bare invocation leave it off)")
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

	// A no-op on macOS and Linux; on Windows, gives this process a real
	// identity (internal/winapp) so a toast notification can be attributed
	// to it (claude/backlog.md item (k)) whether it was launched from the
	// Start Menu, the "start at login" registry entry, or a bare
	// double-click. HKCU-only: never fatal, never needs admin.
	if err := winapp.RegisterIdentity(); err != nil {
		log.Warn("registering this process's Windows identity", "err", err)
	}

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
	// This start's own address, for a watch notification's deep link
	// (build-plan step 10, item 3 — "that model's card, with 'Run
	// benchmark'"). Set before the scheduler can possibly fire.
	srv.SetWatchBaseURL(url)
	// A benchmark the previous start was running did not finish: say so in
	// its row rather than leave it "running" forever.
	srv.RecoverBenchmarks(ctx)
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

	// The new-model watch (build-plan step 10): a daily, jittered scheduler
	// that refreshes the catalogue, checks every curated size against this
	// machine, and notifies once per model, ever — see internal/watch and
	// internal/server/watch.go. It never pulls or switches anything itself
	// (product rule 5); the master switch and the notification mode are the
	// Settings screen's, read fresh on every tick.
	go srv.WatchScheduler(ctx)

	if *flagTray {
		// First launch ever (in tray mode) opens the browser once; every
		// launch after that just starts the daemon and waits at the tray
		// icon — BUILD_PLAN.md step 11. A plain (non-tray) invocation,
		// below, is unaffected and always opens the browser as it always
		// has (make dev, a developer's own terminal use).
		first := true
		if v, ok, err := st.Setting(ctx, appLaunchedBeforeKey); err != nil {
			log.Warn("reading whether this is the first tray launch", "err", err)
		} else {
			first = !ok || v != "1"
		}
		if first && !*flagNoBrowser {
			if err := openBrowser(url); err != nil {
				log.Warn("could not open a browser; open the address yourself", "url", url, "err", err)
			}
			if err := st.SetSetting(ctx, appLaunchedBeforeKey, "1"); err != nil {
				log.Warn("recording that the first launch happened", "err", err)
			}
		}

		runTray(ctx, log, stop, url)
		// tray.Run only returns once ctx is cancelled (Quit, or the tray
		// backend could not start at all and it fell back to headless —
		// either way the select below is the same shutdown wait as the
		// non-tray path).
	} else if !*flagNoBrowser {
		// Open the browser exactly once, after the listener is up. If that
		// fails (no desktop, no browser), the URL is in the log and the
		// daemon runs on.
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

// runTray shows the tray icon and blocks until ctx is cancelled or the
// user clicks Quit (internal/tray). It must be called from this goroutine
// — main()'s own, never a spawned one — because the tray backend locks
// the OS thread it was imported on (internal/tray's own doc comment).
func runTray(ctx context.Context, log *slog.Logger, stop context.CancelFunc, url string) {
	target := autostart.Target{Name: appDisplayName, Args: []string{"-tray"}}
	var autostartEnabled func() bool
	var autostartSet func(bool) error
	if execPath, err := os.Executable(); err != nil {
		// "Start at login" is simply not offered — a tray with two menu
		// items instead of three is a smaller failure than a crash, and
		// there is nothing the user could do about this anyway.
		log.Warn(`could not find this program's own path; "start at login" will not be offered`, "err", err)
	} else {
		target.ExecPath = execPath
		autostartEnabled = func() bool {
			enabled, err := autostart.Enabled(ctx, target)
			if err != nil {
				log.Warn(`checking "start at login"`, "err", err)
			}
			return enabled
		}
		autostartSet = func(enabled bool) error {
			if enabled {
				return autostart.Enable(ctx, target)
			}
			return autostart.Disable(ctx, target)
		}
	}

	err := tray.Run(ctx, tray.Options{
		Tooltip:   appDisplayName,
		AppName:   appDisplayName,
		Icon:      tray.Icon{PNG: icon.Tray, Template: icon.TrayTemplate},
		OpenLabel: "Open " + appDisplayName,
		Open: func() {
			if err := openBrowser(url); err != nil {
				log.Warn("could not open a browser; open the address yourself", "url", url, "err", err)
			}
		},
		AutostartLabel:   "Start at login",
		AutostartEnabled: autostartEnabled,
		AutostartSet:     autostartSet,
		QuitLabel:        "Quit",
		Quit:             stop,
		Log:              log.Warn,
	})
	if err != nil {
		log.Warn("tray: stopped", "err", err)
	}
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
