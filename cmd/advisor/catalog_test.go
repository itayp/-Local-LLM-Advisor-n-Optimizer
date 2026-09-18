package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogCheck(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCatalog([]string{"check"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "is valid") {
		t.Errorf("check of the embedded catalogue: exit %d, %q %q", code, out.String(), errb.String())
	}
	bad := filepath.Join(t.TempDir(), "families.yaml")
	if err := os.WriteFile(bad, []byte("quants: [Q4_K_M]\nfamilies:\n  - id: Not Valid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := runCatalog([]string{"check", bad}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "id must be lower case") {
		t.Errorf("check of a bad file: exit %d, %q", code, errb.String())
	}
}

func TestCatalogUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runCatalog(nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "advisor catalog refresh") {
		t.Errorf("no subcommand: exit %d, %q", code, errb.String())
	}
	if code := runCatalog([]string{"frobnicate"}, &out, &errb); code != 2 {
		t.Errorf("unknown subcommand: exit %d", code)
	}
	if !isCatalogCommand([]string{"advisor", "catalog", "refresh"}) || isCatalogCommand([]string{"advisor", "-port", "1"}) {
		t.Error("isCatalogCommand")
	}
}
