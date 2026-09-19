package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"advisor/internal/backend"
	"advisor/internal/hardware"
)

func TestRecordBackendKeepsHistory(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if _, err := s.LatestBackend(ctx, "ollama"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty table: %v", err)
	}

	notInstalled := backend.Status{State: backend.StateNotInstalled}
	r1, err := s.RecordBackend(ctx, "ollama", notInstalled, "")
	if err != nil {
		t.Fatal(err)
	}
	if r1.State != backend.StateNotInstalled || r1.RuntimePaths != nil || r1.Env != nil {
		t.Fatalf("r1 = %+v", r1)
	}

	running := backend.Status{
		State:   backend.StateRunning,
		Version: "0.34.2",
		Host:    "http://127.0.0.1:11434",
		RuntimePaths: map[int]hardware.RuntimePath{
			0: hardware.PathCUDA,
		},
		Env: map[string]string{"HSA_OVERRIDE_GFX_VERSION": "11.0.0"},
	}
	r2, err := s.RecordBackend(ctx, "ollama", running, "0.34.2")
	if err != nil {
		t.Fatal(err)
	}
	if r2.ID == r1.ID {
		t.Fatal("RecordBackend must insert a new row, not update the old one")
	}

	// The old row is untouched.
	var oldState string
	if err := s.DB().QueryRowContext(ctx, `SELECT state FROM backends WHERE id = ?`, r1.ID).Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if oldState != string(backend.StateNotInstalled) {
		t.Fatalf("old row's state changed: %s", oldState)
	}

	latest, err := s.LatestBackend(ctx, "ollama")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != r2.ID || latest.Version != "0.34.2" || latest.InstalledVersion != "0.34.2" ||
		latest.Host != "http://127.0.0.1:11434" {
		t.Fatalf("latest = %+v", latest)
	}
	if !reflect.DeepEqual(latest.RuntimePaths, running.RuntimePaths) {
		t.Fatalf("runtime paths round trip: got %v want %v", latest.RuntimePaths, running.RuntimePaths)
	}
	if !reflect.DeepEqual(latest.Env, running.Env) {
		t.Fatalf("env round trip: got %v want %v", latest.Env, running.Env)
	}

	if _, err := s.LatestBackend(ctx, "llamacpp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a backend that was never recorded: %v", err)
	}
}

func TestLatestBackendsListsOneRowPerName(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if _, err := s.RecordBackend(ctx, "ollama", backend.Status{State: backend.StateNotInstalled}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordBackend(ctx, "ollama", backend.Status{State: backend.StateRunning, Version: "0.34.2"}, "0.34.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordBackend(ctx, "llamacpp", backend.Status{State: backend.StateUnsupported}, ""); err != nil {
		t.Fatal(err)
	}

	rows, err := s.LatestBackends(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(rows), rows)
	}
	if rows[0].Name != "llamacpp" || rows[0].State != backend.StateUnsupported {
		t.Fatalf("rows[0] = %+v", rows[0])
	}
	if rows[1].Name != "ollama" || rows[1].State != backend.StateRunning || rows[1].Version != "0.34.2" {
		t.Fatalf("rows[1] = %+v", rows[1])
	}
}

// What the runtime was seen to do is remembered per hardware: the last
// check that caught a loaded model answers for this fingerprint, later
// checks that saw nothing do not erase it, and another machine's — or an
// unattributed row's — observation never counts.
func TestLastObservedRuntimePathsIsPerHardware(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	seen := func(p hardware.RuntimePath) backend.Status {
		return backend.Status{State: backend.StateRunning, RuntimePaths: map[int]hardware.RuntimePath{0: p}}
	}
	idle := backend.Status{State: backend.StateRunning}

	if _, err := s.LastObservedRuntimePaths(ctx, "ollama", "v1-new"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nothing recorded: %v", err)
	}
	for _, rec := range []struct {
		fingerprint string
		status      backend.Status
	}{
		{"v1-old", seen(hardware.PathCPU)}, // another graphics card, before the swap
		{"", seen(hardware.PathVulkan)},    // detection had not finished: counts for no hardware
		{"v1-new", seen(hardware.PathCUDA)},
		{"v1-new", idle}, // the model has been unloaded since
	} {
		if _, err := s.RecordBackendOn(ctx, rec.fingerprint, "ollama", rec.status, ""); err != nil {
			t.Fatal(err)
		}
	}
	row, err := s.LastObservedRuntimePaths(ctx, "ollama", "v1-new")
	if err != nil || row.RuntimePaths[0] != hardware.PathCUDA || row.HardwareFingerprint != "v1-new" {
		t.Errorf("this hardware: %+v %v", row, err)
	}
	if row, err := s.LastObservedRuntimePaths(ctx, "ollama", "v1-old"); err != nil || row.RuntimePaths[0] != hardware.PathCPU {
		t.Errorf("the old hardware keeps its own observation: %+v %v", row, err)
	}
	if _, err := s.LastObservedRuntimePaths(ctx, "ollama", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown fingerprint matches nothing, got %v", err)
	}
	if _, err := s.LastObservedRuntimePaths(ctx, "llamacpp", "v1-new"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another backend: %v", err)
	}
}
