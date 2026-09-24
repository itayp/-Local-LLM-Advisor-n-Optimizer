package store

import (
	"context"
	"testing"

	"advisor/internal/catalog"
)

func syncTwoSizes(t *testing.T, s *Store) (small, large int64) {
	t.Helper()
	cat := &catalog.Catalogue{Quants: []string{"Q4_K_M"}, Families: []catalog.Family{{
		ID: "llama3.2", DisplayName: "Llama 3.2", Purposes: []catalog.Purpose{catalog.PurposeChat}, ReviewedAt: "2026-09-18",
		Sizes: []catalog.Size{
			{Parameters: 1_230_000_000, ContextLength: 131072, OllamaTag: "llama3.2:1b", HFRepo: "b/L1-GGUF", HFBaseRepo: "meta-llama/Llama-3.2-1B-Instruct"},
			{Parameters: 3_210_000_000, ContextLength: 131072, OllamaTag: "llama3.2:3b", HFRepo: "b/L3-GGUF", HFBaseRepo: "meta-llama/Llama-3.2-3B-Instruct", OllamaQuant: "Q4_K_M"},
		},
	}}}
	ids, err := s.SyncCatalogModels(context.Background(), cat)
	if err != nil {
		t.Fatal(err)
	}
	return ids[CatalogKey{"llama3.2", 1_230_000_000}], ids[CatalogKey{"llama3.2", 3_210_000_000}]
}

func TestCatalogModelsCarryTheBaseRepoAndReleaseDate(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	_, large := syncTwoSizes(t, s)
	if err := s.SetReleasedAt(ctx, large, "2024-09-25"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.CatalogModels(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var got catalog.Model
	for _, r := range rows {
		if r.Model.ID == large {
			got = r.Model
		}
	}
	if got.Size.HFBaseRepo != "meta-llama/Llama-3.2-3B-Instruct" || got.Size.OllamaQuant != "Q4_K_M" || got.ReleasedAt != "2024-09-25" {
		t.Fatalf("stored size = %+v, released %q", got.Size, got.ReleasedAt)
	}
}

// A value that leaves its source is marked absent, never deleted (the D-30 /
// D-35 pattern), and only within the scope that was read.
func TestReplaceExternalMarksAbsentWithinScope(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	small, large := syncTwoSizes(t, s)
	row := func(model int64, name, metric string, v float64) catalog.External {
		return catalog.External{Source: "arena", SourceModel: name, ModelID: model, Metric: metric, Value: v,
			SourceDate: "2026-09-15", Provenance: "crowd", Attribution: "Arena", License: "CC-BY-4.0",
			Detail: map[string]any{"votes": 1200.0}}
	}
	if _, err := s.ReplaceExternal(ctx, ExternalScope{Source: "arena", Metric: "arena:text/overall"}, []catalog.External{
		row(small, "llama-3.2-1b-instruct", "arena:text/overall", 1100),
		row(large, "llama-3.2-3b-instruct", "arena:text/overall", 1200),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceExternal(ctx, ExternalScope{Source: "arena", Metric: "arena:text/coding"}, []catalog.External{
		row(large, "llama-3.2-3b-instruct", "arena:text/coding", 1150),
	}); err != nil {
		t.Fatal(err)
	}
	// The next read of the overall board no longer lists the 1B model.
	gone, err := s.ReplaceExternal(ctx, ExternalScope{Source: "arena", Metric: "arena:text/overall"}, []catalog.External{
		row(large, "llama-3.2-3b-instruct", "arena:text/overall", 1210),
	})
	if err != nil || gone != 1 {
		t.Fatalf("gone = %d, %v; want 1", gone, err)
	}
	present, err := s.ExternalValues(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(present) != 2 {
		t.Fatalf("present = %+v, want the 3B's two values (coding untouched by the overall read)", present)
	}
	for _, e := range present {
		if e.ModelID != large || e.Detail["votes"] != 1200.0 {
			t.Errorf("unexpected present value %+v", e)
		}
		if e.Metric == "arena:text/overall" && e.Value != 1210 {
			t.Errorf("value not updated: %+v", e)
		}
	}
	all, _ := s.ExternalValues(ctx, true)
	if len(all) != 3 {
		t.Fatalf("all = %d rows, want 3: an absent value is kept", len(all))
	}
}

func TestExternalProvenanceIsConstrained(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	small, _ := syncTwoSizes(t, s)
	_, err := s.ReplaceExternal(ctx, ExternalScope{Source: "epoch"}, []catalog.External{{
		Source: "epoch", SourceModel: "x", ModelID: small, Metric: "epoch:gpqa_diamond", Provenance: "measured",
	}})
	if err == nil {
		t.Fatal("a provenance outside maker|verified|independent|crowd must be refused by the schema")
	}
	if _, err := s.ReplaceExternal(ctx, ExternalScope{Source: "epoch"}, []catalog.External{{
		Source: "epoch", SourceModel: "x", Metric: "epoch:gpqa_diamond", Provenance: "independent",
	}}); err == nil {
		t.Fatal("a value that maps to no catalogue size must not be stored")
	}
}

func TestExternalStateRoundTrips(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, ok, err := s.GetExternalState(ctx, "epoch", ""); ok || err != nil {
		t.Fatalf("empty state: ok=%v err=%v", ok, err)
	}
	want := ExternalState{Source: "epoch", Key: "", ETag: `"abc"`, LastModified: "Tue, 22 Sep 2026 10:00:00 GMT",
		ConfigDigest: "d1", AttemptedAt: "2026-09-24T10:00:00Z", OKAt: "2026-09-24T10:00:00Z"}
	if err := s.PutExternalState(ctx, want); err != nil {
		t.Fatal(err)
	}
	want.Error = "Epoch AI could not be reached"
	if err := s.PutExternalState(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetExternalState(ctx, "epoch", "")
	if !ok || err != nil || got != want {
		t.Fatalf("got %+v ok=%v err=%v, want %+v", got, ok, err, want)
	}
	all, err := s.ExternalStates(ctx)
	if err != nil || all["epoch"] != want {
		t.Fatalf("ExternalStates = %+v, %v", all, err)
	}
}
