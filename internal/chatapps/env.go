package chatapps

import (
	"os"
	"os/exec"
)

// env seams everything that touches this machine, the same reason
// internal/backend/ollama's own env.go exists: so Detect's "is this app
// on disk" logic can be tested with fixtures instead of a real install.
// Deliberately duplicated rather than imported from internal/backend/ollama
// — ARCHITECTURE.md D-11's dependency direction is one way, and these two
// packages are siblings with no reason to depend on each other.
type env interface {
	lookPath(file string) (string, error)
	statSize(path string) (size int64, isDir bool, ok bool) // ok is false: does not exist or unreadable
	getenv(key string) string
	userHomeDir() (string, error)
}

type realEnv struct{}

func (realEnv) lookPath(file string) (string, error) { return exec.LookPath(file) }

func (realEnv) statSize(path string) (int64, bool, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false, false
	}
	return fi.Size(), fi.IsDir(), true
}

func (realEnv) getenv(key string) string { return os.Getenv(key) }

func (realEnv) userHomeDir() (string, error) { return os.UserHomeDir() }
