//go:build linux

package chatapps

import "testing"

func TestLinuxFindsACLICompanionOnPATH(t *testing.T) {
	e := fakeEnv{paths: map[string]string{"lms": "/usr/bin/lms"}}
	apps := detect(e)
	for _, a := range apps {
		if a.ID == IDLMStudio {
			if !a.Found || a.Path != "/usr/bin/lms" {
				t.Fatalf("LM Studio = %+v, want found at /usr/bin/lms", a)
			}
			return
		}
	}
	t.Fatal("lmstudio missing")
}

func TestLinuxFallsBackToOptWhenNotOnPATH(t *testing.T) {
	e := fakeEnv{files: map[string]int64{"/opt/Jan/jan": 12345}}
	apps := detect(e)
	for _, a := range apps {
		if a.ID == IDJan {
			if !a.Found || a.Path != "/opt/Jan/jan" {
				t.Fatalf("Jan = %+v, want found at /opt/Jan/jan", a)
			}
			return
		}
	}
	t.Fatal("jan missing")
}

func TestLinuxOllamaHasNoDesktopAppKnownYet(t *testing.T) {
	// Ollama ships no Linux desktop app as of this build (D-4's note): even
	// with something on $PATH called "ollama" (the CLI, not a chat app),
	// this package must not claim it found a chat app there.
	e := fakeEnv{paths: map[string]string{"ollama": "/usr/bin/ollama"}}
	for _, a := range detect(e) {
		if a.ID == IDOllama && a.Found {
			t.Fatalf("Ollama chat app incorrectly reported found via the CLI binary: %+v", a)
		}
	}
}
