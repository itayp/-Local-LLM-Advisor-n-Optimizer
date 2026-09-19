package estimate

import (
	"advisor/internal/hardware"
)

// Config holds every constant the estimator uses, in one place, each with
// the measurement that justifies it — or, where there is none yet, the
// plain statement that it was chosen and how it will be measured. Nothing
// else in this package carries a magic number.
//
// Three kinds of evidence appear below and are named as such:
//
//	MEASURED (fleet)   step 0's reports in scripts/probe0/results/, which
//	                   step0_test.go replays row by row
//	MEASURED (public)  llama.cpp's own llama-bench scoreboards, read
//	                   2026-09-19; scripts/calibrate replaces them with the
//	                   fleet's numbers as Itay runs it
//	CHOSEN             no measurement yet; the comment says what would settle it
type Config struct {
	// ---- Memory ------------------------------------------------------------

	// Overhead is what a runtime path costs on top of weights and cache,
	// per model loaded.
	//
	// MEASURED (fleet), step 0 round 3, 28 dense rows: the residual of
	// (device-memory delta − weights − KV cache) does not grow with the
	// model's width — fitted against n_batch × embedding_length the slope is
	// negative — it is a constant per backend. Medians: cuda +249 MiB (n=12,
	// RTX 5070 Ti; the CUDA context, which Ollama's own accounting leaves
	// out), vulkan +80 MiB (n=8, FirePro D700), metal −49 MiB (n=8, M1 Pro;
	// within that laptop's wired-memory noise). ARCHITECTURE.md D-20.
	//
	// rocm and cpu were NOT measured in step 0: rocm is set to vulkan's value
	// (same cards, same allocator class) and cpu to zero; both are CHOSEN
	// until a ROCm machine and a CPU-only run are in scripts/probe0/results/.
	Overhead map[hardware.RuntimePath]uint64

	// KVBytesPerElement is the size of one cached key or value element.
	// Format facts, not measurements: f16 is two bytes; GGML's q8_0 block is
	// 32 one-byte values plus a two-byte scale (34/32); q4_0 is 32 half-byte
	// values plus the scale (18/32). ggml/src/ggml-common.h, block_q8_0 and
	// block_q4_0. Step 0 measured f16 only — Ollama's default.
	KVBytesPerElement map[KVCacheType]float64

	// RecurrentBytesPerElement: hybrid models' fixed recurrent state is kept
	// in 32-bit floats whatever the cache type (llama.cpp
	// src/llama-memory-recurrent.cpp). MEASURED (fleet), on one model: step
	// 0's four minicpm-v4.6 rows (a qwen35 hybrid, on Metal and Vulkan at 4k
	// and 32k) land within 10% with this term and the one-layer-in-four
	// cache; with every layer counted the 32k rows over-predict by 40%.
	// step0_test.go replays them.
	RecurrentBytesPerElement uint64

	// SlidingBatchTokens and SlidingPadTokens size a sliding-window layer's
	// cache the way llama.cpp does: min(context, window + n_ubatch) rounded
	// up to a multiple of 256, n_ubatch being 512 by default
	// (src/llama-kv-cache-iswa.cpp). From the runtime's source; not yet
	// MEASURED on the fleet — step 0 measured no sliding-window text model
	// inside its gate (its gemma4:e4b rows on CUDA, excluded there as vision,
	// grow by 17 KB per token of context against this layout's 16 KB).
	SlidingBatchTokens int
	SlidingPadTokens   int

	// ---- Categories --------------------------------------------------------

	// HeadroomFraction and FitsFraction are the shares of the memory budget
	// at or under which a configuration "fits with headroom" and "fits".
	//
	// CHOSEN. The budget is a device's whole memory (or macOS's cap for the
	// GPU), not what is free: the desktop, a browser and the runtime's own
	// reserve take a slice, so 100% is not a fit. 92% leaves about 1.3 GB of
	// a 16 GB card. Step 0's fullest row that stayed wholly on the card was
	// 85% (qwen3:14b at 32k on the RTX 5070 Ti: 14.6 of 17.1 GB) — evidence
	// that 85% fits, none about where splitting starts. Step 6's benchmarks
	// record size_vram against size for every run; the first run that splits
	// below this line, or fits above it, moves it.
	//
	// 80% for headroom is CHOSEN as "the context can still double for a
	// typical model, or a second small model can load".
	HeadroomFraction float64
	FitsFraction     float64

	// MinUsefulContext is the shortest context the advisor will suggest
	// running a model at. CHOSEN: 4096 is Ollama's own default on machines
	// under 23 GiB of graphics memory (ollama/ollama server/routes.go,
	// 6383a0f, 2026-09-18), so it is what a beginner gets without touching a
	// setting; below it a chat forgets its own beginning within a few pages.
	MinUsefulContext int

	// ContextLadder are the contexts the advisor chooses between, ascending.
	// CHOSEN: powers of two from the minimum up to the longest trained
	// context in the catalogue's families.
	ContextLadder []int

	// OSReserve is what the operating system and the user's other programs
	// keep of system memory, by GOOS, when a model runs on the processor (or
	// spills onto it); OSReserveDefault covers any other OS.
	//
	// CHOSEN: an idle Windows 11 or macOS desktop with a browser open on this
	// app holds about 4 GiB; a Linux desktop about 2.5. hardware.Detect reads
	// total memory, not free memory, so this is a standing allowance rather
	// than a reading. Step 6's sampler records system memory during a run and
	// will show whether it is generous or tight.
	OSReserve        map[string]uint64
	OSReserveDefault uint64

	// ---- Speed -------------------------------------------------------------

	// Paths holds, per runtime path, the share of a device's memory bandwidth
	// that generation actually achieves (tok/s × bytes read per token ÷
	// bandwidth) and the ratio of prompt-processing speed to generation
	// speed, each as a range. The width of the range is the honesty of the
	// estimate: tight where one vendor's cards behave alike, wide where they
	// do not. Each entry says what it was measured on.
	Paths map[hardware.RuntimePath]PathSpeed

	// VulkanByVendor refines the vulkan entry: on Vulkan an AMD card, an
	// Intel Arc card and an NVIDIA card are three populations, and one range
	// over all of them would be too wide to be worth showing.
	VulkanByVendor map[hardware.Vendor]PathSpeed

	// NoAVX2Factor scales the cpu efficiency on an x86 processor without
	// AVX2. CHOSEN (0.5): llama.cpp's quantised matrix kernels fall back from
	// AVX2 to SSE/AVX paths roughly half as fast. The fleet's 2013 Mac Pro
	// (Xeon E5 v2, no AVX2) is the machine that will measure it:
	// scripts/calibrate with -ngl 0.
	NoAVX2Factor float64

	// MoEGeneration and MoEPrompt scale a mixture-of-experts model's speed
	// relative to a dense model reading the same number of bytes per token
	// (file bytes × active ÷ total parameters): routing, many small matrix
	// multiplications, and the always-read shared layers cost the rest.
	//
	// MEASURED (public): gpt-oss-20b (12.1 GB, 3.6 of 21 B parameters active)
	// in llama.cpp's gpt-oss guide, discussion #15396, read 2026-09-19,
	// against the same device's dense 7B result: generation lands at 0.53
	// (RTX 5090) to 0.72 (M4 Max) of the dense efficiency — RTX 4090 0.66,
	// RTX 3090 0.56, M1 Pro 0.68, M2 Ultra 0.67; prompt processing at 0.36
	// (RTX 4090) to 1.03 (M1 Pro) of the dense rate per active parameter.
	MoEGeneration Range
	MoEPrompt     Range

	// PromptReferenceBytesPerParam is the size per parameter of the model
	// the prompt ratios were measured on (Llama 2 7B Q4_0: 3.56 GiB over
	// 6.74 B parameters = 0.567 bytes). Prompt processing is bound by
	// arithmetic, not by memory, so it scales with the parameters used per
	// token and hardly with the quant; anchoring the ratio to the reference
	// size keeps a Q8_0 file from looking half as fast at reading a prompt.
	PromptReferenceBytesPerParam float64
}

// Range is a low..high pair; every speed constant is one, because every
// speed estimate is one.
type Range struct {
	Low  float64 `json:"low" source:"n/a"`  // configuration
	High float64 `json:"high" source:"n/a"` // configuration
}

// PathSpeed is one runtime path's speed constants.
type PathSpeed struct {
	Efficiency  Range  // generation: achieved share of memory bandwidth
	PromptRatio Range  // prompt tok/s ÷ generation tok/s, for the reference model
	Basis       string // what the two ranges were measured on
}

const mib = 1 << 20
const gib = 1 << 30

// DefaultConfig returns the constants the advisor ships with.
func DefaultConfig() Config {
	return Config{
		Overhead: map[hardware.RuntimePath]uint64{
			hardware.PathCUDA:   250 * mib,
			hardware.PathMetal:  0,
			hardware.PathVulkan: 50 * mib,
			hardware.PathROCm:   50 * mib,
			hardware.PathCPU:    0,
		},
		KVBytesPerElement: map[KVCacheType]float64{
			KVF16:  2,
			KVQ8_0: 34.0 / 32.0,
			KVQ4_0: 18.0 / 32.0,
		},
		RecurrentBytesPerElement: 4,
		SlidingBatchTokens:       512,
		SlidingPadTokens:         256,

		HeadroomFraction: 0.80,
		FitsFraction:     0.92,
		MinUsefulContext: 4096,
		ContextLadder:    []int{4096, 8192, 16384, 32768, 65536, 131072, 262144},
		OSReserve: map[string]uint64{
			"windows": 4 * gib,
			"darwin":  4 * gib,
			"linux":   5 * gib / 2,
		},
		OSReserveDefault: 3 * gib,

		Paths: map[hardware.RuntimePath]PathSpeed{
			hardware.PathCUDA: {
				Efficiency:  Range{0.60, 0.78},
				PromptRatio: Range{30, 65},
				Basis: "MEASURED (public): llama.cpp CUDA scoreboard, discussion #15013, Llama 2 7B Q4_0 (3.82 GB), read 2026-09-19. " +
					"Generation efficiency RTX 5090 0.62, RTX 3090 0.65, RTX 3090 Ti 0.65, RTX 3080 0.70, RTX 4090 0.71, RTX 5080 0.72, RTX 4080 0.76; " +
					"prompt ÷ generation 33 (RTX 3090) to 64 (RTX 4090).",
			},
			hardware.PathMetal: {
				Efficiency:  Range{0.66, 0.86},
				PromptRatio: Range{7, 14},
				Basis: "MEASURED (public): llama.cpp Apple Silicon table, discussion #4167, LLaMA 7B Q4_0, read 2026-09-19. " +
					"Base and Pro chips M1 to M5: generation efficiency 0.68 (M1 Pro, 14-core) to 0.84 (M2); prompt ÷ generation 7.3 (M1 Pro) to 13 (M2 Ultra). " +
					"Max and Ultra chips sit lower and the M5 generation reads prompts faster; gpus.yaml overrides those rows, each with its own figures.",
			},
			hardware.PathROCm: {
				Efficiency:  Range{0.48, 0.74},
				PromptRatio: Range{18, 30},
				Basis: "MEASURED (public): llama.cpp ROCm/HIP scoreboard, discussion #15021, Llama 2 7B Q4_0, read 2026-09-19. " +
					"Generation efficiency Pro W7900 0.54, RX 7900 XT 0.55, RX 7800 XT 0.62, RX 7900 XTX 0.67, RX 9070 0.68; prompt ÷ generation 21 to 27. " +
					"Five cards, one vendor generation and a half: wider than CUDA's on purpose.",
			},
			hardware.PathVulkan: {
				Efficiency:  Range{0.30, 0.88},
				PromptRatio: Range{5, 58},
				Basis: "MEASURED (public): llama.cpp Vulkan scoreboard, discussion #10879, Llama 2 7B Q4_0, read 2026-09-19 — the whole table, " +
					"used only when the card's vendor is not one of VulkanByVendor's.",
			},
			hardware.PathCPU: {
				Efficiency:  Range{0.35, 0.80},
				PromptRatio: Range{2, 10},
				Basis: "CHOSEN, not yet measured: llama.cpp publishes no processor scoreboard. Community llama-bench runs of 7B Q4_0 on " +
					"dual-channel desktops with AVX2 land between about 0.6 and 0.8 of theoretical memory bandwidth, laptops lower; the range is the " +
					"widest of all paths and gpus.yaml's system_memory rows widen it again, because the advisor cannot see how many memory modules " +
					"are installed. The fleet's Ryzen 7 5800X3D (AVX2) and Xeon E5 v2 (no AVX2) are the calibration runs: scripts/calibrate with -ngl 0.",
			},
		},
		VulkanByVendor: map[hardware.Vendor]PathSpeed{
			hardware.VendorAMD: {
				Efficiency:  Range{0.55, 0.88},
				PromptRatio: Range{6, 38},
				Basis: "MEASURED (public): discussion #10879, AMD cards: generation efficiency RX 580 0.59, RX 7900 XT 0.59, RX 7800 XT 0.72, " +
					"RX 6800 XT 0.75, RX 7900 XTX 0.76, RX 9070 XT 0.82, RX 6700 XT 0.83, RX 6600 0.86; prompt ÷ generation 6.6 (RX 580) to 37 (RX 9070 XT).",
			},
			hardware.VendorIntel: {
				Efficiency:  Range{0.30, 0.62},
				PromptRatio: Range{5, 26},
				Basis: "MEASURED (public): discussion #10879, Intel Arc: generation efficiency A750 0.32, A770 0.36, B580 0.59; " +
					"prompt ÷ generation 5.8 (B580 with flash attention) to 25 (A750). Three cards that disagree: the widest GPU range.",
			},
			hardware.VendorNVIDIA: {
				Efficiency:  Range{0.62, 0.84},
				PromptRatio: Range{24, 58},
				Basis: "MEASURED (public): discussion #10879, NVIDIA cards driven through Vulkan (an old driver): RTX 3090 0.67, RTX 4090 0.71, " +
					"RTX 3060 0.81; prompt ÷ generation 24 to 57.",
			},
		},
		NoAVX2Factor:  0.5,
		MoEGeneration: Range{0.50, 0.75},
		MoEPrompt:     Range{0.35, 1.0},

		PromptReferenceBytesPerParam: 0.567,
	}
}
