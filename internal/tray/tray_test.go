package tray

import (
	"context"
	"errors"
	"testing"
)

// withFakeRunner replaces newRunner for the duration of one test — the
// real github.com/gogpu/systray backend needs an actual desktop session
// (a D-Bus bus on Linux, a window server on macOS/Windows) and cannot run
// here; Run's dispatch to it, and its panic recovery, are what this file
// covers instead.
func withFakeRunner(t *testing.T, fn func(ctx context.Context, opts Options) error) {
	t.Helper()
	orig := newRunner
	newRunner = fn
	t.Cleanup(func() { newRunner = orig })
}

func TestRun_DelegatesToNewRunnerWithTheSameOptions(t *testing.T) {
	var gotTooltip string
	withFakeRunner(t, func(ctx context.Context, opts Options) error {
		gotTooltip = opts.Tooltip
		return nil
	})
	err := Run(context.Background(), Options{Tooltip: "Local LLM Advisor"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotTooltip != "Local LLM Advisor" {
		t.Errorf("Tooltip = %q, want it passed through", gotTooltip)
	}
}

func TestRun_PropagatesTheBackendsError(t *testing.T) {
	want := errors.New("boom")
	withFakeRunner(t, func(ctx context.Context, opts Options) error { return want })
	if err := Run(context.Background(), Options{}); err != want {
		t.Errorf("Run = %v, want %v", err, want)
	}
}

// A backend panic (an immature library's own bug, most plausibly) must
// never take the daemon down with it — Run recovers and returns nil, and
// logs a line rather than dropping it silently.
func TestRun_RecoversFromAPanicInTheBackend(t *testing.T) {
	withFakeRunner(t, func(ctx context.Context, opts Options) error {
		panic("simulated backend failure")
	})
	var logged []string
	err := Run(context.Background(), Options{
		Log: func(msg string, args ...any) { logged = append(logged, msg) },
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil (recovered)", err)
	}
	if len(logged) == 0 {
		t.Errorf("nothing was logged about the recovered panic")
	}
}

func TestRun_ANilLogNeverPanics(t *testing.T) {
	withFakeRunner(t, func(ctx context.Context, opts Options) error {
		panic("simulated backend failure")
	})
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
}

func TestAutostartToggle_AppliedFlipsAndReports(t *testing.T) {
	var setTo bool
	set := func(enabled bool) error {
		setTo = enabled
		return nil
	}
	newChecked, applied := autostartToggle(false, set, nil)
	if !applied {
		t.Fatalf("applied = false, want true")
	}
	if !newChecked {
		t.Errorf("newChecked = false, want true (was unchecked, clicked on)")
	}
	if !setTo {
		t.Errorf("set was called with %v, want true", setTo)
	}
}

func TestAutostartToggle_FailureLeavesStateUnchanged(t *testing.T) {
	set := func(enabled bool) error { return errors.New("no systemd here") }
	var logged []string
	newChecked, applied := autostartToggle(true, set, func(msg string, args ...any) { logged = append(logged, msg) })
	if applied {
		t.Fatalf("applied = true, want false")
	}
	if !newChecked {
		t.Errorf("newChecked = %v, want the original state (true) preserved on failure", newChecked)
	}
	if len(logged) == 0 {
		t.Errorf("a failed toggle was not logged")
	}
}
