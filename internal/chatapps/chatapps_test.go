package chatapps

import (
	"errors"
	"testing"
)

// fakeEnv mirrors internal/backend/ollama's own test double for the same
// env seam shape: no real environment, filesystem or $PATH.
type fakeEnv struct {
	vars  map[string]string
	paths map[string]string
	files map[string]int64
	dirs  map[string]bool
	home  string
}

func (e fakeEnv) getenv(k string) string { return e.vars[k] }

func (e fakeEnv) lookPath(f string) (string, error) {
	if p, ok := e.paths[f]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func (e fakeEnv) statSize(p string) (int64, bool, bool) {
	if e.dirs[p] {
		return 0, true, true
	}
	if sz, ok := e.files[p]; ok {
		return sz, false, true
	}
	return 0, false, false
}

func (e fakeEnv) userHomeDir() (string, error) { return e.home, nil }

func TestDetectOrderIsCatalogOrder(t *testing.T) {
	apps := detect(fakeEnv{})
	want := []ID{IDOllama, IDLMStudio, IDOpenWebUI, IDJan, IDAnythingLLM}
	if len(apps) != len(want) {
		t.Fatalf("detect() returned %d apps, want %d", len(apps), len(want))
	}
	for i, id := range want {
		if apps[i].ID != id {
			t.Fatalf("apps[%d].ID = %q, want %q", i, apps[i].ID, id)
		}
		if apps[i].DownloadURL == "" {
			t.Errorf("%s has no download URL", id)
		}
	}
}

func TestNothingFoundOnABareMachine(t *testing.T) {
	for _, a := range detect(fakeEnv{}) {
		if a.Found {
			t.Errorf("%s reported found on an env with nothing on disk and nothing on $PATH", a.ID)
		}
		if a.Path != "" {
			t.Errorf("%s has a Path %q despite Found being false", a.ID, a.Path)
		}
	}
}

func TestOpenWebUICarriesAMaturityNote(t *testing.T) {
	for _, a := range detect(fakeEnv{}) {
		if a.ID == IDOpenWebUI {
			if a.Note == "" {
				t.Fatal("the Open WebUI desktop app should carry a maturity note (build-plan step 7)")
			}
			return
		}
	}
	t.Fatal("openwebui missing from the catalog")
}

func TestEveryOtherAppHasNoNote(t *testing.T) {
	for _, a := range detect(fakeEnv{}) {
		if a.ID != IDOpenWebUI && a.Note != "" {
			t.Errorf("%s unexpectedly carries a note: %q", a.ID, a.Note)
		}
	}
}
