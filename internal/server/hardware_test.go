package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"advisor/internal/hardware"
	"advisor/internal/store"
)

func profileWith(gpu, pciID string, vram uint64) hardware.Profile {
	return hardware.Profile{
		OS: "linux", OSVersion: "Ubuntu 24.04.3 LTS", Arch: "amd64", Hostname: "box",
		CPU:      hardware.CPU{Model: "AMD Ryzen 9 5950X 16-Core Processor", VectorKnown: true, HasAVX2: true},
		RAMBytes: 64 << 30, RAMKnown: true,
		GPUs: []hardware.GPU{{Vendor: hardware.VendorNVIDIA, Name: gpu, PCIID: pciID, VRAMBytes: vram, VRAMKnown: true,
			IntegratedKnown: true, DriverVersion: "575.64.05", VRAMSource: "nvidia-smi memory.total",
			ExpectedBackend: hardware.PathCUDA, ExpectedBackendReason: "why", ExpectedBackendRule: "nvidia-cuda"}},
		GPUUsableBytes: vram, GPUUsableKnown: true,
		Tier: hardware.TierGPULarge, Summary: "A desktop with an " + gpu + ".",
		Storage: hardware.Storage{ModelsDir: "/home/u/.ollama/models"},
	}
}

func detectReturns(p hardware.Profile) func(context.Context) (hardware.Profile, error) {
	return func(context.Context) (hardware.Profile, error) { return p, nil }
}

func getJSON(t *testing.T, url string, want int, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("GET %s: status %d, want %d", url, resp.StatusCode, want)
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
}

// Every daemon start stores a profile; a changed GPU is a new
// configuration, and the old profile stays retrievable by id.
func TestHardwareIsStoredOnEveryStartWithHistory(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "advisor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	start := func(p hardware.Profile) (*httptest.Server, HardwareResponse) {
		srv := New(nil, st)
		resp, err := srv.RecordHardware(ctx, detectReturns(p))
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(srv.Handler())
		t.Cleanup(ts.Close)
		return ts, resp
	}

	old := profileWith("NVIDIA GeForce RTX 3080", "10de:2206", 10<<30)
	_, first := start(old)
	if first.ProfileID == 0 || first.Changed || first.PreviousProfileID != 0 {
		t.Fatalf("first start: %+v", first)
	}
	_, second := start(old)
	if second.Changed || second.PreviousProfileID != first.ProfileID || second.Fingerprint != first.Fingerprint {
		t.Fatalf("same hardware again is not a change: %+v", second)
	}
	ts, third := start(profileWith("NVIDIA GeForce RTX 5070 Ti", "10de:2c05", 16303<<20))
	if !third.Changed || third.PreviousProfileID != second.ProfileID {
		t.Fatalf("a swapped GPU is a change: %+v", third)
	}

	var got HardwareResponse
	getJSON(t, ts.URL+"/api/hardware", http.StatusOK, &got)
	if got.ProfileID != third.ProfileID || got.Profile.GPUs[0].Name != "NVIDIA GeForce RTX 5070 Ti" || !got.Changed {
		t.Fatalf("GET /api/hardware: %+v", got)
	}

	var hist HardwareHistory
	getJSON(t, ts.URL+"/api/hardware/history", http.StatusOK, &hist)
	if len(hist.Configurations) != 2 || !hist.Configurations[0].Current || hist.Configurations[1].Current ||
		hist.Configurations[1].Starts != 2 || hist.Configurations[1].GPUs[0] != "NVIDIA GeForce RTX 3080" {
		t.Fatalf("history: %+v", hist)
	}

	var oldProfile HardwareResponse
	getJSON(t, ts.URL+"/api/hardware/profiles/"+strconv.FormatInt(first.ProfileID, 10), http.StatusOK, &oldProfile)
	if oldProfile.Profile.GPUs[0].Name != "NVIDIA GeForce RTX 3080" {
		t.Fatalf("a benchmark's old hardware stays readable: %+v", oldProfile)
	}
	getJSON(t, ts.URL+"/api/hardware/profiles/999", http.StatusNotFound, nil)
	getJSON(t, ts.URL+"/api/hardware/profiles/abc", http.StatusBadRequest, nil)

	resp, err := http.Post(ts.URL+"/api/hardware", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/hardware: %d", resp.StatusCode)
	}
}

// The page never waits for detection; the hardware request does.
func TestHardwareRequestWaitsForDetection(t *testing.T) {
	srv := New(nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	getJSON(t, ts.URL+"/api/health", http.StatusOK, nil) // answers while detection has not even started

	type result struct {
		r   HardwareResponse
		err error
	}
	done := make(chan result, 1)
	go func() { // no t.Fatal off the test goroutine
		var res result
		resp, err := http.Get(ts.URL + "/api/hardware")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				err = errors.New(resp.Status)
			} else {
				err = json.NewDecoder(resp.Body).Decode(&res.r)
			}
		}
		res.err = err
		done <- res
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := srv.RecordHardware(context.Background(), detectReturns(profileWith("NVIDIA GeForce RTX 3090", "10de:2204", 24<<30))); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		r := res.r
		if res.err != nil {
			t.Fatal(res.err)
		}
		if r.Profile.GPUs[0].Name != "NVIDIA GeForce RTX 3090" || r.ProfileID != 0 {
			t.Fatalf("without a store the profile is served, unstored: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting request was not answered")
	}
}

func TestHardwareUnavailableIs503(t *testing.T) {
	saved := hardwareWait
	hardwareWait = 50 * time.Millisecond
	defer func() { hardwareWait = saved }()

	srv := New(nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	var e APIError
	getJSON(t, ts.URL+"/api/hardware", http.StatusServiceUnavailable, &e)
	if e.Error.Code != "detecting" {
		t.Fatalf("still detecting: %+v", e)
	}

	_, _ = srv.RecordHardware(context.Background(), func(context.Context) (hardware.Profile, error) {
		return hardware.Profile{}, errors.New("context canceled")
	})
	getJSON(t, ts.URL+"/api/hardware", http.StatusServiceUnavailable, &e)
	if e.Error.Code != "detection_failed" {
		t.Fatalf("failed detection: %+v", e)
	}
}
