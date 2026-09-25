package hf

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// authorHub is a minimal fake of GET /api/models?author=... — the only
// endpoint ListByAuthor uses.
type authorHub struct {
	t       *testing.T
	srv     *httptest.Server
	repos   []AuthorRepo
	etag    string
	queries []string // raw queries seen, for assertions
}

func newAuthorHub(t *testing.T, repos []AuthorRepo) *authorHub {
	h := &authorHub{t: t, repos: repos, etag: `"v1"`}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *authorHub) serve(w http.ResponseWriter, r *http.Request) {
	h.queries = append(h.queries, r.URL.RawQuery)
	if r.URL.Path != "/api/models" {
		http.NotFound(w, r)
		return
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == h.etag {
		w.Header().Set("ETag", h.etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", h.etag)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.repos)
}

func (h *authorHub) client() *Client {
	c := New("advisor-test/0")
	c.BaseURL = h.srv.URL
	c.MinInterval = 0
	c.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	c.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return c
}

func TestListByAuthor(t *testing.T) {
	repos := []AuthorRepo{
		{ID: "Qwen/Qwen4-8B", CreatedAt: "2026-09-01T00:00:00.000Z"},
		{ID: "Qwen/Qwen4-8B-Instruct", CreatedAt: "2026-09-01T00:00:00.000Z"},
	}
	h := newAuthorHub(t, repos)
	c := h.client()

	got, err := c.ListByAuthor(context.Background(), "Qwen", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.NotModified {
		t.Fatal("first read must not be 304")
	}
	if len(got.Repos) != 2 || got.Repos[0].ID != "Qwen/Qwen4-8B" {
		t.Fatalf("repos = %+v", got.Repos)
	}
	if got.ETag == "" {
		t.Fatal("expected an ETag")
	}
	if len(h.queries) != 1 || !containsParam(h.queries[0], "author=Qwen") {
		t.Fatalf("query = %q, want author=Qwen", h.queries[0])
	}
}

func TestListByAuthorNotModified(t *testing.T) {
	h := newAuthorHub(t, []AuthorRepo{{ID: "Qwen/Qwen4-8B"}})
	c := h.client()

	got, err := c.ListByAuthor(context.Background(), "Qwen", h.etag)
	if err != nil {
		t.Fatal(err)
	}
	if !got.NotModified {
		t.Fatal("expected 304 with a matching etag")
	}
	if len(got.Repos) != 0 {
		t.Fatalf("a 304 must carry no repos to merge in: got %+v", got.Repos)
	}
}

func TestListByAuthorEmpty(t *testing.T) {
	c := New("advisor-test/0")
	if _, err := c.ListByAuthor(context.Background(), "  ", ""); err == nil {
		t.Fatal("expected an error for an empty author")
	}
}

func containsParam(query, kv string) bool {
	for _, part := range splitAmp(query) {
		if part == kv {
			return true
		}
	}
	return false
}

func splitAmp(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
