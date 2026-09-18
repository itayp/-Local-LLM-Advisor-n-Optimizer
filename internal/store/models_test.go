package store

import (
	"context"
	"testing"

	"advisor/internal/backend"
)

func TestUpsertInstalledModelsTracksPresence(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	first := []backend.Installed{
		{Name: "llama3.1:8b", Digest: "sha256:aaa", SizeBytes: 4_920_000_000, Family: "llama", ParameterSize: "8B", Quantization: "Q4_K_M"},
		{Name: "qwen3:4b", Digest: "sha256:bbb", SizeBytes: 2_600_000_000, Family: "qwen3", ParameterSize: "4B", Quantization: "Q4_K_M"},
	}
	if err := s.UpsertInstalledModels(ctx, "ollama", first); err != nil {
		t.Fatal(err)
	}
	got, err := s.InstalledModels(ctx, "ollama")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "llama3.1:8b" || got[0].SizeBytes != 4_920_000_000 || !got[0].Present {
		t.Fatalf("got = %+v", got)
	}

	// A second sync that drops qwen3:4b and updates llama3.1:8b's digest.
	second := []backend.Installed{
		{Name: "llama3.1:8b", Digest: "sha256:ccc", SizeBytes: 4_920_000_000, Family: "llama", ParameterSize: "8B", Quantization: "Q4_K_M"},
	}
	if err := s.UpsertInstalledModels(ctx, "ollama", second); err != nil {
		t.Fatal(err)
	}
	got, err = s.InstalledModels(ctx, "ollama")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "llama3.1:8b" || got[0].Digest != "sha256:ccc" {
		t.Fatalf("after second sync: %+v", got)
	}

	// qwen3:4b still exists in the table, just not present — history for
	// anything that referenced it by name is kept.
	var present int
	if err := s.DB().QueryRowContext(ctx, `SELECT present FROM installed_models WHERE backend_name = 'ollama' AND name = 'qwen3:4b'`).
		Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != 0 {
		t.Fatalf("qwen3:4b should be marked absent, not deleted: present = %d", present)
	}
}

func TestAllInstalledModelsSpansBackends(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if err := s.UpsertInstalledModels(ctx, "ollama", []backend.Installed{{Name: "llama3.1:8b", SizeBytes: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertInstalledModels(ctx, "llamacpp", []backend.Installed{{Name: "qwen3-4b.gguf", SizeBytes: 2}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AllInstalledModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].BackendName != "llamacpp" || got[1].BackendName != "ollama" {
		t.Fatalf("got = %+v", got)
	}
}

func TestInstalledModelsEmptyBackend(t *testing.T) {
	s := openTemp(t)
	got, err := s.InstalledModels(context.Background(), "ollama")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want empty", got)
	}
}
