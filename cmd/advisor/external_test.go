package main

import (
	"bytes"
	"strings"
	"testing"
)

// The curator's check covers the public-data files too, offline.
func TestCatalogCheckValidatesThePublicDataFiles(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCatalog([]string{"check"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "external.yaml and aliases.yaml are valid") {
		t.Errorf("output: %s", out.String())
	}
}

// -report reads only the database: no network.
func TestCatalogExternalReportIsOffline(t *testing.T) {
	var out, errb bytes.Buffer
	code := runCatalog([]string{"external", "-report", "-data-dir", t.TempDir()}, &out, &errb)
	if code != 1 { // nothing has ever been read: every enabled source says so
		t.Fatalf("exit %d: %s %s", code, out.String(), errb.String())
	}
	for _, want := range []string{"Hugging Face Eval Results", "Arena leaderboard dataset", "Epoch AI Benchmarking Hub", "misses"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}
