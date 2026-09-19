package estimate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"advisor/internal/catalog"
	"advisor/internal/figure"
	"advisor/internal/hardware"
)

// goldenMachine loads one of internal/hardware's golden profiles — what
// detection derives from a fixture machine — so that the estimator is
// tested against the profiles the daemon actually produces.
func goldenMachine(t *testing.T, name string) Machine {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "hardware", "testdata", "golden", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var p hardware.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return Machine{Profile: p}
}

// dense is a plain Llama-shaped model of the given file size: 32 layers, 8
// KV heads of 128 — 131,072 bytes of f16 cache per token.
func dense(bytes uint64) Model {
	return Model{
		File: catalog.File{ID: 1, Role: catalog.RoleModel, Quant: "Q4_K_M", Bytes: bytes, Header: catalog.GGUFHeader{
			Architecture: "llama", BlockCount: 32, HeadCount: 32, HeadCountKV: 8, HeadCountKVStated: true,
			KeyLength: 128, ValueLength: 128, EmbeddingLength: 4096, ContextLength: 131072,
		}},
		Size: catalog.Size{Parameters: 8_000_000_000, ContextLength: 131072},
	}
}

func TestCategoriesAndTheThresholdThatDecided(t *testing.T) {
	e := mustEstimator(t)
	card := goldenMachine(t, "WindowsNVIDIADesktop") // RTX 5070 Ti, 15.9 GiB, 31.9 GiB of RAM
	budget := card.Profile.GPUUsableBytes
	kv4k := uint64(131072 * 4096)
	over := e.Config.Overhead[hardware.PathCUDA]
	weightsFor := func(share float64) uint64 { return uint64(share*float64(budget)) - kv4k - over }

	cases := []struct {
		name    string
		weights uint64
		ctx     int
		want    Category
		says    string
	}{
		{"well under the headroom line", weightsFor(0.50), 4096, FitsWithHeadroom, "at or under 80% fits with headroom"},
		{"just under the headroom line", weightsFor(0.795), 4096, FitsWithHeadroom, "fits with headroom"},
		{"between the lines", weightsFor(0.86), 4096, Fits, "at or under 92% fits"},
		{"over the line at the shortest context, room in RAM", weightsFor(1.10), 4096, NeedsCPUOffload, "layers fit on the graphics"},
		{"over the line only because of a long context", weightsFor(0.60), 65536, ReducedContextOnly, "at a context of 32,768"},
		{"more than graphics memory and RAM together", 60 * gib, 4096, NotRecommended, "more than the"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			est := e.Fit(card, dense(c.weights), Request{NumCtx: c.ctx})
			if est.Category != c.want {
				t.Fatalf("category %q, want %q\nthreshold: %s", est.Category, c.want, est.Threshold)
			}
			if !strings.Contains(est.Threshold, c.says) {
				t.Errorf("the threshold sentence should say %q:\n%s", c.says, est.Threshold)
			}
			if est.BudgetBytes != budget || est.BudgetKind != BudgetGraphics || !est.BudgetKnown {
				t.Errorf("budget %d (%s, known %v), want the card's %d", est.BudgetBytes, est.BudgetKind, est.BudgetKnown, budget)
			}
			m := est.Memory
			if m.Total.Value != m.Weights.Value+m.KVCache.Value+m.Overhead.Value {
				t.Errorf("total %d is not weights + cache + overhead", m.Total.Value)
			}
			if m.GPUResident.Value+m.CPUOffload.Value != m.Total.Value {
				t.Errorf("gpu_resident %d + cpu_offload %d is not the total %d", m.GPUResident.Value, m.CPUOffload.Value, m.Total.Value)
			}
			for name, f := range map[string]figure.Bytes{"weights": m.Weights, "kv_cache": m.KVCache, "overhead": m.Overhead, "total": m.Total, "gpu_resident": m.GPUResident, "cpu_offload": m.CPUOffload} {
				if f.Source != figure.Estimated {
					t.Errorf("%s has source %q; nothing here is a measurement", name, f.Source)
				}
			}
			switch c.want {
			case FitsWithHeadroom, Fits:
				if m.CPUOffload.Value != 0 {
					t.Errorf("a model that fits offloads nothing, got %d", m.CPUOffload.Value)
				}
			case NeedsCPUOffload:
				if m.CPUOffload.Value == 0 || m.GPUResident.Value == 0 || m.GPUResident.Value > uint64(0.92*float64(budget))+1 {
					t.Errorf("split: %d on the card, %d on the processor", m.GPUResident.Value, m.CPUOffload.Value)
				}
			case ReducedContextOnly:
				if est.SuggestedCtx != 32768 {
					t.Errorf("suggested context %d, want 32768", est.SuggestedCtx)
				}
			}
			if _, err := json.Marshal(est); err != nil {
				t.Errorf("the estimate must serialise (every figure needs a source): %v", err)
			}
		})
	}
}

// Apple Silicon compares against gpu_usable_bytes, not RAM: a model that
// would fit in 16 GB of memory does not fit the 11.8 GB macOS gives the GPU.
func TestAppleSiliconComparesAgainstTheGPUBudgetNotRAM(t *testing.T) {
	e := mustEstimator(t)
	mac := goldenMachine(t, "AppleSiliconM1Pro")
	if mac.Profile.RAMBytes != 16*gib || mac.Profile.GPUUsableBytes >= 12*gib {
		t.Fatalf("fixture changed: RAM %d, GPU budget %d", mac.Profile.RAMBytes, mac.Profile.GPUUsableBytes)
	}
	est := e.Fit(mac, dense(11*gib), Request{NumCtx: 4096}) // 11.5 GiB with its cache: under RAM, over the GPU's share
	if est.BudgetKind != BudgetUnified || est.BudgetBytes != mac.Profile.GPUUsableBytes {
		t.Fatalf("compared against %d (%s), want gpu_usable_bytes %d", est.BudgetBytes, est.BudgetKind, mac.Profile.GPUUsableBytes)
	}
	if est.Category.FitsOnDevice() {
		t.Errorf("category %q: 11.5 GB does not fit in the 11.8 GB the graphics can use at 92%%\n%s", est.Category, est.Threshold)
	}
	if est.Request.RuntimePath != hardware.PathMetal || est.Basis.PathSource != PathExpected {
		t.Errorf("path %q (%s), want metal, expected", est.Request.RuntimePath, est.Basis.PathSource)
	}
	// And a model that does fit the budget fits.
	if est := e.Fit(mac, dense(5*gib), Request{NumCtx: 8192}); est.Category != FitsWithHeadroom {
		t.Errorf("a 5 GB model at 8k on the M1 Pro: %q\n%s", est.Category, est.Threshold)
	}
}

// A machine without a usable graphics device compares against RAM with the
// operating system's own needs reserved.
func TestCPUOnlyComparesAgainstRAMLessTheOSReserve(t *testing.T) {
	e := mustEstimator(t)
	laptop := goldenMachine(t, "WindowsNVIDIAWithoutDriver") // 15.9 GiB of RAM, a card without a driver
	want := laptop.Profile.RAMBytes - e.Config.OSReserve["windows"]
	est := e.Fit(laptop, dense(4*gib), Request{NumCtx: 4096})
	if est.BudgetKind != BudgetSystem || est.BudgetBytes != want {
		t.Fatalf("budget %d (%s), want RAM − reserve = %d", est.BudgetBytes, est.BudgetKind, want)
	}
	if est.Request.RuntimePath != hardware.PathCPU {
		t.Errorf("path %q, want cpu", est.Request.RuntimePath)
	}
	if est.Category != FitsWithHeadroom {
		t.Errorf("4 GB in %d: %q", want, est.Category)
	}
	if est.Memory.GPUResident.Value != 0 || est.Memory.CPUOffload.Value != est.Memory.Total.Value {
		t.Errorf("on the processor everything lives in system memory: gpu %d, cpu %d, total %d",
			est.Memory.GPUResident.Value, est.Memory.CPUOffload.Value, est.Memory.Total.Value)
	}
	// Bigger than RAM − reserve: not recommended, never "offload" (there is nowhere to offload to).
	if est := e.Fit(laptop, dense(13*gib), Request{NumCtx: 4096}); est.Category != NotRecommended {
		t.Errorf("13 GB in %d: %q", want, est.Category)
	}
}

func TestPlacement(t *testing.T) {
	e := mustEstimator(t)
	cases := []struct {
		golden string
		actual hardware.RuntimePath
		path   hardware.RuntimePath
		source PathSource
		kind   BudgetKind
		known  bool
		unused UnusedKind
	}{
		{"WindowsNVIDIADesktop", "", hardware.PathCUDA, PathExpected, BudgetGraphics, true, ""},
		{"WindowsNVIDIADesktop", hardware.PathCUDA, hardware.PathCUDA, PathEstablished, BudgetGraphics, true, ""},
		{"AppleSiliconM1Pro", "", hardware.PathMetal, PathExpected, BudgetUnified, true, ""},
		{"AppleSiliconMetalUnreadableIsUnknown", "", hardware.PathMetal, PathExpected, BudgetUnified, false, ""},
		{"LinuxMacProTwoD700sOnVulkan", hardware.PathVulkan, hardware.PathVulkan, PathEstablished, BudgetGraphics, true, ""},
		{"WindowsAMDVulkan", hardware.PathCPU, hardware.PathCPU, PathEstablished, BudgetSystem, true, UnusedEstablished},
		{"WindowsAMDROCm", hardware.PathVulkan, hardware.PathVulkan, PathEstablished, BudgetGraphics, true, ""}, // what happened wins over what was expected
		{"LinuxNVIDIAWithNoDriver", "", hardware.PathCPU, PathExpected, BudgetSystem, true, UnusedUnsupported},
		{"IntelMac", "", hardware.PathCPU, PathExpected, BudgetSystem, true, UnusedUnsupported},
		{"LinuxServerWithoutGPU", "", hardware.PathCPU, PathExpected, BudgetSystem, true, ""},
		{"LinuxIntegratedIntel", "", hardware.PathVulkan, PathExpected, BudgetSystem, true, ""}, // built-in graphics: system memory, and no "card not used"
		{"LinuxNouveauBesideIntel", "", hardware.PathVulkan, PathExpected, BudgetSystem, true, ""},
	}
	for _, c := range cases {
		t.Run(c.golden+"/"+string(c.actual), func(t *testing.T) {
			m := goldenMachine(t, c.golden)
			m.ActualPath = c.actual
			pl := e.Place(m)
			if pl.Path != c.path || pl.PathSource != c.source {
				t.Errorf("path %q (%s), want %q (%s)", pl.Path, pl.PathSource, c.path, c.source)
			}
			if pl.BudgetKind != c.kind || pl.BudgetKnown != c.known {
				t.Errorf("budget %s known=%v, want %s known=%v", pl.BudgetKind, pl.BudgetKnown, c.kind, c.known)
			}
			got := UnusedKind("")
			if pl.Unused != nil {
				got = pl.Unused.Kind
				if strings.TrimSpace(pl.Unused.Why) == "" || strings.TrimSpace(pl.Unused.Name) == "" {
					t.Errorf("an unused card needs its name and a reason: %+v", pl.Unused)
				}
			}
			if got != c.unused {
				t.Errorf("unused %q, want %q", got, c.unused)
			}
		})
	}

	t.Run("an operating system too old for the runtime blocks everything", func(t *testing.T) {
		pl := e.Place(goldenMachine(t, "MacOSTooOldForOllama"))
		if pl.Blocked == "" {
			t.Error("Blocked should carry the sentence to show")
		}
	})
	t.Run("Vulkan switched off is named as the reason", func(t *testing.T) {
		m := goldenMachine(t, "WindowsAMDVulkan")
		m.ActualPath, m.RuntimeEnv = hardware.PathCPU, map[string]string{"OLLAMA_VULKAN": "0"}
		if pl := e.Place(m); pl.Unused == nil || !strings.Contains(pl.Unused.Why, "Vulkan is switched off") {
			t.Errorf("why: %+v", pl.Unused)
		}
	})
}

// An unreadable memory budget is unknown — not a fit, not a misfit, and not
// a number (ARCHITECTURE.md D-21).
func TestUnknownBudgetIsUnknown(t *testing.T) {
	e := mustEstimator(t)
	est := e.Fit(goldenMachine(t, "AppleSiliconMetalUnreadableIsUnknown"), dense(4*gib), Request{NumCtx: 4096})
	if est.Category != CategoryUnknown || est.BudgetKnown || est.Basis.BudgetKnown {
		t.Errorf("category %q, budget known %v", est.Category, est.BudgetKnown)
	}
	if est.Speed.Known || est.Speed.Generation != nil {
		t.Errorf("no placement, no speed: %+v", est.Speed)
	}
}

func TestLayoutsShapeTheCache(t *testing.T) {
	e := mustEstimator(t)
	ctx := 32768

	t.Run("sliding-window layers stop growing at the window", func(t *testing.T) {
		h := catalog.GGUFHeader{Architecture: "gpt-oss", BlockCount: 24, HeadCount: 64, HeadCountKV: 8, KeyLength: 64, ValueLength: 64, SlidingWindow: 128, ContextLength: 131072, ExpertCount: 32}
		l := catalog.NewLayout(h, nil)
		if l.Basis != catalog.LayoutArchitecture || len(l.Groups) != 2 {
			t.Fatalf("layout %+v", l)
		}
		full := uint64(12 * 8 * 128 * ctx * 2)
		window := uint64(12 * 8 * 128 * 768 * 2) // 128 + 512, rounded up to 256s
		if got := e.kvBytes(l, ctx, KVF16); got != full+window {
			t.Errorf("cache %d, want %d full + %d window", got, full, window)
		}
	})
	t.Run("a window the runtime does not use leaves the plain formula, and says so", func(t *testing.T) {
		// phi3 states a sliding window; llama.cpp runs it with full attention,
		// and step 0's phi4 rows passed on exactly that arithmetic.
		h := catalog.GGUFHeader{Architecture: "phi3", BlockCount: 40, HeadCount: 40, HeadCountKV: 10, KeyLength: 128, SlidingWindow: 131072, ContextLength: 16384}
		l := catalog.NewLayout(h, nil)
		if l.Basis != catalog.LayoutUniform || l.CacheLayers() != 40 || len(l.Notes) == 0 {
			t.Fatalf("layout %+v", l)
		}
		if got, want := e.kvBytes(l, 16384, KVF16), uint64(2*40*10*128*16384*2); got != want {
			t.Errorf("cache %d, want %d", got, want)
		}
	})
	t.Run("a hybrid model whose state size is missing is incomplete, and said so", func(t *testing.T) {
		h := catalog.GGUFHeader{Architecture: "qwen35", BlockCount: 24, HeadCount: 8, HeadCountKV: 2, KeyLength: 256, ValueLength: 256, FullAttentionInterval: 4, ContextLength: 262144}
		l := catalog.NewLayout(h, nil) // the typed columns only: no ssm.* keys
		if l.Basis != catalog.LayoutIncomplete || l.CacheLayers() != 6 || l.RecurrentLayers != 18 {
			t.Fatalf("layout %+v", l)
		}
		est := e.Fit(roomyMachine(hardware.PathCUDA), Model{File: catalog.File{Bytes: gib, Header: h, Layout: l}}, Request{NumCtx: ctx})
		if est.Basis.MemoryModel != MemoryIncomplete {
			t.Errorf("memory model %q, want incomplete", est.Basis.MemoryModel)
		}
	})
	t.Run("a dense-first pattern", func(t *testing.T) {
		h := catalog.GGUFHeader{Architecture: "smallthinker", BlockCount: 8, HeadCount: 8, HeadCountKV: 2, KeyLength: 64, SlidingWindow: 4096, ContextLength: 32768}
		l := catalog.NewLayout(h, nil)
		if l.Basis != catalog.LayoutArchitecture || len(l.Groups) != 2 || l.Groups[0].Kind != catalog.LayersFull || l.Groups[0].Layers != 2 || l.Groups[1].Layers != 6 {
			t.Fatalf("layout %+v", l)
		}
	})
	t.Run("a Gemma-style header: per-layer heads, a pattern, shared layers, shorter sliding heads", func(t *testing.T) {
		n := 12
		pattern, heads := make([]any, n), make([]any, n)
		for i := range pattern {
			sliding := (i+1)%6 != 0
			pattern[i] = sliding
			heads[i] = float64(2) // as header_json decodes numbers
			if !sliding {
				heads[i] = float64(1)
			}
		}
		kv := map[string]any{
			"gemma4.attention.sliding_window_pattern": pattern,
			"gemma4.attention.head_count_kv":          heads,
			"gemma4.attention.shared_kv_layers":       float64(6),
			"gemma4.attention.key_length_swa":         float64(256),
			"gemma4.attention.value_length_swa":       float64(256),
		}
		h := catalog.GGUFHeader{Architecture: "gemma4", BlockCount: n, HeadCount: 8, HeadCountKV: 2, KeyLength: 512, ValueLength: 512, SlidingWindow: 512, ContextLength: 131072}
		l := catalog.NewLayout(h, kv)
		// Layers 0..5 own a cache (the last six reuse it): five sliding, one full.
		if l.Basis != catalog.LayoutStated || l.CacheLayers() != 6 || l.StatelessLayers != 6 {
			t.Fatalf("layout %+v", l)
		}
		want := uint64(5*2*(256+256)*1024*2 + 1*1*(512+512)*ctx*2)
		if got := e.kvBytes(l, ctx, KVF16); got != want {
			t.Errorf("cache %d, want %d", got, want)
		}
		// Step 5's M1 Pro run showed Gemma 4 cards with no sliding-window
		// note: a pattern stated layer by layer says so too.
		if !strings.Contains(strings.Join(l.Notes, "|"), "sliding-window attention on 10 of 12 layers (window 512 tokens)") {
			t.Errorf("notes %v", l.Notes)
		}
	})
	t.Run("multi-head latent attention caches one compressed key and no values", func(t *testing.T) {
		kv := map[string]any{"deepseek2.attention.key_length_mla": float64(256), "deepseek2.attention.value_length_mla": float64(256), "deepseek2.nextn_predict_layers": float64(1)}
		h := catalog.GGUFHeader{Architecture: "deepseek2", BlockCount: 47, HeadCount: 20, HeadCountKV: 1, KeyLength: 576, ValueLength: 512, ContextLength: 202752, ExpertCount: 64}
		l := catalog.NewLayout(h, kv)
		if got, want := e.kvBytes(l, ctx, KVF16), uint64(46*1*576*ctx*2); got != want {
			t.Errorf("cache %d, want %d (46 layers × 576 × ctx × 2 B)", got, want)
		}
	})
	t.Run("a Nemotron-style header: per-layer heads with zeros", func(t *testing.T) {
		n := 8
		heads, ffn := make([]any, n), make([]any, n)
		for i := range heads {
			heads[i], ffn[i] = float64(0), float64(0) // Mamba
			if i%4 == 3 {
				heads[i] = float64(2) // attention
			} else if i%4 == 1 {
				ffn[i] = float64(1024) // a feed-forward block: no state at all
			}
		}
		kv := map[string]any{
			"nemotron_h_moe.attention.head_count_kv": heads, "nemotron_h_moe.feed_forward_length": ffn,
			"nemotron_h_moe.ssm.conv_kernel": float64(4), "nemotron_h_moe.ssm.inner_size": float64(4096),
			"nemotron_h_moe.ssm.state_size": float64(128), "nemotron_h_moe.ssm.group_count": float64(8),
		}
		h := catalog.GGUFHeader{Architecture: "nemotron_h_moe", BlockCount: n, HeadCount: 32, HeadCountKV: 2, KeyLength: 128, ValueLength: 128, ContextLength: 1 << 20, ExpertCount: 128}
		l := catalog.NewLayout(h, kv)
		if l.CacheLayers() != 2 || l.RecurrentLayers != 4 || l.StatelessLayers != 2 {
			t.Fatalf("layout %+v", l)
		}
		state := uint64(3*(4096+2*8*128) + 128*4096)
		want := uint64(2*2*(128+128)*ctx*2) + 4*state*4
		if got := e.kvBytes(l, ctx, KVF16); got != want {
			t.Errorf("cache %d, want %d", got, want)
		}
	})
	t.Run("a quantised cache is smaller by GGML's block sizes", func(t *testing.T) {
		l := catalog.NewLayout(dense(gib).File.Header, nil)
		f16 := e.kvBytes(l, ctx, KVF16)
		if q8 := e.kvBytes(l, ctx, KVQ8_0); q8 != f16*34/64 {
			t.Errorf("q8_0 cache %d, want %d", q8, f16*34/64)
		}
		if q4 := e.kvBytes(l, ctx, KVQ4_0); q4 != f16*18/64 {
			t.Errorf("q4_0 cache %d, want %d", q4, f16*18/64)
		}
	})
}

func TestVisionEncoderAndPerLayerTables(t *testing.T) {
	e := mustEstimator(t)
	card := goldenMachine(t, "WindowsNVIDIALaptop") // RTX 4060 Laptop, 8 GiB

	m := dense(5 * gib)
	proj := catalog.File{Role: catalog.RoleProjector, Bytes: gib}
	m.Projector = &proj
	with := e.Fit(card, m, Request{NumCtx: 4096})
	m.Projector = nil
	without := e.Fit(card, m, Request{NumCtx: 4096})
	if with.Memory.Weights.Value != without.Memory.Weights.Value+gib {
		t.Errorf("the vision encoder's bytes belong in the weights: %d vs %d", with.Memory.Weights.Value, without.Memory.Weights.Value)
	}
	if with.Basis.MemoryModel != MemoryModelled || without.Basis.MemoryModel != MemoryValidated {
		t.Errorf("memory model with a projector %q, without %q", with.Basis.MemoryModel, without.Basis.MemoryModel)
	}

	// Gemma's "E" sizes: 8 B parameters of which 4.5 B compute. The rest are
	// lookup tables the runtime keeps in ordinary memory — by design, so the
	// model still "fits", and the tables show up as memory off the card.
	e4b := dense(8 * gib)
	e4b.Size = catalog.Size{Parameters: 8_000_000_000, ActiveParameters: 4_500_000_000, ContextLength: 131072}
	est := e.Fit(card, e4b, Request{NumCtx: 4096})
	if !est.Category.FitsOnDevice() {
		t.Errorf("category %q: only 4.5 of 8 GB belong on the card\n%s", est.Category, est.Threshold)
	}
	if est.Memory.CPUOffload.Value < 3*gib || est.Memory.CPUOffload.Value > 4*gib {
		t.Errorf("per-layer tables off the card: %d, want about 3.5 GiB", est.Memory.CPUOffload.Value)
	}
}

func TestContextIsClampedToTheTrainedLength(t *testing.T) {
	e := mustEstimator(t)
	m := dense(9 * gib)
	m.File.Header.ContextLength = 16384 // phi4: step 0's +29% row before the clamp
	est := e.Fit(roomyMachine(hardware.PathCUDA), m, Request{NumCtx: 32768})
	if est.Memory.EffectiveCtx != 16384 || est.Request.NumCtx != 32768 {
		t.Errorf("effective context %d for a request of %d", est.Memory.EffectiveCtx, est.Request.NumCtx)
	}
	if est.Memory.KVCache.Value != 131072*16384 {
		t.Errorf("cache %d, want the clamped %d", est.Memory.KVCache.Value, 131072*16384)
	}
}

func TestOllamaDefaultContext(t *testing.T) {
	for _, c := range []struct {
		pl   Placement
		want int
	}{
		{Placement{BudgetKind: BudgetGraphics, BudgetKnown: true, BudgetBytes: 16 * gib}, 4096},
		{Placement{BudgetKind: BudgetGraphics, BudgetKnown: true, BudgetBytes: 24 * gib}, 32768},
		{Placement{BudgetKind: BudgetUnified, BudgetKnown: true, BudgetBytes: 96 * gib}, 262144},
		{Placement{BudgetKind: BudgetSystem, BudgetKnown: true, BudgetBytes: 200 * gib}, 4096},
	} {
		if got := OllamaDefaultContext(c.pl); got != c.want {
			t.Errorf("%+v: %d, want %d", c.pl, got, c.want)
		}
	}
}

// Every constant lives in the one config, and every speed entry names what
// it was measured on (build-plan step 5, items 1 and 2).
func TestConfigIsCompleteAndSaysWhereItCameFrom(t *testing.T) {
	cfg := DefaultConfig()
	want := map[hardware.RuntimePath]uint64{hardware.PathCUDA: 250 * mib, hardware.PathMetal: 0, hardware.PathVulkan: 50 * mib, hardware.PathROCm: 50 * mib, hardware.PathCPU: 0}
	for path, bytes := range want {
		if got, ok := cfg.Overhead[path]; !ok || got != bytes {
			t.Errorf("overhead[%s] = %d, step 0 validated %d (ARCHITECTURE.md D-20)", path, got, bytes)
		}
	}
	width := func(r Range) float64 { return r.High / r.Low }
	for path, ps := range cfg.Paths {
		if !strings.Contains(ps.Basis, "MEASURED") && !strings.Contains(ps.Basis, "CHOSEN") {
			t.Errorf("paths[%s]: the basis must say MEASURED or CHOSEN: %q", path, ps.Basis)
		}
		if ps.Efficiency.Low <= 0 || ps.Efficiency.High > 1 || ps.Efficiency.Low >= ps.Efficiency.High || ps.PromptRatio.Low >= ps.PromptRatio.High {
			t.Errorf("paths[%s]: %+v", path, ps)
		}
	}
	for v, ps := range cfg.VulkanByVendor {
		if !strings.Contains(ps.Basis, "MEASURED") {
			t.Errorf("vulkan[%s]: basis %q", v, ps.Basis)
		}
	}
	// "tight for cuda and metal, wider for rocm, wider still for vulkan,
	// widest for cpu".
	cuda, metal, rocm := width(cfg.Paths[hardware.PathCUDA].Efficiency), width(cfg.Paths[hardware.PathMetal].Efficiency), width(cfg.Paths[hardware.PathROCm].Efficiency)
	vulkan, cpu := width(cfg.VulkanByVendor[hardware.VendorAMD].Efficiency), width(cfg.Paths[hardware.PathCPU].Efficiency)
	if !(cuda < rocm && metal < rocm && rocm < vulkan && vulkan < cpu) {
		t.Errorf("range widths cuda %.2f, metal %.2f, rocm %.2f, vulkan %.2f, cpu %.2f are not in the order the build plan asks for", cuda, metal, rocm, vulkan, cpu)
	}
}
