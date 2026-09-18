//go:build linux

package ollama

import (
	"path/filepath"
	"testing"
)

// The per-OS findBinary/osInstall implementations are structurally
// identical across install_darwin.go, install_windows.go and
// install_linux.go (checked by the cross-compiles in this package's build,
// which all three must pass); only the host this test suite actually runs
// on can be exercised live, the same constraint internal/hardware's own
// live tests accept.

func TestFindBinaryPrefersPATH(t *testing.T) {
	b := backendWithEnv(fakeEnv{
		paths: map[string]string{"ollama": "/usr/bin/ollama"},
		files: map[string]int64{"/usr/bin/ollama": 100},
	})
	got, ok := b.findBinary()
	if !ok || got != "/usr/bin/ollama" {
		t.Fatalf("findBinary() = %q, %v, want /usr/bin/ollama, true", got, ok)
	}
}

func TestFindBinaryFallsBackToManagedInstall(t *testing.T) {
	t.Setenv("ADVISOR_DATA_DIR", "/data/advisor")
	want := filepath.Join("/data/advisor", "ollama", "bin", "ollama")
	b := backendWithEnv(fakeEnv{
		files: map[string]int64{want: 12345},
	})
	got, ok := b.findBinary()
	if !ok || got != want {
		t.Fatalf("findBinary() = %q, %v, want %q, true", got, ok, want)
	}
}

func TestFindBinaryNotFound(t *testing.T) {
	t.Setenv("ADVISOR_DATA_DIR", "/data/advisor")
	b := backendWithEnv(fakeEnv{})
	if _, ok := b.findBinary(); ok {
		t.Fatal("findBinary() should fail when nothing is on PATH or in the managed install location")
	}
}

func TestOllamaArchRejectsUnshippedArchitectures(t *testing.T) {
	// amd64 and arm64 are exercised implicitly by the build matrix; this
	// just checks the error path names the architecture.
	orig := goarchForTest
	defer func() { goarchForTest = orig }()
	goarchForTest = "riscv64"
	if _, err := ollamaArch(); err == nil {
		t.Fatal("expected an error for an architecture Ollama does not ship on Linux")
	}
}
