package hardware

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// fakeEnv answers detection from fixtures. Every OS path runs through it on
// every CI runner: that is how the Windows parser is tested on Linux and the
// sysfs parser on Windows.
type fakeEnv struct {
	os, arch string
	host     string
	home     string
	vars     map[string]string
	cmds     map[string]fakeCmd // normalised command-line prefix → answer
	onPath   map[string]bool    // programs lookPath finds
	fsys     fstest.MapFS       // Linux root filesystem
	existing map[string]bool    // OS paths that exist (Windows, macOS)
	free     uint64
	freeErr  error
	avx2     bool
	avx512   bool
	vecOK    bool

	mu       sync.Mutex // detection runs tools concurrently
	ran      []string
	diskPath string
}

type fakeCmd struct {
	out string
	err error
}

func newFakeEnv(goos, goarch string) *fakeEnv {
	return &fakeEnv{
		os: goos, arch: goarch,
		host:     "fixture-host",
		vars:     map[string]string{},
		cmds:     map[string]fakeCmd{},
		onPath:   map[string]bool{},
		fsys:     fstest.MapFS{},
		existing: map[string]bool{},
		free:     500 << 30,
		vecOK:    true, avx2: true,
	}
}

func (f *fakeEnv) goos() string              { return f.os }
func (f *fakeEnv) goarch() string            { return f.arch }
func (f *fakeEnv) hostname() (string, error) { return f.host, nil }
func (f *fakeEnv) getenv(k string) string    { return f.vars[k] }
func (f *fakeEnv) cpuVector() (bool, bool, bool) {
	return f.avx2, f.avx512, f.vecOK
}

func (f *fakeEnv) homeDir() (string, error) {
	if f.home == "" {
		return "", errors.New("no home")
	}
	return f.home, nil
}

func (f *fakeEnv) files() fs.FS {
	if f.os != "linux" {
		return nil
	}
	return f.fsys
}

func (f *fakeEnv) exists(p string) bool { return f.existing[p] }

func (f *fakeEnv) diskFree(p string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.diskPath = p
	return f.free, f.freeErr
}

func (f *fakeEnv) lookPath(name string) (string, error) {
	if f.onPath[commandBase(name)] {
		return "/fake/bin/" + name, nil
	}
	return "", fmt.Errorf("%s: not found", name)
}

// commandBase is a program's name without directories or ".exe", lower
// case: how fixtures name commands whatever path detection used.
func commandBase(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(strings.ToLower(name), ".exe")
}

func (f *fakeEnv) run(ctx context.Context, _ time.Duration, name string, args ...string) (string, error) {
	line := strings.Join(append([]string{commandBase(name)}, args...), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, line)
	best, found := "", false
	for k := range f.cmds {
		if strings.HasPrefix(line, k) && len(k) >= len(best) {
			best, found = k, true
		}
	}
	if !found {
		return "", fmt.Errorf("%s: not found", commandBase(name))
	}
	c := f.cmds[best]
	return c.out, c.err
}

// loadFixture reads testdata/<name>.txtar into the fake: sections named
// "-- path --" become files of the Linux root filesystem, "-- $ cmd --"
// the standard output of a command line starting with cmd, "-- ! cmd --" a
// command that fails with that text as its error. Paths may contain colons
// (PCI addresses): they live inside one file, never on the disk, so the
// fixtures check out on Windows too.
func loadFixture(t *testing.T, f *fakeEnv, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txtar"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range parseTxtar(string(b)) {
		switch {
		case sec.name == "env": // KEY=VALUE per line: the process environment
			for _, line := range strings.Split(strings.TrimSpace(sec.body), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					f.vars[k] = v
				}
			}
		case sec.name == "exists": // one OS path per line (Windows, macOS)
			for _, line := range strings.Split(strings.TrimSpace(sec.body), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					f.existing[line] = true
				}
			}
		case strings.HasPrefix(sec.name, "$ "):
			cmd := strings.TrimPrefix(sec.name, "$ ")
			f.cmds[cmd] = fakeCmd{out: sec.body}
			f.onPath[strings.Fields(cmd)[0]] = true
		case strings.HasPrefix(sec.name, "! "):
			cmd := strings.TrimPrefix(sec.name, "! ")
			f.cmds[cmd] = fakeCmd{err: errors.New(strings.TrimSpace(sec.body))}
			f.onPath[strings.Fields(cmd)[0]] = true
		default:
			f.fsys[strings.TrimPrefix(sec.name, "/")] = &fstest.MapFile{Data: []byte(sec.body)}
		}
	}
	// Every Linux fixture shares one pci.ids excerpt unless it brings its own.
	if f.os == "linux" {
		if _, ok := f.fsys["usr/share/misc/pci.ids"]; !ok {
			ids, err := os.ReadFile(filepath.Join("testdata", "linux", "pci.ids"))
			if err != nil {
				t.Fatal(err)
			}
			f.fsys["usr/share/misc/pci.ids"] = &fstest.MapFile{Data: ids}
		}
	}
}

type txtarSection struct{ name, body string }

// parseTxtar reads the txtar layout: a free-form header, then sections
// that start with a line "-- name --". Lines starting with "#" before the
// first section are the fixture's provenance note.
func parseTxtar(s string) []txtarSection {
	var out []txtarSection
	var cur *txtarSection
	var body strings.Builder
	flush := func() {
		if cur != nil {
			cur.body = body.String()
			out = append(out, *cur)
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		trim := strings.TrimRight(line, "\n")
		if strings.HasPrefix(trim, "-- ") && strings.HasSuffix(trim, " --") && len(trim) > 6 {
			flush()
			cur = &txtarSection{name: strings.TrimSpace(trim[3 : len(trim)-3])}
			continue
		}
		if cur != nil {
			body.WriteString(line)
		}
	}
	flush()
	return out
}

// section returns one named section of a fixture, for parser tests.
func section(t *testing.T, fixture, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", fixture+".txtar"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range parseTxtar(string(b)) {
		if sec.name == name {
			return sec.body
		}
	}
	var names []string
	for _, sec := range parseTxtar(string(b)) {
		names = append(names, sec.name)
	}
	sort.Strings(names)
	t.Fatalf("fixture %s has no section %q (has %v)", fixture, name, names)
	return ""
}

func detectFixture(t *testing.T, f *fakeEnv) Profile {
	t.Helper()
	p, err := detect(context.Background(), f)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	return p
}
