package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q", got)
		}
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "local-llm-advisor/") {
			t.Errorf("User-Agent header = %q, want the advisor's own", got)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheck_NewerReleaseAvailable(t *testing.T) {
	srv := serve(t, http.StatusOK, `{"tag_name":"v0.4.0","html_url":"https://example.com/releases/v0.4.0"}`)
	info := checkURL(context.Background(), srv.Client(), srv.URL, "v0.3.1")
	if !info.Checked {
		t.Fatalf("Checked = false, err %q", info.Error)
	}
	if info.Latest != "v0.4.0" {
		t.Errorf("Latest = %q", info.Latest)
	}
	if info.URL != "https://example.com/releases/v0.4.0" {
		t.Errorf("URL = %q", info.URL)
	}
	if !info.UpdateAvailable {
		t.Errorf("UpdateAvailable = false, want true (v0.3.1 -> v0.4.0)")
	}
}

func TestCheck_AlreadyLatest(t *testing.T) {
	srv := serve(t, http.StatusOK, `{"tag_name":"v0.3.1","html_url":"https://example.com/releases/v0.3.1"}`)
	info := checkURL(context.Background(), srv.Client(), srv.URL, "v0.3.1")
	if !info.Checked {
		t.Fatalf("Checked = false, err %q", info.Error)
	}
	if info.UpdateAvailable {
		t.Errorf("UpdateAvailable = true, want false (already on v0.3.1)")
	}
}

// A dev build sitting a few commits past the latest tag (git describe's
// "-N-gHASH[-dirty]" suffix) must not be told it is behind its own tag.
func TestCheck_DevBuildPastTagIsNotBehind(t *testing.T) {
	srv := serve(t, http.StatusOK, `{"tag_name":"v0.3.1"}`)
	info := checkURL(context.Background(), srv.Client(), srv.URL, "v0.3.1-5-gabcdef1-dirty")
	if !info.Checked {
		t.Fatalf("Checked = false, err %q", info.Error)
	}
	if info.UpdateAvailable {
		t.Errorf("UpdateAvailable = true, want false (same core version, just dirty)")
	}
}

// An unstamped developer build ("dev", cmd/advisor's own default) has
// nothing honest to compare against — it must say what's latest without
// claiming to know whether it is behind.
func TestCheck_DevVersionHasNothingToCompare(t *testing.T) {
	srv := serve(t, http.StatusOK, `{"tag_name":"v0.4.0"}`)
	info := checkURL(context.Background(), srv.Client(), srv.URL, "dev")
	if !info.Checked {
		t.Fatalf("Checked = false, err %q", info.Error)
	}
	if info.Latest != "v0.4.0" {
		t.Errorf("Latest = %q", info.Latest)
	}
	if info.UpdateAvailable {
		t.Errorf("UpdateAvailable = true, want false (\"dev\" is not comparable)")
	}
}

func TestCheck_NoReleaseYet(t *testing.T) {
	srv := serve(t, http.StatusNotFound, `{"message":"Not Found"}`)
	info := checkURL(context.Background(), srv.Client(), srv.URL, "v0.3.1")
	if info.Checked {
		t.Fatalf("Checked = true, want false on 404")
	}
	if info.Error == "" {
		t.Errorf("Error is empty, want a reason")
	}
	if info.URL != DownloadURL {
		t.Errorf("URL = %q, want the DownloadURL fallback", info.URL)
	}
}

func TestCheck_RateLimited(t *testing.T) {
	srv := serve(t, http.StatusForbidden, `{"message":"rate limited"}`)
	info := checkURL(context.Background(), srv.Client(), srv.URL, "v0.3.1")
	if info.Checked {
		t.Fatalf("Checked = true, want false on 403")
	}
}

func TestCheck_MalformedAnswer(t *testing.T) {
	srv := serve(t, http.StatusOK, `not json`)
	info := checkURL(context.Background(), srv.Client(), srv.URL, "v0.3.1")
	if info.Checked {
		t.Fatalf("Checked = true, want false on malformed JSON")
	}
}

func TestCheck_Unreachable(t *testing.T) {
	// A closed server: nothing is listening, so the request itself fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	info := checkURL(context.Background(), http.DefaultClient, url, "v0.3.1")
	if info.Checked {
		t.Fatalf("Checked = true, want false when unreachable")
	}
	if info.Error == "" {
		t.Errorf("Error is empty, want a reason")
	}
	if info.URL != DownloadURL {
		t.Errorf("URL = %q, want the DownloadURL fallback", info.URL)
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		newer, ok       bool
	}{
		{"v1.0.0", "v1.0.1", true, true},
		{"v1.0.1", "v1.0.0", false, true},
		{"v1.0.0", "v1.0.0", false, true},
		{"v1.2.0", "v1.10.0", true, true},
		{"dev", "v1.0.0", false, false},
		{"v1.0.0-3-gabc123", "v1.0.0", false, true},
		{"garbage", "v1.0.0", false, false},
	}
	for _, c := range cases {
		newer, ok := isNewer(c.current, c.latest)
		if newer != c.newer || ok != c.ok {
			t.Errorf("isNewer(%q, %q) = (%v, %v), want (%v, %v)", c.current, c.latest, newer, ok, c.newer, c.ok)
		}
	}
}
