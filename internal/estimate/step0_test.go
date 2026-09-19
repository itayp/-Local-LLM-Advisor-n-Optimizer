package estimate

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"advisor/internal/catalog"
	"advisor/internal/catalog/gguf"
	"advisor/internal/hardware"
)

// The step 0 gate, replayed (build-plan step 5, item 5: "reproduce every
// step 0 row within the gate").
//
// scripts/probe0/results/ holds what three machines actually used for every
// (model, context) row. The reports were written by probe0 build .4, before
// the three corrections of round 3, so their own predicted_bytes are the
// OLD formula's; what is replayed here is each row's inputs (the header
// fields, the blob size, the path Ollama took) through Fit, scored against
// the row's measured bytes — the same thing `probe0 -refit -recompute` does.
//
// The gate: predicted within 15% of measured for at least four of every
// five dense rows on each machine. The recorded result (ARCHITECTURE.md
// D-20): 27 of 28, worst row llama3.2:3b at 4096 on the M1 Pro at −20.8%.

type probeReport struct {
	MachineLabel string     `json:"machine_label"`
	Rows         []probeRow `json:"rows"`
}

type probeRow struct {
	Model              string   `json:"model"`
	Arch               string   `json:"arch"`
	Class              string   `json:"class"`
	Excluded           bool     `json:"excluded"`
	Skipped            bool     `json:"skipped"`
	NumCtx             int      `json:"num_ctx"`
	BlockCount         int      `json:"block_count"`
	HeadCount          int      `json:"head_count"`
	HeadCountKV        int      `json:"head_count_kv"`
	EmbeddingLength    int      `json:"embedding_length"`
	KeyLength          int      `json:"key_length"`
	ModelContextLength int      `json:"model_context_length"`
	WeightsBytes       uint64   `json:"weights_bytes"`
	MeasuredBytes      uint64   `json:"measured_bytes"`
	RuntimePath        string   `json:"runtime_path"`
	Notes              []string `json:"notes"`
}

var keyLengthNote = regexp.MustCompile(`attention\.key_length = (\d+)`)

// keyLength is the row's attention.key_length: a field in later probe0
// builds, a note in the build these reports came from.
func (r probeRow) keyLength() int {
	if r.KeyLength > 0 {
		return r.KeyLength
	}
	for _, n := range r.Notes {
		if m := keyLengthNote.FindStringSubmatch(n); m != nil {
			v, _ := strconv.Atoi(m[1])
			return v
		}
	}
	return 0
}

func loadProbeReports(t *testing.T) []probeReport {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "scripts", "probe0", "results", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no step 0 reports found: %v", err)
	}
	var out []probeReport
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var r probeReport
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(r.Rows) > 0 {
			out = append(out, r)
		}
	}
	return out
}

// roomyMachine is a machine on the given path with more memory than any row
// needs: the replay is about the predicted total, not the category.
func roomyMachine(path hardware.RuntimePath) Machine {
	p := hardware.Profile{
		OS: "linux", Arch: "amd64", RAMBytes: 256 * gib, RAMKnown: true,
		GPUs: []hardware.GPU{{
			Vendor: hardware.VendorNVIDIA, Name: "test device", VRAMBytes: 128 * gib, VRAMKnown: true,
			IntegratedKnown: true, ExpectedBackend: path,
		}},
		GPUUsableBytes: 128 * gib, GPUUsableKnown: true,
	}
	return Machine{Profile: p, ActualPath: path}
}

func (r probeRow) model() Model {
	return Model{File: catalog.File{
		Bytes: r.WeightsBytes,
		Role:  catalog.RoleModel,
		Header: catalog.GGUFHeader{
			Architecture: r.Arch, BlockCount: r.BlockCount, HeadCount: r.HeadCount, HeadCountKV: r.HeadCountKV,
			HeadCountKVStated: true, KeyLength: r.keyLength(), EmbeddingLength: r.EmbeddingLength,
			ContextLength: r.ModelContextLength,
		},
	}}
}

func TestStep0RowsWithinTheGate(t *testing.T) {
	e := mustEstimator(t)
	const gate = 15.0

	type worst struct {
		label string
		err   float64
	}
	var w worst
	total, within := 0, 0
	for _, rep := range loadProbeReports(t) {
		n, ok := 0, 0
		for _, row := range rep.Rows {
			if row.Excluded || row.Skipped || row.MeasuredBytes == 0 {
				continue
			}
			est := e.Fit(roomyMachine(hardware.RuntimePath(row.RuntimePath)), row.model(), Request{NumCtx: row.NumCtx, KVCacheType: KVF16})
			if est.Basis.MemoryModel != MemoryValidated {
				t.Errorf("%s %s: a step 0 dense row must be on validated ground, got %q", rep.MachineLabel, row.Model, est.Basis.MemoryModel)
			}
			errPct := 100 * (float64(est.Memory.Total.Value) - float64(row.MeasuredBytes)) / float64(row.MeasuredBytes)
			n++
			if math.Abs(errPct) <= gate {
				ok++
			}
			if math.Abs(errPct) > math.Abs(w.err) {
				w = worst{fmt.Sprintf("%s %s @ %d", rep.MachineLabel, row.Model, row.NumCtx), errPct}
			}
			t.Logf("%-16s %-14s %6d  predicted %5.2f GB  measured %5.2f GB  %+6.1f%%", rep.MachineLabel, row.Model, row.NumCtx,
				float64(est.Memory.Total.Value)/gib, float64(row.MeasuredBytes)/gib, errPct)
		}
		if n == 0 {
			t.Errorf("%s: no dense rows", rep.MachineLabel)
			continue
		}
		if float64(ok) < 0.8*float64(n) {
			t.Errorf("%s: %d of %d dense rows within %.0f%% — the step 0 gate needs four of every five", rep.MachineLabel, ok, n, gate)
		}
		total, within = total+n, within+ok
	}

	// The recorded result, pinned: a change to the formula that moves it is a
	// change to ARCHITECTURE.md D-20, not a refactor.
	if total != 28 || within != 27 {
		t.Errorf("pooled: %d of %d rows within the gate; step 0 recorded 27 of 28", within, total)
	}
	if w.label != "macOS llama3.2:3b @ 4096" || math.Abs(w.err-(-20.8)) > 0.1 {
		t.Errorf("worst row is %s at %+.1f%%; step 0 recorded llama3.2:3b @ 4096 on the M1 Pro at −20.8%%", w.label, w.err)
	}
}

// Step 0 also measured one hybrid model — minicpm-v4.6, a qwen35 (Gated
// DeltaNet, one attention layer in four) — on Metal and on Vulkan, and kept
// it out of its gate as a vision model. Its rows are the only fleet
// measurement of a non-uniform layout, so they are held to the same 15%
// here: with every layer counted the 32k rows over-predict by more than 40%.
func TestStep0HybridRowsSupportTheLayout(t *testing.T) {
	e := mustEstimator(t)

	f, err := os.Open(filepath.Join("..", "catalog", "gguf", "testdata", "minicpm-v4.6.gguf.head.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := gguf.Parse(z, gguf.Options{StopAtTokenizer: true})
	if err != nil {
		t.Fatal(err)
	}
	header := catalog.HeaderFromGGUF(hdr)
	layout := catalog.NewLayout(header, hdr.KV)
	if layout.Basis != catalog.LayoutStated || layout.CacheLayers() != 6 || layout.RecurrentLayers != 18 {
		t.Fatalf("layout of the real qwen35 header: basis %q, %d cache layers, %d recurrent; want stated, 6, 18\n%+v",
			layout.Basis, layout.CacheLayers(), layout.RecurrentLayers, layout)
	}

	rows := 0
	for _, rep := range loadProbeReports(t) {
		for _, row := range rep.Rows {
			if row.Model != "minicpm-v4.6:latest" || row.MeasuredBytes == 0 {
				continue
			}
			rows++
			// The blob Ollama reports already contains the vision encoder.
			m := Model{File: catalog.File{Bytes: row.WeightsBytes, Role: catalog.RoleModel, Header: header, Layout: layout}}
			est := e.Fit(roomyMachine(hardware.RuntimePath(row.RuntimePath)), m, Request{NumCtx: row.NumCtx})
			errPct := 100 * (float64(est.Memory.Total.Value) - float64(row.MeasuredBytes)) / float64(row.MeasuredBytes)
			t.Logf("%-10s %6d  predicted %5.2f GB  measured %5.2f GB  %+6.1f%%", rep.MachineLabel, row.NumCtx,
				float64(est.Memory.Total.Value)/gib, float64(row.MeasuredBytes)/gib, errPct)
			if math.Abs(errPct) > 15 {
				t.Errorf("%s @ %d: %+.1f%%, outside 15%%", rep.MachineLabel, row.NumCtx, errPct)
			}
			if est.Basis.MemoryModel != MemoryModelled {
				t.Errorf("a hybrid layout is modelled, not validated; got %q", est.Basis.MemoryModel)
			}
		}
	}
	if rows != 4 {
		t.Errorf("expected step 0's four minicpm-v4.6 rows (Metal and Vulkan, two contexts), found %d", rows)
	}
}

// For the shape step 0 measured, the generalised cache arithmetic must be
// D-20's formula to the byte.
func TestUniformCacheIsTheStep0Formula(t *testing.T) {
	e := mustEstimator(t)
	for _, c := range []struct{ layers, kvHeads, headDim, ctx int }{
		{32, 8, 128, 4096}, {36, 8, 128, 32768}, {16, 8, 64, 131072}, {40, 10, 128, 16384},
	} {
		h := catalog.GGUFHeader{Architecture: "llama", BlockCount: c.layers, HeadCount: 32, HeadCountKV: c.kvHeads, KeyLength: c.headDim, ContextLength: 1 << 20}
		got := e.kvBytes(catalog.NewLayout(h, nil), c.ctx, KVF16)
		want := uint64(2 * c.layers * c.kvHeads * c.headDim * c.ctx * 2)
		if got != want {
			t.Errorf("%+v: cache %d bytes, D-20's formula gives %d", c, got, want)
		}
	}
}

func mustEstimator(t *testing.T) *Estimator {
	t.Helper()
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return e
}
