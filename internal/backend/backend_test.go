package backend

import (
	"context"
	"testing"
)

type fake struct{ name string }

func (f fake) Name() string { return f.name }
func (f fake) Detect(context.Context) (Status, error) {
	return Status{State: StateNotInstalled}, nil
}

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
