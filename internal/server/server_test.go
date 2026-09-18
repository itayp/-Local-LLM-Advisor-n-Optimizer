package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(New(nil, nil).Handler())
	// httptest listens on 127.0.0.1, so the Host header is allowed.
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

func TestHealth(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	var h Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if h.Version == "" || h.OS != runtime.GOOS || h.Arch != runtime.GOARCH {
		t.Fatalf("health = %+v", h)
	}
}

func TestHealthWrongMethodIs405(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/api/health", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", resp.StatusCode)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var e APIError
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || e.Error.Code != "not_found" {
		t.Fatalf("body was not a JSON API error: %v %+v", err, e)
	}
}

func TestSPAFallbackServesIndex(t *testing.T) {
	ts := newTestServer(t)
	for _, p := range []string{"/", "/models", "/settings/advanced"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", p, resp.StatusCode)
		}
		if !strings.Contains(strings.ToLower(string(body)), "<!doctype html>") {
			t.Fatalf("%s: did not get index.html, got %.80q", p, body)
		}
	}
}

func TestForeignHostIsRejected(t *testing.T) {
	ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("status %d, want 421 for a foreign Host header", resp.StatusCode)
	}

	for _, host := range []string{"127.0.0.1:1234", "localhost:1234", "localhost", "127.0.0.1"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Host %q: status %d, want 200", host, resp.StatusCode)
		}
	}
}

func TestForeignOriginIsRejected(t *testing.T) {
	ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for a foreign Origin", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Header.Set("Origin", "http://localhost:5173") // the Vite dev server
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 for a loopback Origin", resp.StatusCode)
	}
}

// TestListenIsLoopbackOnly is product rule 7 as a test: the only listener
// constructor binds to 127.0.0.1, and Serve refuses anything else.
func TestListenIsLoopbackOnly(t *testing.T) {
	l, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !IsLoopback(l.Addr()) {
		t.Fatalf("Listen bound to %s, want %s", l.Addr(), LoopbackHost)
	}
	if got := l.Addr().(*net.TCPAddr).IP.String(); got != LoopbackHost {
		t.Fatalf("Listen bound to %s, want %s", got, LoopbackHost)
	}
}

func TestServeRefusesNonLoopbackListener(t *testing.T) {
	// A listener on the wildcard address — what a bind flag would allow.
	l, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Skip("cannot bind 0.0.0.0 here:", err)
	}
	err = New(nil, nil).Serve(context.Background(), l)
	if !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("Serve on 0.0.0.0 returned %v, want ErrNotLoopback", err)
	}
}

func TestServeAndShutdown(t *testing.T) {
	l, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(nil, nil).Serve(ctx, l) }()

	resp, err := http.Get("http://" + l.Addr().String() + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v after shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}
