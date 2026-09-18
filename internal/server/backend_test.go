package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"advisor/internal/backend"
	"advisor/internal/hardware"
	"advisor/internal/store"
)

// fakeBackend is a minimal backend.Backend for server tests: it never
// touches the network, so tests control exactly what Detect/Models return.
type fakeBackend struct {
	name       string
	status     backend.Status
	detectErr  error
	models     []backend.Installed
	modelsErr  error
	detectHits int
}

func (f *fakeBackend) Name() string { return f.name }
func (f *fakeBackend) Detect(context.Context) (backend.Status, error) {
	f.detectHits++
	return f.status, f.detectErr
}
func (f *fakeBackend) Models(context.Context) ([]backend.Installed, error) {
	return f.models, f.modelsErr
}
func (f *fakeBackend) Show(context.Context, string) (backend.ModelInfo, error) {
	return backend.ModelInfo{}, nil
}
func (f *fakeBackend) Running(context.Context) ([]backend.Loaded, error) { return nil, nil }
func (f *fakeBackend) Pull(context.Context, backend.ModelSource, func(backend.PullProgress)) error {
	return backend.ErrUnsupportedSource
}
func (f *fakeBackend) Generate(context.Context, backend.GenerateRequest, func(backend.GenerateEvent) error) error {
	return nil
}
func (f *fakeBackend) Unload(context.Context, string) error                         { return nil }
func (f *fakeBackend) Install(context.Context, func(backend.InstallProgress)) error { return nil }
func (f *fakeBackend) Start(context.Context) error                                  { return nil }

func newBackendTestServer(t *testing.T, backends ...backend.Backend) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "advisor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := New(nil, st)
	srv.backendList = func() []backend.Backend { return backends }
	return srv, st
}

func TestBackendsDistinguishesNotInstalledFromInstalledNotRunning(t *testing.T) {
	notInstalled := &fakeBackend{name: "llamacpp", status: backend.Status{State: backend.StateNotInstalled}}
	installed := &fakeBackend{name: "ollama", status: backend.Status{
		State: backend.StateInstalledNotRunning, Host: "http://127.0.0.1:11434", Detail: "found at /usr/bin/ollama",
	}}
	srv, _ := newBackendTestServer(t, notInstalled, installed)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var got BackendsResponse
	getJSON(t, ts.URL+"/api/backends", http.StatusOK, &got)
	if len(got.Backends) != 2 {
		t.Fatalf("got %d backends, want 2: %+v", len(got.Backends), got)
	}
	byName := map[string]BackendInfo{}
	for _, b := range got.Backends {
		byName[b.Name] = b
	}
	if byName["llamacpp"].State != backend.StateNotInstalled {
		t.Fatalf("llamacpp: %+v", byName["llamacpp"])
	}
	if byName["ollama"].State != backend.StateInstalledNotRunning || byName["ollama"].Detail == "" {
		t.Fatalf("ollama: %+v", byName["ollama"])
	}
}

func TestBackendsRefreshesInstalledModelsWhenRunning(t *testing.T) {
	models := []backend.Installed{
		{Name: "llama3.1:8b", Digest: "sha256:aaa", SizeBytes: 4_900_000_000, Family: "llama", ParameterSize: "8B", Quantization: "Q4_K_M"},
	}
	running := &fakeBackend{
		name:   "ollama",
		status: backend.Status{State: backend.StateRunning, Version: "0.34.2", Host: "http://127.0.0.1:11434", RuntimePaths: map[int]hardware.RuntimePath{0: hardware.PathCUDA}},
		models: models,
	}
	srv, st := newBackendTestServer(t, running)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var got BackendsResponse
	getJSON(t, ts.URL+"/api/backends", http.StatusOK, &got)
	if len(got.Backends) != 1 || got.Backends[0].State != backend.StateRunning || got.Backends[0].Version != "0.34.2" {
		t.Fatalf("backends: %+v", got)
	}
	if got.Backends[0].RuntimePaths[0] != hardware.PathCUDA {
		t.Fatalf("runtime path not carried through: %+v", got.Backends[0])
	}

	rows, err := st.InstalledModels(context.Background(), "ollama")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "llama3.1:8b" {
		t.Fatalf("installed_models was not written: %+v", rows)
	}

	var im InstalledModelsResponse
	getJSON(t, ts.URL+"/api/models/installed", http.StatusOK, &im)
	if len(im.Models) != 1 || im.Models[0].BackendName != "ollama" || im.Models[0].SizeBytes != 4_900_000_000 {
		t.Fatalf("GET /api/models/installed: %+v", im)
	}

	var filtered InstalledModelsResponse
	getJSON(t, ts.URL+"/api/models/installed?backend=ollama", http.StatusOK, &filtered)
	if len(filtered.Models) != 1 {
		t.Fatalf("filtered by backend: %+v", filtered)
	}
	var empty InstalledModelsResponse
	getJSON(t, ts.URL+"/api/models/installed?backend=llamacpp", http.StatusOK, &empty)
	if len(empty.Models) != 0 {
		t.Fatalf("a backend with nothing installed: %+v", empty)
	}
}

func TestBackendsInstalledVersionCarriesForwardAcrossChecks(t *testing.T) {
	b := &fakeBackend{name: "ollama", status: backend.Status{State: backend.StateInstalledNotRunning}}
	srv, _ := newBackendTestServer(t, b)
	ctx := context.Background()

	first := srv.RecordBackends(ctx)
	if first.Backends[0].InstalledVersion != "" {
		t.Fatalf("nothing installed this yet: %+v", first)
	}
	// Simulate this app having installed it (what install.go's flow would
	// eventually record): a later check must still say so even though
	// Detect() itself has no notion of "who installed this".
	if _, err := srv.store.RecordBackend(ctx, "ollama", backend.Status{State: backend.StateInstalledNotRunning}, "0.34.2"); err != nil {
		t.Fatal(err)
	}
	second := srv.RecordBackends(ctx)
	if second.Backends[0].InstalledVersion != "0.34.2" {
		t.Fatalf("installed version should carry forward: %+v", second)
	}
}

func TestBackendsDetectErrorFallsBackToLastKnown(t *testing.T) {
	b := &fakeBackend{name: "ollama", status: backend.Status{State: backend.StateRunning, Version: "0.34.2"}}
	srv, _ := newBackendTestServer(t, b)
	ctx := context.Background()

	first := srv.RecordBackends(ctx)
	if first.Backends[0].State != backend.StateRunning {
		t.Fatalf("first: %+v", first)
	}

	b.detectErr = errors.New("connection refused")
	second := srv.RecordBackends(ctx)
	if second.Backends[0].State != backend.StateRunning || second.Backends[0].Version != "0.34.2" {
		t.Fatalf("a failed check should report the last known state, not guess: %+v", second)
	}
	if second.Backends[0].Detail == "" {
		t.Fatal("expected the fallback to say the check failed")
	}
}

func TestBackendsEmptyRegistry(t *testing.T) {
	srv, _ := newBackendTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	var got BackendsResponse
	getJSON(t, ts.URL+"/api/backends", http.StatusOK, &got)
	if len(got.Backends) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestModelsInstalledWithNoStore(t *testing.T) {
	srv := New(nil, nil)
	srv.backendList = func() []backend.Backend { return nil }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	var got InstalledModelsResponse
	getJSON(t, ts.URL+"/api/models/installed", http.StatusOK, &got)
	if len(got.Models) != 0 {
		t.Fatalf("got %+v", got)
	}
}
