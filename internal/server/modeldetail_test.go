package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"advisor/internal/backend"
	"advisor/internal/catalog/external"
	"advisor/internal/figure"
	"advisor/internal/recommend"
)

// arenaHub is a fake Dataset Viewer with one board rating both test sizes.
func arenaHub(t *testing.T) *httptest.Server {
	t.Helper()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/splits":
			_, _ = io.WriteString(w, `{"splits":[{"dataset":"lmarena-ai/leaderboard-dataset","config":"text","split":"latest"}]}`)
		case "/filter":
			if r.URL.Query().Get("where") != `"category"='overall'` {
				_, _ = io.WriteString(w, `{"features":[{"name":"model_name"},{"name":"rating"},{"name":"category"},{"name":"leaderboard_publish_date"}],"rows":[],"num_rows_total":0}`)
				return
			}
			_, _ = io.WriteString(w, `{"features":[{"name":"model_name"},{"name":"license"},{"name":"rating"},{"name":"vote_count"},{"name":"category"},{"name":"leaderboard_publish_date"}],
				"rows":[
				 {"row":{"model_name":"llama-3.2-3b-instruct","license":"Llama 3.2","rating":1166.2,"vote_count":12904,"category":"overall","leaderboard_publish_date":"2026-09-15"}},
				 {"row":{"model_name":"llama-3.2-1b-instruct","license":"Llama 3.2","rating":1102.9,"vote_count":9001,"category":"overall","leaderboard_publish_date":"2026-09-15"}}],
				"num_rows_total":2}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)
	return hub
}

// publicTestServer is recommendTestServer with Arena switched on (and
// pointed at a fake), the others off, and a refresh that read it.
func publicTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	ollama := &fakeBackend{name: "ollama", status: backend.Status{State: backend.StateRunning, Version: "0.34.2"}}
	srv, ts := recommendTestServer(t, ollama)
	hub := arenaHub(t)
	u, _ := url.Parse(hub.URL)
	srv.cat.loadExternal = func() (*external.Config, external.Aliases, error) {
		cfg, err := external.DefaultConfig()
		if err != nil {
			return nil, nil, err
		}
		for i := range cfg.Sources {
			s := &cfg.Sources[i]
			s.Enabled = s.ID == external.SourceArena
			s.URL, s.Hosts = hub.URL, []string{u.Host}
		}
		al, err := external.ParseAliases([]byte(`
arena:
  - {name: llama-3.2-1b-instruct, family: llama3.2, parameters: 1230000000, reviewed_at: 2026-09-24}
  - {name: llama-3.2-3b-instruct, family: llama3.2, parameters: 3210000000, reviewed_at: 2026-09-24}
`))
		return cfg, al, err
	}
	srv.cat.externalFetcher = func(cfg *external.Config) *external.Fetcher {
		f := external.NewFetcher("advisor-test", cfg.EnabledHosts())
		f.MinInterval = 0
		f.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
		return f
	}
	rep, err := srv.RefreshCatalog(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if rep.External == nil || len(rep.External.Sources) != 3 {
		t.Fatalf("the refresh report carries no public-data part: %+v", rep.External)
	}
	for _, sr := range rep.External.Sources {
		if sr.ID == external.SourceArena && (sr.Stored != 2 || len(sr.Hits) != 2) {
			t.Fatalf("arena: %+v", sr)
		}
	}
	return srv, ts
}

func TestModelDetailKeepsPublicAndLocalApart(t *testing.T) {
	_, ts := publicTestServer(t)
	var res recommend.Result
	getJSON(t, ts.URL+"/api/recommend?purposes=chat", http.StatusOK, &res)
	if len(res.Recommendations) != 1 {
		t.Fatalf("recommendations: %+v", res)
	}
	card := res.Recommendations[0]
	if card.Public == nil || card.Public.Position != "2nd for everyday chat of the 2 models here that Arena has rated." ||
		card.Public.Value.Origin.Date != "2026-09-15" || card.Public.Value.Origin.Publisher != "Arena (arena.ai)" {
		t.Errorf("the card's public line: %+v", card.Public)
	}
	for _, reason := range card.Reasons {
		if strings.Contains(reason.Text, "Arena") {
			t.Errorf("a public value leaked into a reason: %q", reason.Text)
		}
	}

	id := card.Model.ID
	resp, err := http.Get(ts.URL + "/api/models/" + strconv.FormatInt(id, 10) + "/detail")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail: %d %s", resp.StatusCode, body)
	}
	var d ModelDetailResponse
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatal(err)
	}
	if d.Name != "Llama 3.2 1B" || d.PullName != "llama3.2:1b" || len(d.Local.Fits) == 0 || d.Local.Fits[0].Estimate.Memory.Total.Source != figure.Estimated {
		t.Errorf("detail: %+v", d)
	}
	if len(d.Public.Entries) != 1 || d.Public.Entries[0].Rated != 2 || d.Public.Entries[0].Rank != 2 ||
		!strings.Contains(d.Public.Updated, "last updated") {
		t.Errorf("public block: %+v", d.Public)
	}
	// On the wire: the public object carries no "source" key a local figure
	// could be confused with; the local object carries no public value.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	if strings.Contains(string(raw["public"]), `"source"`) {
		t.Errorf("the public block has a \"source\" key: %s", raw["public"])
	}
	if strings.Contains(string(raw["local"]), `"origin"`) || strings.Contains(string(raw["local"]), "Arena") {
		t.Errorf("a public value in the local block: %s", raw["local"])
	}

	getJSON(t, ts.URL+"/api/models/999/detail", http.StatusNotFound, nil)
}

// Public data never changes a fit category, a speed range or a confidence
// (the engine test holds the arithmetic; this holds the wiring).
func TestPublicDataMovesNoLocalNumber(t *testing.T) {
	ollama := &fakeBackend{name: "ollama", status: backend.Status{State: backend.StateRunning, Version: "0.34.2"}}
	_, plainTS := recommendTestServer(t, ollama)
	_, publicTS := publicTestServer(t)
	var a, b recommend.Result
	getJSON(t, plainTS.URL+"/api/recommend?purposes=chat", http.StatusOK, &a)
	getJSON(t, publicTS.URL+"/api/recommend?purposes=chat", http.StatusOK, &b)
	ea, _ := json.Marshal(a.Recommendations[0].Estimate)
	eb, _ := json.Marshal(b.Recommendations[0].Estimate)
	if string(ea) != string(eb) || a.Recommendations[0].Confidence != b.Recommendations[0].Confidence {
		t.Errorf("public data moved a local number:\n%s\n%s", ea, eb)
	}
}
