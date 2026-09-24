package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"advisor/internal/backend"
	"advisor/internal/bench"
	"advisor/internal/catalog/refresh"
	"advisor/internal/estimate"
)

// Every screen that needs the model list can tell a fresh install from a
// fetched list, and follow a fetch while it runs.
func TestCatalogStatusSaysWhetherTheListWasFetched(t *testing.T) {
	srv, ts := newCatalogTestServer(t)
	ctx := context.Background()
	if err := srv.SyncCatalogue(ctx); err != nil {
		t.Fatal(err)
	}
	var st CatalogStatus
	getJSON(t, ts.URL+"/api/catalog/status", http.StatusOK, &st)
	if st.Fetched || st.Running || st.LastRefresh != nil || st.PublicFetched || st.PublicUpdated != "Public scores have not been fetched yet." {
		t.Fatalf("a fresh install: %+v", st)
	}

	// While a refresh runs, the progress says what is being read, in words.
	srv.cat.progress.begin()
	srv.cat.progress.update(refresh.Progress{Phase: refresh.PhaseModels, Done: 1, Total: 2, Current: "Llama 3.2 3B"})
	getJSON(t, ts.URL+"/api/catalog/status", http.StatusOK, &st)
	if !st.Running || st.Progress == nil || st.Progress.Phase != "models" || st.Progress.Done != 1 || st.Progress.Total != 2 ||
		st.Progress.Message != "Reading the description of Llama 3.2 3B" || st.Progress.StartedAt == "" {
		t.Fatalf("running: %+v %+v", st, st.Progress)
	}
	srv.cat.progress.update(refresh.Progress{Phase: refresh.PhasePublic, Done: 0, Total: 3, Current: "Epoch AI Benchmarking Hub"})
	if p, _ := srv.cat.progress.snapshot(); p.Message != "Reading public scores from Epoch AI Benchmarking Hub" {
		t.Errorf("public phase: %+v", p)
	}
	srv.cat.progress.end()

	if _, err := srv.RefreshCatalog(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	// The refresh reported each size as it went.
	if p, running := srv.cat.progress.snapshot(); running || p.Phase != refresh.PhaseModels || p.Total != 2 || p.Done != 1 || !strings.Contains(p.Message, "Llama 3.2 3B") {
		t.Errorf("the refresh's last progress: %+v (running %v)", p, running)
	}
	st = CatalogStatus{}
	getJSON(t, ts.URL+"/api/catalog/status", http.StatusOK, &st)
	if !st.Fetched || st.Running || st.Progress != nil || st.LastRefresh == nil || st.LastRefresh.Resolved != 1 {
		t.Fatalf("after a refresh: %+v", st)
	}
}

// The Benchmarks screen offers what is installed and, apart, what the list
// has that would run here — with what it costs to download.
func TestBenchModelsOffersInstalledAndNot(t *testing.T) {
	ollama := &fakeBackend{name: "ollama", status: backend.Status{State: backend.StateRunning, Version: "0.34.2"}}
	_, ts := recommendTestServer(t, ollama)
	var bm BenchModelsResponse
	getJSON(t, ts.URL+"/api/bench/models", http.StatusOK, &bm)
	if !bm.CatalogueFetched || len(bm.Installed) != 0 || len(bm.Available) != 1 {
		t.Fatalf("nothing installed: %+v", bm)
	}
	a := bm.Available[0]
	if a.Name != "llama3.2:1b" || a.DisplayName != "Llama 3.2 1B" || a.Installed || a.DownloadBytes == 0 || a.ModelID == 0 || a.Fit != estimate.FitsWithHeadroom {
		t.Errorf("available: %+v", a)
	}

	// Installed (here or in a chat app): offered as installed, and no longer
	// as a download — the inventory is read afresh on every call.
	ollama.models = []backend.Installed{
		{Name: "llama3.2:1b", Family: "llama", ParameterSize: "1.2B", Quantization: "Q8_0"},
		{Name: "qwen3:4b", Family: "qwen3", ParameterSize: "4.0B", Quantization: "Q4_K_M"},
	}
	getJSON(t, ts.URL+"/api/bench/models", http.StatusOK, &bm)
	if len(bm.Installed) != 2 || bm.Installed[0].Name != "llama3.2:1b" || bm.Installed[0].DisplayName != "Llama 3.2 1B" ||
		bm.Installed[1].Name != "qwen3:4b" || bm.Installed[1].DisplayName != "" || len(bm.Available) != 0 {
		t.Errorf("installed: %+v", bm)
	}
}

// Before the list is fetched there is nothing to offer beyond what is installed, and the answer says why.
func TestBenchModelsBeforeTheListIsFetched(t *testing.T) {
	srv, ts := newCatalogTestServer(t)
	if err := srv.SyncCatalogue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var bm BenchModelsResponse
	getJSON(t, ts.URL+"/api/bench/models", http.StatusOK, &bm)
	if bm.CatalogueFetched || len(bm.Available) != 0 {
		t.Errorf("an unfetched list: %+v", bm)
	}
}

// A browser whose event stream never arrives can still follow a test.
func TestBenchProgressAsJSON(t *testing.T) {
	o := newBenchOllama()
	_, ts := benchTestServer(t, o)
	var started bench.Run
	postJSON(t, ts.URL+"/api/bench", `{"model":"llama3.2:1b","num_ctx":8192,"prompts":["500"]}`, http.StatusAccepted, &started)
	events(t, ts.URL+"/api/bench/"+itoa(started.ID)) // to its end
	var p bench.Progress
	getJSON(t, ts.URL+"/api/bench/"+itoa(started.ID)+"/progress", http.StatusOK, &p)
	if p.RunID != started.ID || p.Status != bench.StatusDone || p.Message != "Finished" || p.Run.ID != started.ID {
		t.Errorf("progress: %+v", p)
	}
	getJSON(t, ts.URL+"/api/bench/999/progress", http.StatusNotFound, nil)
}
