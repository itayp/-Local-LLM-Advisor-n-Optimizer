package ollama

import (
	"os"
	"os/exec"
)

// env seams everything that touches this machine rather than Ollama's HTTP
// API (which is already testable with httptest.Server): environment
// variables, the filesystem, and $PATH. It exists so Detect's "is the
// binary on disk" path, and Install/Start's per-OS binary-location logic,
// can be tested with fixtures instead of a real Ollama install — the same
// reason internal/hardware seams the OS behind env (its doc comment).
type env interface {
	getenv(key string) string
	lookPath(file string) (string, error)
	statSize(path string) (size int64, isDir bool, ok bool) // ok is false: does not exist or unreadable
	userHomeDir() (string, error)
}

type realEnv struct{}

func (realEnv) getenv(key string) string { return os.Getenv(key) }

func (realEnv) lookPath(file string) (string, error) { return exec.LookPath(file) }

func (realEnv) statSize(path string) (int64, bool, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false, false
	}
	return fi.Size(), fi.IsDir(), true
}

func (realEnv) userHomeDir() (string, error) { return os.UserHomeDir() }
