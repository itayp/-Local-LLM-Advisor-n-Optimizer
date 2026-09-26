package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"advisor/internal/update"
)

func TestUpdateCheckUsesTheInjectedSeam(t *testing.T) {
	srv := New(nil, nil)
	var gotCurrent string
	srv.checkUpdate = func(ctx context.Context, current string) update.Info {
		gotCurrent = current
		return update.Info{Current: current, Latest: "v9.9.9", URL: "https://example.com", UpdateAvailable: true, Checked: true}
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/update/check")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	var got update.Info
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.UpdateAvailable || got.Latest != "v9.9.9" {
		t.Errorf("got %+v", got)
	}
	// gotCurrent is whatever version.Version is in this test binary
	// ("dev" unless stamped) — just confirm the seam actually received it,
	// not a hard-coded guess at the value.
	if gotCurrent == "" {
		t.Errorf("checkUpdate was not called with a current version")
	}
}

func TestUpdateCheckWrongMethod(t *testing.T) {
	srv := New(nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/update/check", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}
