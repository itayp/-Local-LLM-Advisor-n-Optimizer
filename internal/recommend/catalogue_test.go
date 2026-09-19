package recommend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/hardware"
)

// The catalogue the tests recommend from.
//
// CAPTURED: every size's download bytes (to the 0.1 GB the refresh prints),
// vision-encoder bytes, architecture, layer count, head counts, stated head
// dimension and context are what `advisor catalog refresh` resolved on the
// fleet's M1 Pro on 2026-09-19 (verify.log, build c1013aa); parameters,
// active parameters, purposes and their order are data/catalog/families.yaml.
//
// CHOSEN: what that log does not print — Gemma 4's per-layer pattern, heads
// and shared layers, the recurrent-state sizes of the hybrids, which of
// Nemotron's layers attend. The values follow the model cards and are good
// enough to exercise the engine; the real headers decide on a real machine.
type fixtureSize struct {
	family, display string
	purposes        []catalog.Purpose
	tag             string
	params, active  float64 // billions
	ctx             int
	quant           string
	gb, projGB      float64
	arch            string
	layers          int
	heads, kvHeads  int
	keyLen, valLen  int
	experts         int
	window          int
	kv              map[string]any
}

const (
	pChat   = catalog.PurposeChat
	pCode   = catalog.PurposeCoding
	pReason = catalog.PurposeReasoning
	pLong   = catalog.PurposeLongContext
	pVision = catalog.PurposeVision
	pAgent  = catalog.PurposeAgentic
	pWrite  = catalog.PurposeWriting
)

func qwenHybrid(arch string) map[string]any {
	return map[string]any{
		arch + ".nextn_predict_layers": float64(1),
		arch + ".ssm.conv_kernel":      float64(4), arch + ".ssm.inner_size": float64(4096),
		arch + ".ssm.state_size": float64(128), arch + ".ssm.group_count": float64(16),
	}
}

// gemmaLayers writes a 5-sliding-to-1-full pattern with per-layer heads.
func gemmaLayers(n, slidingHeads, fullHeads, shared int) map[string]any {
	pattern, heads := make([]any, n), make([]any, n)
	for i := range pattern {
		sliding := (i+1)%6 != 0
		pattern[i] = sliding
		heads[i] = float64(fullHeads)
		if sliding {
			heads[i] = float64(slidingHeads)
		}
	}
	kv := map[string]any{
		"gemma4.attention.sliding_window_pattern": pattern,
		"gemma4.attention.head_count_kv":          heads,
		"gemma4.attention.key_length_swa":         float64(256),
		"gemma4.attention.value_length_swa":       float64(256),
	}
	if shared > 0 {
		kv["gemma4.attention.shared_kv_layers"] = float64(shared)
	}
	return kv
}

func nemotronLayers(n int) map[string]any {
	heads, ffn := make([]any, n), make([]any, n)
	for i := range heads {
		heads[i], ffn[i] = float64(0), float64(0)
		switch {
		case i%9 == 8 && i < 54: // six attention layers of 52
			heads[i] = float64(2)
		case i%2 == 1:
			ffn[i] = float64(1856) // expert layers keep no state
		}
	}
	return map[string]any{
		"nemotron_h_moe.attention.head_count_kv": heads, "nemotron_h_moe.feed_forward_length": ffn,
		"nemotron_h_moe.ssm.conv_kernel": float64(4), "nemotron_h_moe.ssm.inner_size": float64(4096),
		"nemotron_h_moe.ssm.state_size": float64(128), "nemotron_h_moe.ssm.group_count": float64(8),
	}
}

var (
	qwen35Purposes  = []catalog.Purpose{pChat, pCode, pReason, pAgent, pVision, pLong}
	qwen36Purposes  = []catalog.Purpose{pCode, pAgent, pReason, pChat, pVision, pLong}
	qwen38Purposes  = []catalog.Purpose{pCode, pAgent, pReason, pChat, pWrite, pVision, pLong}
	gemmaPurposes   = []catalog.Purpose{pChat, pWrite, pVision, pReason, pLong}
	gptossPurposes  = []catalog.Purpose{pReason, pCode, pAgent, pChat}
	devstralPurpose = []catalog.Purpose{pCode, pAgent}
	ministralPurp   = []catalog.Purpose{pChat, pWrite, pVision, pAgent}
	nemotronPurp    = []catalog.Purpose{pAgent, pReason, pCode, pChat, pLong}
	glmPurposes     = []catalog.Purpose{pCode, pAgent, pReason}
	llamaPurposes   = []catalog.Purpose{pChat, pWrite}
)

var fixtureSizes = []fixtureSize{
	{"qwen3.5", "Qwen3.5", qwen35Purposes, "qwen3.5:0.8b", 0.8, 0, 262144, "Q4_K_M", 0.6, 0.2, "qwen35", 25, 8, 2, 256, 256, 0, 0, qwenHybrid("qwen35")},
	{"qwen3.5", "Qwen3.5", qwen35Purposes, "qwen3.5:2b", 2, 0, 262144, "Q4_K_M", 1.4, 0.7, "qwen35", 25, 8, 2, 256, 256, 0, 0, qwenHybrid("qwen35")},
	{"qwen3.5", "Qwen3.5", qwen35Purposes, "qwen3.5:4b", 4, 0, 262144, "Q4_K_M", 3.0, 0.7, "qwen35", 33, 16, 4, 256, 256, 0, 0, qwenHybrid("qwen35")},
	{"qwen3.5", "Qwen3.5", qwen35Purposes, "qwen3.5:9b", 9, 0, 262144, "Q4_K_M", 6.2, 0.9, "qwen35", 33, 16, 4, 256, 256, 0, 0, qwenHybrid("qwen35")},
	{"qwen3.6", "Qwen3.6", qwen36Purposes, "qwen3.6:35b", 35, 3, 262144, "Q4_K_M", 22.3, 0.9, "qwen35moe", 41, 16, 2, 256, 256, 256, 0, qwenHybrid("qwen35moe")},
	{"qwen3.8", "Qwen3.8", qwen38Purposes, "qwen3.8:27b", 27, 0, 262144, "Q4_K_M", 17.4, 0.9, "qwen35", 65, 24, 4, 256, 256, 0, 0, qwenHybrid("qwen35")},
	{"gemma4", "Gemma 4", gemmaPurposes, "gemma4:e2b", 5.1, 2.3, 131072, "Q4_K_M", 3.5, 1.0, "gemma4", 35, 8, 1, 512, 512, 0, 512, gemmaLayers(35, 1, 1, 20)},
	{"gemma4", "Gemma 4", gemmaPurposes, "gemma4:e4b", 8, 4.5, 131072, "Q4_K_M", 5.4, 1.0, "gemma4", 42, 8, 2, 512, 512, 0, 512, gemmaLayers(42, 2, 2, 18)},
	{"gemma4", "Gemma 4", gemmaPurposes, "gemma4:12b", 11.95, 0, 131072, "Q4_K_M", 7.7, 0.1, "gemma4", 48, 16, 8, 512, 512, 0, 1024, gemmaLayers(48, 8, 4, 0)},
	{"gemma4", "Gemma 4", gemmaPurposes, "gemma4:26b", 25.2, 3.8, 262144, "Q4_K_M", 17.0, 1.2, "gemma4", 30, 16, 8, 512, 512, 128, 1024, gemmaLayers(30, 8, 2, 0)},
	{"gemma4", "Gemma 4", gemmaPurposes, "gemma4:31b", 30.7, 0, 262144, "Q4_K_M", 19.6, 1.2, "gemma4", 60, 32, 16, 512, 512, 0, 1024, gemmaLayers(60, 16, 4, 0)},
	{"gpt-oss", "gpt-oss", gptossPurposes, "gpt-oss:20b", 21, 3.6, 131072, "MXFP4", 12.1, 0, "gpt-oss", 24, 64, 8, 64, 64, 32, 128, nil},
	{"devstral-small-2", "Devstral Small 2", devstralPurpose, "devstral-small-2:24b", 24, 0, 393216, "Q4_K_M", 14.3, 0, "mistral3", 40, 32, 8, 128, 128, 0, 0, nil},
	{"ministral-3", "Ministral 3", ministralPurp, "ministral-3:3b", 3.4, 0, 262144, "Q4_K_M", 2.1, 0.8, "mistral3", 26, 32, 8, 128, 128, 0, 0, nil},
	{"ministral-3", "Ministral 3", ministralPurp, "ministral-3:8b", 8.4, 0, 262144, "Q4_K_M", 5.2, 0.9, "mistral3", 34, 32, 8, 128, 128, 0, 0, nil},
	{"ministral-3", "Ministral 3", ministralPurp, "ministral-3:14b", 13.5, 0, 262144, "Q4_K_M", 8.2, 0.9, "mistral3", 40, 32, 8, 128, 128, 0, 0, nil},
	{"nemotron-3-nano", "Nemotron 3 Nano", nemotronPurp, "nemotron-3-nano:30b", 30, 3.5, 1048576, "Q4_K_M", 24.7, 0, "nemotron_h_moe", 52, 32, 2, 128, 128, 128, 0, nemotronLayers(52)},
	{"glm-4.7-flash", "GLM-4.7-Flash", glmPurposes, "glm-4.7-flash", 31, 3, 202752, "Q4_K_M", 18.5, 0, "deepseek2", 47, 20, 1, 576, 512, 64, 0,
		map[string]any{"deepseek2.attention.key_length_mla": float64(256), "deepseek2.attention.value_length_mla": float64(256), "deepseek2.nextn_predict_layers": float64(1)}},
	{"llama3.2", "Llama 3.2", llamaPurposes, "llama3.2:1b", 1.23, 0, 131072, "Q4_K_M", 0.8, 0, "llama", 16, 32, 8, 64, 64, 0, 0, nil},
	{"llama3.2", "Llama 3.2", llamaPurposes, "llama3.2:3b", 3.21, 0, 131072, "Q4_K_M", 2.0, 0, "llama", 28, 24, 8, 128, 128, 0, 0, nil},
	{"llama3.1", "Llama 3.1", llamaPurposes, "llama3.1:8b", 8.03, 0, 131072, "Q4_K_M", 4.9, 0, "llama", 32, 32, 8, 0, 0, 0, 0, nil},
	{"llama3.3", "Llama 3.3", llamaPurposes, "llama3.3:70b", 70.6, 0, 131072, "Q4_K_M", 42.5, 0, "llama", 80, 64, 8, 128, 128, 0, 0, nil},
}

// fleetCatalogue builds the engine's catalogue from the fixture: one entry
// per size, its default-quant file, a second (larger) quant so that the
// engine has something to ignore, and the vision encoder where there is one.
func fleetCatalogue() []Entry {
	var out []Entry
	for i, s := range fixtureSizes {
		modelID := int64(i + 1)
		h := catalog.GGUFHeader{
			Architecture: s.arch, BlockCount: s.layers, HeadCount: s.heads, HeadCountKV: s.kvHeads, HeadCountKVStated: true,
			KeyLength: s.keyLen, ValueLength: s.valLen, EmbeddingLength: 4096, ContextLength: s.ctx,
			SlidingWindow: s.window, ExpertCount: s.experts, HasVision: s.projGB > 0, FileType: -1,
		}
		if s.arch == "qwen35" || s.arch == "qwen35moe" {
			h.FullAttentionInterval = 4
		}
		file := func(id int64, quant string, gb float64) catalog.File {
			return catalog.File{
				ID: id, ModelID: modelID, Filename: s.tag + "-" + quant + ".gguf", Role: catalog.RoleModel, Quant: quant,
				Parts: 1, Bytes: uint64(gb * 1e9), Present: true, Header: h, Layout: catalog.NewLayout(h, s.kv),
			}
		}
		m := catalog.Model{
			ID: modelID, FamilyID: s.family, Present: true,
			Size: catalog.Size{Parameters: uint64(s.params * 1e9), ActiveParameters: uint64(s.active * 1e9), ContextLength: s.ctx, OllamaTag: s.tag},
		}
		m.Files = append(m.Files, file(modelID*10+1, s.quant, s.gb))
		if s.quant == "Q4_K_M" {
			m.Files = append(m.Files, file(modelID*10+2, "Q8_0", s.gb*1.75))
		}
		if s.projGB > 0 {
			m.Files = append(m.Files, catalog.File{ID: modelID*10 + 3, ModelID: modelID, Filename: "mmproj-F16.gguf", Role: catalog.RoleProjector, Quant: "F16",
				Parts: 1, Bytes: uint64(s.projGB * 1e9), Present: true, Header: catalog.GGUFHeader{Architecture: "clip", FileType: -1}})
		}
		out = append(out, Entry{FamilyID: s.family, DisplayName: s.display, Purposes: s.purposes, Model: m})
	}
	return out
}

func entryByTag(t *testing.T, cat []Entry, tag string) Entry {
	t.Helper()
	for _, e := range cat {
		if e.Model.Size.OllamaTag == tag {
			return e
		}
	}
	t.Fatalf("no %s in the fixture catalogue", tag)
	return Entry{}
}

// installedFromCatalogue is an installed model the catalogue knows.
func installedFromCatalogue(t *testing.T, cat []Entry, tag string) InstalledModel {
	e := entryByTag(t, cat, tag)
	return InstalledModel{Name: tag, SizeBytes: e.Model.Files[0].Bytes, CatalogModelID: e.Model.ID, CatalogFileID: e.Model.Files[0].ID}
}

// goldenMachine loads one of internal/hardware's golden profiles.
func goldenMachine(t *testing.T, name string) estimate.Machine {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "hardware", "testdata", "golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var p hardware.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return estimate.Machine{Profile: p}
}

func mustEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(fleetCatalogue())
	if err != nil {
		t.Fatal(err)
	}
	return e
}
