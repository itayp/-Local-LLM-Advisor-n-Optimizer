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
