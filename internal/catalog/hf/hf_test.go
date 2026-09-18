package hf

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"advisor/internal/catalog/gguf"
)

// fixture is a real GGUF header prefix (see ../gguf/testdata/README.md).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "gguf", "testdata", name+".gguf.head.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// hub is a fake Hugging Face: a model-info endpoint with ETags, and resolve
// URLs that redirect to a CDN path serving byte ranges — the shape of the
// real thing.
type hub struct {
	t     *testing.T
	srv   *httptest.Server
	files map[string][]byte // "owner/name/path" → content
	etag  string

	mu           sync.Mutex
	requests     []string
	servedBytes  int64
	rangeless    int // content requests without a Range header
	userAgents   map[string]bool
	ignoreRange  bool
	redirectTo   string // if set, resolve redirects here instead of the CDN path
	statusQueue  []int  // statuses to answer before behaving (429, 503, ...)
	retryAfter   string
	rateLimitHdr string
}

func newHub(t *testing.T) *hub {
	h := &hub{t: t, files: map[string][]byte{}, etag: `"v1"`, userAgents: map[string]bool{}}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hub) client() *Client {
	c := New("advisor-test/0")
	c.BaseURL = h.srv.URL
	c.MinInterval = 0
	c.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	c.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return c
}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, r.Method+" "+r.URL.RequestURI())
	h.userAgents[r.UserAgent()] = true
	if len(h.statusQueue) > 0 {
		st := h.statusQueue[0]
		h.statusQueue = h.statusQueue[1:]
		if h.retryAfter != "" {
			w.Header().Set("Retry-After", h.retryAfter)
		}
		if h.rateLimitHdr != "" {
			w.Header().Set("RateLimit", h.rateLimitHdr)
		}
		h.mu.Unlock()
		http.Error(w, "slow down", st)
		return
	}
	h.mu.Unlock()

	switch {
	case strings.HasPrefix(r.URL.Path, "/api/models/"):
		repo := strings.TrimPrefix(r.URL.Path, "/api/models/")
		if r.URL.Query().Get("blobs") != "true" {
			h.t.Errorf("model-info without blobs=true: %s", r.URL)
		}
		if repo == "owner/gated" {
			http.Error(w, `{"error":"Access to model owner/gated is restricted."}`, http.StatusUnauthorized)
			return
		}
		var sib []map[string]any
		for k, v := range h.files {
			if p, ok := strings.CutPrefix(k, repo+"/"); ok {
				sib = append(sib, map[string]any{"rfilename": p, "size": len(v), "lfs": map[string]any{"sha256": fmt.Sprintf("sha-%s", p), "size": len(v)}})
			}
		}
		if len(sib) == 0 {
			http.Error(w, `{"error":"Repository not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", h.etag)
		if r.Header.Get("If-None-Match") == h.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": repo, "sha": "commit1", "gated": false, "private": false,
			"gguf":     map[string]any{"total": 1235814432, "architecture": "llama", "context_length": 131072},
			"siblings": sib,
		})
	case strings.Contains(r.URL.Path, "/resolve/"):
		target := strings.Replace(r.URL.Path, "/resolve/commit1/", "/cdn/", 1)
		if h.redirectTo != "" {
			target = h.redirectTo
		}
		http.Redirect(w, r, target, http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/"):
		key := strings.Replace(strings.TrimPrefix(r.URL.Path, "/"), "/cdn/", "/", 1)
		b, ok := h.files[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		if r.Header.Get("Range") == "" {
			h.rangeless++
		}
		h.mu.Unlock()
		if h.ignoreRange {
			r.Header.Del("Range")
		}
		http.ServeContent(&countingWriter{ResponseWriter: w, h: h}, r, "", time.Time{}, bytes.NewReader(b))
	}
}

// countingWriter counts body bytes as they are written, so the count is
// complete by the time the client has read them.
type countingWriter struct {
	http.ResponseWriter
	h *hub
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.h.mu.Lock()
	c.h.servedBytes += int64(len(p))
	c.h.mu.Unlock()
	return c.ResponseWriter.Write(p)
}

func TestModelInfoAndETag(t *testing.T) {
	h := newHub(t)
	h.files["bartowski/Llama-3.2-1B-Instruct-GGUF/Llama-3.2-1B-Instruct-Q4_K_M.gguf"] = []byte("x")
	c := h.client()
	ctx := context.Background()
	l, err := c.ModelInfo(ctx, "bartowski/Llama-3.2-1B-Instruct-GGUF", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.NotModified || l.ETag != `"v1"` || l.Info.SHA != "commit1" || l.Info.GGUF == nil || l.Info.GGUF.Total != 1235814432 {
		t.Fatalf("listing = %+v / %+v", l, l.Info)
	}
	if len(l.Info.Siblings) != 1 || l.Info.Siblings[0].LFS == nil || l.Info.Siblings[0].LFS.SHA256 == "" {
		t.Fatalf("siblings = %+v", l.Info.Siblings)
	}
	again, err := c.ModelInfo(ctx, "bartowski/Llama-3.2-1B-Instruct-GGUF", l.ETag, l.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !again.NotModified || again.Info.SHA != "commit1" {
		t.Errorf("second listing: notModified=%v info=%+v", again.NotModified, again.Info)
	}
	if st := c.Stats(); st.NotModified != 1 || st.Requests != 2 {
		t.Errorf("stats = %+v", st)
	}
	if !h.userAgents["advisor-test/0"] || len(h.userAgents) != 1 {
		t.Errorf("user agents sent: %v", h.userAgents)
	}
}

func TestReadHeaderFetchesOnlyTheHeader(t *testing.T) {
	h := newHub(t)
	file := fixture(t, "llama3.2-1b") // 7.8 MB: the header of a 1.3 GB file
	h.files["o/r/Llama-3.2-1B-Instruct-Q8_0.gguf"] = file
	c := h.client()

	hdr, n, err := c.ReadHeader(context.Background(), "o/r", "commit1", "Llama-3.2-1B-Instruct-Q8_0.gguf", uint64(len(file)), gguf.Options{StopAtTokenizer: true})
	if err != nil {
		t.Fatal(err)
	}
	m := hdr.Metadata()
	if m.Architecture != "llama" || m.BlockCount != 16 || m.HeadCountKV != 8 || m.KeyLength != 64 || m.FileType != 7 {
		t.Errorf("metadata = %+v", m)
	}
	if n != c.FirstChunk || h.servedBytes != c.FirstChunk {
		t.Errorf("downloaded %d bytes (server sent %d) to read a header that stops at byte %d; want one %d-byte range",
			n, h.servedBytes, hdr.BytesRead, c.FirstChunk)
	}
	if h.rangeless != 0 {
		t.Errorf("%d content requests had no Range header", h.rangeless)
	}

	// The whole metadata section, tokenizer and all: the chunks double, so
	// the transfer stays within twice the header.
	h.servedBytes = 0
	full, n, err := c.ReadHeader(context.Background(), "o/r", "commit1", "Llama-3.2-1B-Instruct-Q8_0.gguf", uint64(len(file)), gguf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !full.Complete || n < full.BytesRead || n > 2*full.BytesRead {
		t.Errorf("full read: complete=%v downloaded=%d header=%d", full.Complete, n, full.BytesRead)
	}
}

func TestReadHeaderRefusesAServerThatIgnoresRange(t *testing.T) {
	h := newHub(t)
	h.files["o/r/m.gguf"] = fixture(t, "llama3.2-1b")
	h.ignoreRange = true
	c := h.client()
	_, n, err := c.ReadHeader(context.Background(), "o/r", "commit1", "m.gguf", 0, gguf.Options{StopAtTokenizer: true})
	if !errors.Is(err, ErrRangeIgnored) {
		t.Fatalf("err = %v, want ErrRangeIgnored", err)
	}
	if n != 0 {
		t.Errorf("read %d bytes of a whole-file answer", n)
	}
}

func TestRedirectAwayFromHuggingFaceIsRefused(t *testing.T) {
	h := newHub(t)
	h.files["o/r/m.gguf"] = []byte("GGUF")
	h.redirectTo = "http://evil.example.com/m.gguf"
	c := h.client()
	_, _, err := c.ReadHeader(context.Background(), "o/r", "commit1", "m.gguf", 4, gguf.Options{})
	if !errors.Is(err, ErrRedirect) {
		t.Fatalf("err = %v, want ErrRedirect", err)
	}
	for _, host := range []string{"huggingface.co", "cdn-lfs.huggingface.co", "cas-bridge.xethub.hf.co", "hf.co"} {
		if !c.allowedHost(host) {
			t.Errorf("%s refused", host)
		}
	}
	for _, host := range []string{"huggingface.co.evil.com", "evilhuggingface.co", "example.com", "hf.com"} {
		if c.allowedHost(host) {
			t.Errorf("%s allowed", host)
		}
	}
}

func TestRateLimitIsHonoured(t *testing.T) {
	h := newHub(t)
	h.files["o/r/m.gguf"] = []byte("x")
	c := h.client()
	var slept []time.Duration
	c.Sleep = func(ctx context.Context, d time.Duration) error { slept = append(slept, d); return nil }

	h.statusQueue, h.retryAfter = []int{429}, "3"
	if _, err := c.ModelInfo(context.Background(), "o/r", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 3*time.Second {
		t.Errorf("slept %v, want [3s] from Retry-After", slept)
	}

	slept = nil
	h.statusQueue, h.retryAfter, h.rateLimitHdr = []int{429}, "", `"api";r=0;t=7`
	if _, err := c.ModelInfo(context.Background(), "o/r", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] != 7*time.Second {
		t.Errorf("slept %v, want [7s] from the RateLimit header", slept)
	}

	slept = nil
	h.statusQueue, h.rateLimitHdr = []int{503, 503}, ""
	if _, err := c.ModelInfo(context.Background(), "o/r", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 2 || slept[0] != time.Second || slept[1] != 2*time.Second {
		t.Errorf("slept %v, want the 1s, 2s backoff", slept)
	}

	// A wait longer than MaxWait fails now instead of stalling the refresh.
	slept = nil
	h.statusQueue, h.retryAfter = []int{429}, "3600"
	_, err := c.ModelInfo(context.Background(), "o/r", "", nil)
	if !errors.Is(err, ErrRateLimited) || len(slept) != 0 {
		t.Errorf("err = %v, slept %v; want ErrRateLimited without waiting an hour", err, slept)
	}

	// Retries are bounded.
	h.statusQueue, h.retryAfter = []int{429, 429, 429, 429, 429}, "1"
	_, err = c.ModelInfo(context.Background(), "o/r", "", nil)
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("err = %v after %d 429s, want ErrRateLimited", err, c.MaxAttempts)
	}
	h.statusQueue = nil
}

func TestGatedAndMissingRepos(t *testing.T) {
	h := newHub(t)
	c := h.client()
	if _, err := c.ModelInfo(context.Background(), "owner/gated", "", nil); !errors.Is(err, ErrGated) {
		t.Errorf("gated: err = %v", err)
	}
	if _, err := c.ModelInfo(context.Background(), "owner/missing", "", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: err = %v", err)
	}
	var g Gated
	for in, want := range map[string]Gated{`false`: "", `"auto"`: "auto", `"manual"`: "manual", `true`: "true", `null`: ""} {
		if err := json.Unmarshal([]byte(in), &g); err != nil || g != want {
			t.Errorf("gated %s → %q, %v", in, g, err)
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		h    http.Header
		want time.Duration
		ok   bool
	}{
		{http.Header{"Retry-After": {"12"}}, 12 * time.Second, true},
		{http.Header{"Retry-After": {now.Add(30 * time.Second).Format(http.TimeFormat)}}, 30 * time.Second, true},
		{http.Header{"Ratelimit": {`"api";r=0;t=55`}}, 55 * time.Second, true},
		{http.Header{"Retry-After": {"soon"}}, 0, false},
		{http.Header{}, 0, false},
	}
	for _, tc := range cases {
		got, ok := retryAfter(tc.h, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("retryAfter(%v) = %v, %v; want %v, %v", tc.h, got, ok, tc.want, tc.ok)
		}
	}
}

func TestUnreachableIsItsOwnError(t *testing.T) {
	h := newHub(t)
	c := h.client()
	c.BaseURL = "http://127.0.0.1:1" // nothing listens there
	_, err := c.ModelInfo(context.Background(), "o/r", "", nil)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if st := c.Stats(); st.Requests != c.MaxAttempts {
		t.Errorf("%d attempts, want %d", st.Requests, c.MaxAttempts)
	}
}

func TestAWholeFileAnswerIsFineWhenTheFileFitsTheRange(t *testing.T) {
	h := newHub(t)
	proj := fixture(t, "minicpm-v4.6-projector") // 4 KiB: smaller than the first range
	h.files["o/r/mmproj-small.gguf"] = proj
	h.ignoreRange = true
	c := h.client()
	hdr, n, err := c.ReadHeader(context.Background(), "o/r", "commit1", "mmproj-small.gguf", 0, gguf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Metadata().Architecture != "clip" || n != int64(len(proj)) {
		t.Errorf("arch %q, read %d of %d", hdr.Metadata().Architecture, n, len(proj))
	}
}
