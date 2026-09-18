package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"advisor/internal/backend"
	"advisor/internal/catalog"
	"advisor/internal/catalog/hf"
	"advisor/internal/catalog/refresh"
)

const serverTestCatalogue = `
quants: [Q8_0]
families:
  - id: llama3.2
    display_name: Llama 3.2
    maintainer: Meta
    license: {name: Llama 3.2 Community License Agreement, url: "https://www.llama.com/llama3_2/license/"}
    purposes: [chat, writing]
    reviewed_at: 2026-09-18
    source: https://huggingface.co/meta-llama/Llama-3.2-1B-Instruct
    sizes:
      - parameters: 1230000000
        context_length: 131072
        ollama_tag: llama3.2:1b
        hf_repo: bartowski/Llama-3.2-1B-Instruct-GGUF
      - parameters: 3210000000
        context_length: 131072
        ollama_tag: llama3.2:3b
        hf_repo: bartowski/Llama-3.2-3B-Instruct-GGUF
`

// fakeHub serves one repo whose Q8_0 file is a real captured header (see
// internal/catalog/gguf/testdata); every other repo is a 404.
func fakeHub(t *testing.T) *httptest.Server {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "catalog", "gguf", "testdata", "llama3.2-1b.gguf.head.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	file, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	const repo, name = "bartowski/Llama-3.2-1B-Instruct-GGUF", "Llama-3.2-1B-Instruct-Q8_0.gguf"
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/" + repo:
			fmt.Fprintf(w, `{"id":%q,"sha":"abc","siblings":[{"rfilename":%q,"size":%d,"lfs":{"sha256":"f00d","size":%d}}]}`,
				repo, name, len(file), len(file))
		case "/" + repo + "/resolve/abc/" + name:
			if r.Header.Get("Range") == "" {
				http.Error(w, "ranges only", http.StatusBadRequest)
				return
			}
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(file))
		default:
			http.Error(w, `{"error":"Repository not found"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(hub.Close)
	return hub
}

func newCatalogTestServer(t *testing.T, backends ...backend.Backend) (*Server, *httptest.Server) {
	t.Helper()
	srv, _ := newBackendTestServer(t, backends...)
	srv.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := fakeHub(t)
	srv.cat.load = func() (*catalog.Catalogue, error) { return catalog.Parse([]byte(serverTestCatalogue)) }
	srv.cat.newClient = func() *hf.Client {
		c := hf.New("advisor-test")
		c.BaseURL, c.MinInterval = hub.URL, 0
		c.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
		return c
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestCatalogBeforeAndAfterARefresh(t *testing.T) {
	ollama := &fakeBackend{
		name:   "ollama",
		status: backend.Status{State: backend.StateRunning, Version: "0.34.2"},
		models: []backend.Installed{
			{Name: "llama3.2:1b", Family: "llama", ParameterSize: "1.2B", Quantization: "Q8_0"},
			{Name: "qwen3:4b", Family: "qwen3", ParameterSize: "4.0B", Quantization: "Q4_K_M"},
		},
	}
	srv, ts := newCatalogTestServer(t, ollama)
	ctx := context.Background()
	if err := srv.SyncCatalogue(ctx); err != nil {
		t.Fatal(err)
	}

	var before CatalogResponse
	getJSON(t, ts.URL+"/api/catalog", http.StatusOK, &before)
	if before.LastRefresh != nil || len(before.Families) != 1 || len(before.Families[0].Sizes) != 2 ||
		before.Families[0].Sizes[0].ID == 0 || len(before.Families[0].Sizes[0].Files) != 0 {
		t.Fatalf("before a refresh: %+v", before)
	}

	// The inventory refresh maps installed models as soon as it stores them.
	srv.RecordBackends(ctx)
	var inst InstalledModelsResponse
	getJSON(t, ts.URL+"/api/models/installed", http.StatusOK, &inst)
	match := map[string]string{}
	for _, m := range inst.Models {
		match[m.Name] = m.CatalogMatch
	}
	if match["llama3.2:1b"] != "model" || match["qwen3:4b"] != "unknown" {
		t.Errorf("before a refresh: matches %v", match)
	}

	resp, err := http.Post(ts.URL+"/api/catalog/refresh", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var rep refresh.Report
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || rep.Trigger != "api" || rep.Sizes != 2 || rep.Resolved != 1 ||
		len(rep.Failures) != 1 || !strings.Contains(rep.Failures[0].Error, "no repo or file") {
		t.Fatalf("POST /api/catalog/refresh: %d %+v", resp.StatusCode, rep)
	}
	if len(rep.Unknown) != 1 || rep.Unknown[0].Name != "qwen3:4b" {
		t.Errorf("unknown installed in the report: %+v", rep.Unknown)
	}

	var after CatalogResponse
	getJSON(t, ts.URL+"/api/catalog", http.StatusOK, &after)
	one := after.Families[0].Sizes[0]
	if after.LastRefresh == nil || after.LastRefresh.Resolved != 1 || len(one.Files) != 1 ||
		one.Files[0].Quant != "Q8_0" || one.Files[0].Header.HeadCountKV != 8 || one.RefreshedAt == "" {
		t.Errorf("after a refresh: %+v", after)
	}
	if three := after.Families[0].Sizes[1]; three.RefreshError == "" {
		t.Errorf("the size that did not resolve carries no error: %+v", three)
	}

	getJSON(t, ts.URL+"/api/models/installed", http.StatusOK, &inst)
	for _, m := range inst.Models {
		if m.Name == "llama3.2:1b" && (m.CatalogMatch != "file" || m.CatalogFileID == 0) {
			t.Errorf("after a refresh, llama3.2:1b: %+v", m)
		}
	}
	var unknown UnknownInstalledResponse
	getJSON(t, ts.URL+"/api/catalog/unknown", http.StatusOK, &unknown)
	if len(unknown.Models) != 1 || unknown.Models[0].Name != "qwen3:4b" || !strings.Contains(unknown.Models[0].Note, "not in the catalogue") {
		t.Errorf("GET /api/catalog/unknown: %+v", unknown)
	}
}

func TestCatalogRefreshIsOneAtATimeAndPostOnly(t *testing.T) {
	srv, ts := newCatalogTestServer(t)
	srv.cat.running.Lock() // a refresh is "running"
	resp, err := http.Post(ts.URL+"/api/catalog/refresh", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	srv.cat.running.Unlock()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("a second refresh: %d, want 409", resp.StatusCode)
	}
	resp, err = http.Get(ts.URL + "/api/catalog/refresh")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/catalog/refresh: %d, want 405", resp.StatusCode)
	}
	// A browser page on another origin cannot start one (D-12).
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/catalog/refresh", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin POST: %d, want 403", resp.StatusCode)
	}
}
