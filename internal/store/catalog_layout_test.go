package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"advisor/internal/catalog"
)

// A file's Layout is derived from header_json on every read — never stored —
// so a better reading of a header needs no catalogue refresh, and the rows a
// step 4 build wrote serve the step 5 estimator as they are. The JSON round
// trip turns every number into a float64 and every list into []any; this is
// the test that the derivation reads them anyway.
func TestCatalogFilesCarryALayoutDerivedFromHeaderJSON(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	cat := &catalog.Catalogue{Quants: []string{"Q4_K_M"}, Families: []catalog.Family{{
		ID: "qwen3.5", DisplayName: "Qwen3.5", Purposes: []catalog.Purpose{catalog.PurposeChat}, ReviewedAt: "2026-09-18",
		Sizes: []catalog.Size{{Parameters: 9_000_000_000, ContextLength: 262144, OllamaTag: "qwen3.5:9b", HFRepo: "bartowski/Qwen_Qwen3.5-9B-GGUF"}},
	}}}
	ids, err := s.SyncCatalogModels(ctx, cat)
	if err != nil {
		t.Fatal(err)
	}
	modelID := ids[CatalogKey{"qwen3.5", 9_000_000_000}]

	header := catalog.GGUFHeader{
		Architecture: "qwen35", BlockCount: 33, HeadCount: 16, HeadCountKV: 4, HeadCountKVStated: true,
		KeyLength: 256, ValueLength: 256, EmbeddingLength: 4096, ContextLength: 262144, FullAttentionInterval: 4, FileType: -1,
	}
	headerJSON, err := json.Marshal(map[string]any{"kv": map[string]any{
		"general.architecture":        "qwen35",
		"qwen35.block_count":          uint64(33),
		"qwen35.nextn_predict_layers": uint64(1),
		"qwen35.ssm.conv_kernel":      uint64(4),
		"qwen35.ssm.inner_size":       uint64(4096),
		"qwen35.ssm.state_size":       uint64(128),
		"qwen35.ssm.group_count":      uint64(16),
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = s.RecordCatalogModelRefresh(ctx, modelID, CatalogModelRefresh{HFSHA: "abc", Files: []CatalogFileWrite{
		{File: catalog.File{Filename: "Qwen3.5-9B-Q4_K_M.gguf", Role: catalog.RoleModel, Quant: "Q4_K_M", Parts: 1, Bytes: 6_200_000_000, Header: header, FetchedAt: time.Now()}, HeaderJSON: headerJSON},
		{File: catalog.File{Filename: "mmproj-F16.gguf", Role: catalog.RoleProjector, Quant: "F16", Parts: 1, Bytes: 900_000_000, Header: catalog.GGUFHeader{Architecture: "clip", FileType: -1}, FetchedAt: time.Now()}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := s.CatalogModels(ctx, false)
	if err != nil || len(rows) != 1 || len(rows[0].Model.Files) != 2 {
		t.Fatalf("catalogue: %v %+v", err, rows)
	}
	for _, f := range rows[0].Model.Files {
		switch f.Role {
		case catalog.RoleModel:
			l := f.Layout
			// 33 blocks less one prediction layer; one in four attends.
			if l.Basis != catalog.LayoutStated || l.CacheLayers() != 8 || l.RecurrentLayers != 24 || l.StatelessLayers != 1 {
				t.Errorf("layout from header_json: %+v", l)
			}
			if want := uint64(3*(4096+2*16*128) + 128*4096); l.RecurrentStateElements != want {
				t.Errorf("recurrent state %d elements, want %d", l.RecurrentStateElements, want)
			}
		case catalog.RoleProjector:
			if len(f.Layout.Groups) != 0 {
				t.Errorf("a vision encoder has no language-model layout: %+v", f.Layout)
			}
		}
	}
}
