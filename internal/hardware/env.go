package hardware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/cpu"
)

// env is everything detection reads from the machine. The real one
// (systemEnv) shells out and reads files; the tests' one (fakeEnv, in
// env_test.go) answers from fixtures, which is how the Windows, macOS and
// Linux paths all run on every CI runner.
type env interface {
	goos() string
	goarch() string
	hostname() (string, error)
	homeDir() (string, error)
	getenv(key string) string
	// run executes a program and returns its standard output. A non-nil
	// error still comes with whatever the program printed: several tools
	// (sysctl with one unknown key) print useful output and exit non-zero.
	run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error)
	lookPath(name string) (string, error)
	// files is the root filesystem as an fs.FS ("proc/meminfo",
	// "sys/bus/pci/devices"). Linux reads through it; nil elsewhere.
	files() fs.FS
	// exists reports whether a path in the OS's own syntax exists.
	exists(path string) bool
	// diskFree is the space available to this user on the volume holding path.
	diskFree(path string) (uint64, error)
	// cpuVector reports AVX2 and AVX-512F for the processor the daemon runs
	// on (CPUID, including the OS's support for the wider registers).
	cpuVector() (avx2, avx512, ok bool)
}

// Timeouts for the tools detection runs. Generous, because a first
// system_profiler or PowerShell start on a cold machine is slow, and bounded,
// because a wedged driver can hang nvidia-smi.
const (
	timeoutQuick      = 10 * time.Second // sysctl, sw_vers, launchctl, lscpu
	timeoutNvidiaSMI  = 20 * time.Second
	timeoutProfiler   = 45 * time.Second // system_profiler
	timeoutPowerShell = 60 * time.Second
)

type systemEnv struct{ root fs.FS }

func newSystemEnv() env {
	e := &systemEnv{}
	if systemGOOS != "windows" {
		e.root = os.DirFS("/")
	}
	return e
}

func (e *systemEnv) goos() string                         { return systemGOOS }
func (e *systemEnv) goarch() string                       { return systemGOARCH }
func (e *systemEnv) hostname() (string, error)            { return os.Hostname() }
func (e *systemEnv) homeDir() (string, error)             { return os.UserHomeDir() }
func (e *systemEnv) getenv(k string) string               { return os.Getenv(k) }
func (e *systemEnv) lookPath(n string) (string, error)    { return exec.LookPath(n) }
func (e *systemEnv) files() fs.FS                         { return e.root }
func (e *systemEnv) diskFree(path string) (uint64, error) { return diskFree(path) }

func (e *systemEnv) exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (e *systemEnv) cpuVector() (avx2, avx512, ok bool) {
	switch systemGOARCH {
	case "amd64", "386":
		return cpu.X86.HasAVX2, cpu.X86.HasAVX512F, true
	}
	return false, false, false
}

func (e *systemEnv) run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s: not found", name)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Tools print numbers and names; keep their output independent of the
	// user's locale where the OS honours it.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	hideWindow(cmd) // Windows: no console window flashes up behind the browser
	err = cmd.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout.String(), fmt.Errorf("%s: no answer within %s", name, timeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		if msg != "" {
			return stdout.String(), fmt.Errorf("%s: %v: %s", name, err, msg)
		}
		return stdout.String(), fmt.Errorf("%s: %v", name, err)
	}
	return stdout.String(), nil
}

// readText reads a file from the root filesystem, trimmed. ok is false when
// the file is missing or unreadable — the caller decides whether that is a
// problem worth naming.
func readText(fsys fs.FS, path string) (string, bool) {
	if fsys == nil {
		return "", false
	}
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}
