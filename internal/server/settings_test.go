package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"advisor/internal/hardware"
	"advisor/internal/store"
)

func putJSON(t *testing.T, url, body string, want int, into any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("PUT %s: %d, want %d: %s", url, resp.StatusCode, want, b)
	}
	if into != nil {
		if err := json.Unmarshal(b, into); err != nil {
			t.Fatal(err)
		}
	}
}

func newSettingsTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "advisor.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(nil, st), st
}

func TestSettingsRoundTripThroughTheStore(t *testing.T) {
	srv, _ := newSettingsTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var got SettingsResponse
	getJSON(t, ts.URL+"/api/settings", http.StatusOK, &got)
	if got.Advanced {
		t.Fatalf("advanced should default to off: %+v", got)
	}
	if got.DataDir == "" {
		t.Fatal("data_dir should be set once there is a store")
	}

	var updated SettingsResponse
	putJSON(t, ts.URL+"/api/settings", `{"advanced": true}`, http.StatusOK, &updated)
	if !updated.Advanced {
		t.Fatalf("PUT /api/settings should report the new value: %+v", updated)
	}

	var again SettingsResponse
	getJSON(t, ts.URL+"/api/settings", http.StatusOK, &again)
	if !again.Advanced {
		t.Fatalf("advanced should stay on across a later GET: %+v", again)
	}
}

func TestSettingsPutRejectsMalformedBody(t *testing.T) {
	srv, _ := newSettingsTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	putJSON(t, ts.URL+"/api/settings", `{"advanced": "yes"}`, http.StatusBadRequest, nil)
	putJSON(t, ts.URL+"/api/settings", `{"advanced": true, "unknown": 1}`, http.StatusBadRequest, nil)
}

func TestSettingsWithoutAStore(t *testing.T) {
	srv := New(nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var got SettingsResponse
	getJSON(t, ts.URL+"/api/settings", http.StatusOK, &got)
	if got.Advanced || got.DataDir != "" {
		t.Fatalf("without a store there is nothing durable to report: %+v", got)
	}
	putJSON(t, ts.URL+"/api/settings", `{"advanced": true}`, http.StatusServiceUnavailable, nil)
}

func TestOpenDataDirOpensTheStoresFolder(t *testing.T) {
	srv, st := newSettingsTestServer(t)
	var opened []string
	srv.open = func(dir string) error {
		opened = append(opened, dir)
		return nil
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	postJSON(t, ts.URL+"/api/settings/open-data-dir", ``, http.StatusOK, nil)
	want := filepath.Dir(st.Path())
	if len(opened) != 1 || opened[0] != want {
		t.Fatalf("opened = %v, want [%q]", opened, want)
	}
}

func TestOpenDataDirWithoutAStore(t *testing.T) {
	srv := New(nil, nil)
	srv.open = func(string) error { return nil }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	postJSON(t, ts.URL+"/api/settings/open-data-dir", ``, http.StatusServiceUnavailable, nil)
}

func TestOpenModelsDirOpensWhatHardwareDetectionFound(t *testing.T) {
	srv, _ := newSettingsTestServer(t)
	var opened []string
	srv.open = func(dir string) error {
		opened = append(opened, dir)
		return nil
	}
	p := hardware.Profile{Storage: hardware.Storage{ModelsDir: "/home/u/.ollama/models"}}
	if _, err := srv.RecordHardware(context.Background(), detectReturns(p)); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	postJSON(t, ts.URL+"/api/settings/open-models-dir", ``, http.StatusOK, nil)
	if len(opened) != 1 || opened[0] != "/home/u/.ollama/models" {
		t.Fatalf("opened = %v", opened)
	}
}

func TestOpenModelsDirUnknown(t *testing.T) {
	srv, _ := newSettingsTestServer(t)
	srv.open = func(string) error { return nil }
	p := hardware.Profile{Storage: hardware.Storage{ModelsDir: hardware.Unknown}}
	if _, err := srv.RecordHardware(context.Background(), detectReturns(p)); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	postJSON(t, ts.URL+"/api/settings/open-models-dir", ``, http.StatusServiceUnavailable, nil)
}

func TestOpenFolderFailureIsReportedInWords(t *testing.T) {
	srv, _ := newSettingsTestServer(t)
	srv.open = func(string) error { return errors.New("no file manager on this machine") }
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	postJSON(t, ts.URL+"/api/settings/open-data-dir", ``, http.StatusInternalServerError, nil)
}
