package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"advisor/internal/backend"
	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/hardware"
	"advisor/internal/recommend"
)

// recommendTestServer is a daemon with a refreshed (one-model) catalogue, a
// detected RTX 3090 and a fake Ollama.
func recommendTestServer(t *testing.T, ollama *fakeBackend) (*Server, *httptest.Server) {
	t.Helper()
	srv, ts := newCatalogTestServer(t, ollama)
	ctx := context.Background()
	if err := srv.SyncCatalogue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.RefreshCatalog(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.RecordHardware(ctx, detectReturns(profileWith("NVIDIA GeForce RTX 3090", "10de:2204", 24<<30))); err != nil {
		t.Fatal(err)
	}
	srv.RecordBackends(ctx)
	return srv, ts
}

func TestRecommendEndpoint(t *testing.T) {
	ollama := &fakeBackend{name: "ollama", status: backend.Status{State: backend.StateRunning, Version: "0.34.2"}}
	_, ts := recommendTestServer(t, ollama)

	var res recommend.Result
	getJSON(t, ts.URL+"/api/recommend?purposes=chat,writing", http.StatusOK, &res)
	if len(res.Purposes) != 2 || len(res.Recommendations) != 1 {
		t.Fatalf("GET /api/recommend: %+v", res)
	}
	r := res.Recommendations[0]
	if r.PullName != "llama3.2:1b" || r.File.Quant != "Q8_0" || r.NumCtx != 8192 || r.DownloadBytes == 0 {
		t.Errorf("recommendation: %+v", r)
	}
	if r.Estimate.Category != estimate.FitsWithHeadroom || r.Estimate.Request.RuntimePath != hardware.PathCUDA || r.Estimate.BudgetBytes != 24<<30 {
		t.Errorf("estimate: %+v", r.Estimate)
	}
	if r.Speed == nil || r.Speed.Source != figure.Estimated || !r.Speed.IsRange() {
		t.Errorf("headline speed should be an estimated range: %+v", r.Speed)
	}
	if r.Confidence != recommend.ConfidenceMedium || !strings.Contains(r.ConfidenceWhy, "expected, not seen") || len(r.Reasons) < 4 {
		t.Errorf("confidence %q (%s), reasons %+v", r.Confidence, r.ConfidenceWhy, r.Reasons)
	}
	if res.RuntimePath != "cuda" || res.PathSource != estimate.PathExpected || res.Warning != "" {
		t.Errorf("result: path %q (%s), warning %q", res.RuntimePath, res.PathSource, res.Warning)
	}

	// No purposes means everyday chat; a purpose nothing serves is an honest empty list.
	getJSON(t, ts.URL+"/api/recommend", http.StatusOK, &res)
	if len(res.Purposes) != 1 || res.Purposes[0] != "chat" || len(res.Recommendations) != 1 {
		t.Errorf("no purposes: %+v", res)
	}
	getJSON(t, ts.URL+"/api/recommend?purposes=coding", http.StatusOK, &res)
	if len(res.Recommendations) != 0 || res.Empty == "" {
		t.Errorf("coding, from a catalogue with no coding family: %+v", res)
	}

	getJSON(t, ts.URL+"/api/recommend?purposes=poetry", http.StatusBadRequest, nil)
	getJSON(t, ts.URL+"/api/recommend?min_context=lots", http.StatusBadRequest, nil)
	resp, err := http.Post(ts.URL+"/api/recommend", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/recommend: %d, want 405", resp.StatusCode)
	}
}

// The path the runtime was SEEN taking decides what the numbers are built
// for — and an observation outlives the checks that come after it, which
// record nothing because no model is loaded.
func TestRecommendUsesTheLastObservedRuntimePath(t *testing.T) {
	ollama := &fakeBackend{name: "ollama", status: backend.Status{
		State: backend.StateRunning, Version: "0.34.2",
		RuntimePaths: map[int]hardware.RuntimePath{0: hardware.PathCPU},
		Env:          map[string]string{"OLLAMA_VULKAN": "0"},
	}}
	srv, ts := recommendTestServer(t, ollama)

	ollama.status.RuntimePaths = nil // the model has been unloaded since
	srv.RecordBackends(context.Background())

	var res recommend.Result
	getJSON(t, ts.URL+"/api/recommend?purposes=chat", http.StatusOK, &res)
	if res.RuntimePath != "cpu" || res.PathSource != estimate.PathEstablished || res.GPUNotUsed == nil || !strings.Contains(res.Warning, "RTX 3090") {
		t.Fatalf("result: %+v", res)
	}
	first := res.Recommendations[0].Reasons[0]
	if first.Kind != "warning" || first.Explainer != recommend.ExplainerGPUNotUsed || first.Detail == "" ||
		first.Text != "Your graphics card is not being used by Ollama; these are the numbers without it." {
		t.Errorf("first reason: %+v", first)
	}
	if est := res.Recommendations[0].Estimate; est.Request.RuntimePath != hardware.PathCPU || est.BudgetKind != estimate.BudgetSystem {
		t.Errorf("estimate not built for the processor: %+v", est)
	}
}

// srv2 is a second daemon start on the same database.
func srv2(t *testing.T, first *Server) *Server {
	t.Helper()
	s := New(nil, first.store)
	s.backendList = first.backendList
	s.cat.load, s.cat.newClient = first.cat.load, first.cat.newClient
	return s
}

func TestAnObservationOnOtherHardwareDoesNotCount(t *testing.T) {
	ollama := &fakeBackend{name: "ollama", status: backend.Status{
		State: backend.StateRunning, RuntimePaths: map[int]hardware.RuntimePath{0: hardware.PathCPU},
	}}
	first, _ := recommendTestServer(t, ollama)
	ollama.status.RuntimePaths = nil

	second := srv2(t, first)
	if _, err := second.RecordHardware(context.Background(), detectReturns(profileWith("NVIDIA GeForce RTX 4090", "10de:2684", 24<<30))); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(second.Handler())
	defer ts.Close()
	var res recommend.Result
	getJSON(t, ts.URL+"/api/recommend?purposes=chat", http.StatusOK, &res)
	if res.RuntimePath != "cuda" || res.PathSource != estimate.PathExpected || res.GPUNotUsed != nil {
		t.Errorf("after a GPU swap the old observation must not decide: %+v", res)
	}
}

func TestModelFitEndpoint(t *testing.T) {
	ollama := &fakeBackend{name: "ollama", status: backend.Status{State: backend.StateRunning}}
	_, ts := recommendTestServer(t, ollama)

	var cat CatalogResponse
	getJSON(t, ts.URL+"/api/catalog", http.StatusOK, &cat)
	id := cat.Families[0].Sizes[0].ID
	base := ts.URL + "/api/models/" + itoa(id) + "/fit"

	var fit ModelFitResponse
	getJSON(t, base, http.StatusOK, &fit)
	// A 24 GiB card: Ollama's own default context is 32k.
	if fit.NumCtx != 32768 || fit.NumCtxSource != "ollama_default" || fit.KVCacheType != estimate.KVF16 || fit.RuntimePath != "cuda" {
		t.Errorf("defaults: %+v", fit)
	}
	if len(fit.Fits) != 1 || !fit.Fits[0].Default || fit.Fits[0].File.Quant != "Q8_0" || len(fit.Model.Files) != 0 || fit.DisplayName != "Llama 3.2" {
		t.Fatalf("fits: %+v", fit)
	}
	est := fit.Fits[0].Estimate
	// llama3.2:1b: 16 layers × 8 KV heads × 64 × 2 × 2 B = 32,768 bytes a token.
	if est.Category != estimate.FitsWithHeadroom || est.Memory.KVCache.Value != 32768*32768 || est.Memory.Overhead.Value != 250<<20 {
		t.Errorf("estimate: %+v", est)
	}
	if est.Memory.Total.Source != figure.Estimated || est.Threshold == "" || !est.Speed.Known || est.Basis.MemoryModel != estimate.MemoryValidated {
		t.Errorf("estimate: %+v", est)
	}

	getJSON(t, base+"?ctx=4096&kv=q8_0", http.StatusOK, &fit)
	if fit.NumCtx != 4096 || fit.NumCtxSource != "requested" || fit.Fits[0].Estimate.Memory.KVCache.Value != 32768*4096*34/64 {
		t.Errorf("ctx=4096&kv=q8_0: %+v", fit.Fits[0].Estimate.Memory)
	}

	getJSON(t, base+"?ctx=12", http.StatusBadRequest, nil)
	getJSON(t, base+"?kv=fp8", http.StatusBadRequest, nil)
	getJSON(t, ts.URL+"/api/models/999999/fit", http.StatusNotFound, nil)
	getJSON(t, ts.URL+"/api/models/abc/fit", http.StatusBadRequest, nil)
	// The size that did not resolve has no files: an empty list, not an error.
	getJSON(t, ts.URL+"/api/models/"+itoa(cat.Families[0].Sizes[1].ID)+"/fit", http.StatusOK, &fit)
	if len(fit.Fits) != 0 {
		t.Errorf("an unresolved size: %+v", fit.Fits)
	}
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{digits[n%10]}, b...)
	}
	return string(b)
}
