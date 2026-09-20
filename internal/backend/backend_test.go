package backend

import (
	"context"
	"testing"
)

// fake is a minimal Backend for registry tests; it does not exercise the
// runtime-driving methods (see ollama_test.go — no wait, this package has
// no ollama import; internal/backend/ollama tests those against a real
// HTTP server).
type fake struct{ name string }

func (f fake) Name() string { return f.name }
func (f fake) Detect(context.Context) (Status, error) {
	return Status{State: StateNotInstalled}, nil
}
func (f fake) Models(context.Context) ([]Installed, error)     { return nil, nil }
func (f fake) Show(context.Context, string) (ModelInfo, error) { return ModelInfo{}, nil }
func (f fake) Running(context.Context) ([]Loaded, error)       { return nil, nil }
func (f fake) Pull(context.Context, ModelSource, func(PullProgress)) error {
	return ErrUnsupportedSource
}
func (f fake) Generate(context.Context, GenerateRequest, func(GenerateEvent) error) error {
	return nil
}
func (f fake) Unload(context.Context, string) error                 { return nil }
func (f fake) Delete(context.Context, string) error                 { return nil }
func (f fake) Install(context.Context, func(InstallProgress)) error { return nil }
func (f fake) Start(context.Context) error                          { return nil }

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register(fake{"ollama"})
	r.Register(fake{"llamacpp"})

	if b, ok := r.Lookup("ollama"); !ok || b.Name() != "ollama" {
		t.Fatalf("Lookup(ollama) = %v, %v", b, ok)
	}
	if _, ok := r.Lookup("nope"); ok {
		t.Fatal("Lookup of an unknown name must fail")
	}
	all := r.All()
	if len(all) != 2 || all[0].Name() != "llamacpp" || all[1].Name() != "ollama" {
		t.Fatalf("All() must be sorted by name: %v", all)
	}
}

func TestRegisterTwicePanics(t *testing.T) {
	r := NewRegistry()
	r.Register(fake{"ollama"})
	defer func() {
		if recover() == nil {
			t.Fatal("registering the same name twice must panic")
		}
	}()
	r.Register(fake{"ollama"})
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	r := NewRegistry()
	defer func() {
		if recover() == nil {
			t.Fatal("registering an empty name must panic")
		}
	}()
	r.Register(fake{""})
}

func TestModelSourceValidate(t *testing.T) {
	cases := []struct {
		name string
		src  ModelSource
		ok   bool
	}{
		{"ollama tag ok", ModelSource{Kind: SourceOllamaTag, OllamaTag: "llama3.1:8b"}, true},
		{"ollama tag empty", ModelSource{Kind: SourceOllamaTag}, false},
		{"hf ok", ModelSource{Kind: SourceHuggingFace, HFRepo: "org/repo", HFFile: "m.gguf"}, true},
		{"hf missing file", ModelSource{Kind: SourceHuggingFace, HFRepo: "org/repo"}, false},
		{"hf missing repo", ModelSource{Kind: SourceHuggingFace, HFFile: "m.gguf"}, false},
		{"unknown kind", ModelSource{Kind: "bogus"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.src.Validate()
			if c.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !c.ok && err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
		})
	}
}

func TestModelSourceString(t *testing.T) {
	if got := (ModelSource{Kind: SourceOllamaTag, OllamaTag: "llama3.1:8b"}).String(); got != "llama3.1:8b" {
		t.Fatalf("String() = %q", got)
	}
	if got := (ModelSource{Kind: SourceHuggingFace, HFRepo: "org/repo", HFFile: "m.gguf"}).String(); got != "org/repo/m.gguf" {
		t.Fatalf("String() = %q", got)
	}
}
