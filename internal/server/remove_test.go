package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"advisor/internal/backend"
)

func TestRemoveDeletesFromTheBackendAndTheInventory(t *testing.T) {
	fb := &fakeBackend{
		name:   "ollama",
		status: backend.Status{State: backend.StateRunning},
		models: []backend.Installed{
			{Name: "llama3.1:8b", Digest: "sha256:aaa", SizeBytes: 4_900_000_000},
			{Name: "qwen3:4b", Digest: "sha256:bbb", SizeBytes: 2_500_000_000},
		},
	}
	srv, _ := newBackendTestServer(t, fb)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Seed the inventory the way RecordBackends would, so the removal has
	// something to resync away.
	var before BackendsResponse
	getJSON(t, ts.URL+"/api/backends", http.StatusOK, &before)

	var out InstalledModelsResponse
	postJSON(t, ts.URL+"/api/backends/ollama/models/remove", `{"name": "llama3.1:8b"}`, http.StatusOK, &out)

	if len(fb.deleted) != 1 || fb.deleted[0] != "llama3.1:8b" {
		t.Fatalf("deleted = %v, want [\"llama3.1:8b\"]", fb.deleted)
	}
	if len(out.Models) != 1 || out.Models[0].Name != "qwen3:4b" {
		t.Fatalf("installed models after removal = %+v, want only qwen3:4b left", out.Models)
	}

	var again InstalledModelsResponse
	getJSON(t, ts.URL+"/api/models/installed", http.StatusOK, &again)
	if len(again.Models) != 1 || again.Models[0].Name != "qwen3:4b" {
		t.Fatalf("GET /api/models/installed after removal = %+v", again.Models)
	}
}

func TestRemoveRequiresAName(t *testing.T) {
	fb := &fakeBackend{name: "ollama", status: backend.Status{State: backend.StateRunning}}
	srv, _ := newBackendTestServer(t, fb)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	postJSON(t, ts.URL+"/api/backends/ollama/models/remove", `{"name": ""}`, http.StatusBadRequest, nil)
	postJSON(t, ts.URL+"/api/backends/ollama/models/remove", `not json`, http.StatusBadRequest, nil)
}

func TestRemoveOnUnknownBackend(t *testing.T) {
	srv, _ := newBackendTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	postJSON(t, ts.URL+"/api/backends/llamacpp/models/remove", `{"name": "x"}`, http.StatusNotFound, nil)
}

func TestRemoveReportsWhatTheBackendSaid(t *testing.T) {
	fb := &fakeBackend{
		name:      "ollama",
		status:    backend.Status{State: backend.StateRunning},
		deleteErr: errors.New("disk error"),
	}
	srv, _ := newBackendTestServer(t, fb)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	postJSON(t, ts.URL+"/api/backends/ollama/models/remove", `{"name": "llama3.1:8b"}`, http.StatusInternalServerError, nil)
}
