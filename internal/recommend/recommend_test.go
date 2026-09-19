package recommend

import (
	"encoding/json"
	"strings"
	"testing"

	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/figure"
	"advisor/internal/hardware"
)

var everyPurpose = catalog.Purposes

func checkCard(t *testing.T, r Recommendation) {
	t.Helper()
	if r.File.ID == 0 || r.File.Role != catalog.RoleModel || r.PullName == "" || r.DisplayName == "" {
		t.Errorf("%s: a card carries the catalogue file and the name to pull: %+v", r.DisplayName, r.File)
	}
	if r.NumCtx < 4096 || r.Estimate.Request.NumCtx != r.NumCtx {
		t.Errorf("%s: context %d, estimate made for %d", r.DisplayName, r.NumCtx, r.Estimate.Request.NumCtx)
	}
	if len(r.Reasons) < 3 {
		t.Errorf("%s: reasons %+v", r.DisplayName, r.Reasons)
	}
	kinds := map[string]bool{}
	for _, reason := range r.Reasons {
		kinds[reason.Kind] = true
		if strings.TrimSpace(reason.Text) == "" || !strings.HasSuffix(reason.Text, ".") {
			t.Errorf("%s: a reason is a sentence: %q", r.DisplayName, reason.Text)
		}
		// The copy rule: none of these terms reaches a customer without its explainer.
		for _, term := range []string{"VRAM", "quantiz", "GGUF", "KV cache", "context window", "tokens/sec", "tok/s", "offload"} {
			if strings.Contains(strings.ToLower(reason.Text), strings.ToLower(term)) {
				t.Errorf("%s: reason uses %q: %q", r.DisplayName, term, reason.Text)
			}
		}
	}
	for _, k := range []string{"fit", "speed", "size"} {
		if !kinds[k] {
			t.Errorf("%s: no %q reason (product rule 3: why, and what it costs): %+v", r.DisplayName, k, r.Reasons)
		}
	}
	switch r.Confidence {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
	default:
		t.Errorf("%s: confidence %q", r.DisplayName, r.Confidence)
	}
	if strings.TrimSpace(r.ConfidenceWhy) == "" {
		t.Errorf("%s: confidence without its why", r.DisplayName)
	}
	if !r.Installed && r.DownloadBytes < r.File.Bytes {
		t.Errorf("%s: download %d for a %d-byte file", r.DisplayName, r.DownloadBytes, r.File.Bytes)
	}
}

func TestAtMostThreeOnePerFamilyBestFirst(t *testing.T) {
	e := mustEngine(t)
	m := goldenMachine(t, "LinuxNVIDIA")
	for _, p := range everyPurpose {
		res := e.Recommend(m, []catalog.Purpose{p}, nil, Preferences{})
		if n := len(res.Recommendations); n == 0 || n > 3 {
			t.Errorf("%s: %d recommendations", p, n)
		}
		seen := map[string]bool{}
		last := 2.0
		for _, r := range res.Recommendations {
			checkCard(t, r)
			if seen[r.FamilyID] {
				t.Errorf("%s: family %s twice", p, r.FamilyID)
			}
			seen[r.FamilyID] = true
			if r.Score > last {
				t.Errorf("%s: not best first", p)
			}
			last = r.Score
			if len(r.Model.Files) != 0 {
				t.Errorf("%s: the card's model should not drag its whole file list along", r.DisplayName)
			}
		}
		if _, err := json.Marshal(res); err != nil {
			t.Errorf("%s: the result must serialise: %v", p, err)
		}
	}
	if got := e.Recommend(m, nil, nil, Preferences{}).Purposes; len(got) != 1 || got[0] != catalog.PurposeChat {
		t.Errorf("no purpose asked for means everyday chat, got %v", got)
	}
}

// "fits your 16 GB graphics card with room for long documents", "about 5 GB
// to download", "the family people use for coding".
func TestReasonsAreWrittenForTheCustomer(t *testing.T) {
	e := mustEngine(t)
	m := goldenMachine(t, "WindowsNVIDIADesktop")
	res := e.Recommend(m, []catalog.Purpose{pLong}, nil, Preferences{})
	if len(res.Recommendations) == 0 {
		t.Fatal("nothing recommended")
	}
	top := res.Recommendations[0]
	text := ""
	for _, r := range top.Reasons {
		text += r.Text + "\n"
	}
	for _, want := range []string{"Fits your 16 GB graphics card with room for long documents.", " to download.", "long documents", "words a second"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s: reasons should contain %q:\n%s", top.DisplayName, want, text)
		}
	}

	coding := e.Recommend(m, []catalog.Purpose{pCode}, nil, Preferences{})
	found := false
	for _, r := range coding.Recommendations {
		for _, reason := range r.Reasons {
			if reason.Kind == "purpose" && strings.Contains(reason.Text, "is a family people use for coding.") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no card says its family is one people use for coding: %+v", coding.Recommendations)
	}

	mac := e.Recommend(goldenMachine(t, "AppleSiliconM1Pro"), []catalog.Purpose{pChat}, nil, Preferences{})
	if got := mac.Recommendations[0].Reasons[0].Text; !strings.Contains(got, "the 11.8 GB of memory your Mac's graphics can use") {
		t.Errorf("an Apple Silicon fit is against the GPU's share, in those words: %q", got)
	}
}

// A CPU-only laptop profile gets small models and a warning, never an empty list.
func TestCPUOnlyLaptopGetsSmallModelsAndAWarning(t *testing.T) {
	e := mustEngine(t)
	laptops := map[string]estimate.Machine{
		"graphics built into the processor, 16 GB": goldenMachine(t, "WindowsIntegratedOnlyFromPowerShell51"),
		"a card without a driver, 16 GB":           goldenMachine(t, "WindowsNVIDIAWithoutDriver"),
	}
	bare := goldenMachine(t, "LinuxIntegratedIntel") // the same laptop with no graphics device at all, and half the memory
	bare.Profile.GPUs, bare.Profile.Tier, bare.Profile.RAMBytes = nil, hardware.TierCPUOnly, 8<<30
	laptops["no graphics device, 8 GB"] = bare

	for name, m := range laptops {
		for _, p := range everyPurpose {
			res := e.Recommend(m, []catalog.Purpose{p}, nil, Preferences{})
			if len(res.Recommendations) == 0 {
				t.Errorf("%s, %s: an empty list (%q) — weak hardware is a tier, not an error", name, p, res.Empty)
				continue
			}
			if !strings.Contains(res.Warning, "processor") || !strings.Contains(res.Warning, "a few words a second") {
				t.Errorf("%s, %s: warning %q", name, p, res.Warning)
			}
			top := res.Recommendations[0]
			if b := effectiveBillions(top.Model.Size, top.File.Header); b > 5 {
				t.Errorf("%s, %s: top pick %s is %.1f B effective parameters — not a small model", name, p, top.DisplayName, b)
			}
			for _, r := range res.Recommendations {
				checkCard(t, r)
				if r.Estimate.BudgetKind != estimate.BudgetSystem || r.Estimate.Memory.GPUResident.Value != 0 {
					t.Errorf("%s, %s: %s was not sized against system memory: %+v", name, p, r.DisplayName, r.Estimate.Memory)
				}
				if r.Confidence == ConfidenceHigh {
					t.Errorf("%s, %s: %s: a processor's speed range is too wide for high confidence", name, p, r.DisplayName)
				}
			}
		}
	}
}

// A 24 GB card is never handed a model that wastes it as the top pick when a
// purpose it asked for has a better fit.
func TestA24GBCardIsNotWasted(t *testing.T) {
	e := mustEngine(t)
	for _, golden := range []string{"LinuxNVIDIA", "WindowsAMDROCm"} {
		m := goldenMachine(t, golden)
		budget := float64(m.Profile.GPUUsableBytes)
		for _, p := range everyPurpose {
			res := e.Recommend(m, []catalog.Purpose{p}, nil, Preferences{})
			if len(res.Recommendations) == 0 {
				t.Fatalf("%s, %s: nothing recommended", golden, p)
			}
			top := res.Recommendations[0]
			// A better fit for the purpose exists whenever some family listing
			// it has a size of 20 B parameters or more that fits the card.
			betterExists := false
			for _, entry := range e.Catalogue {
				for _, q := range entry.Purposes {
					if q == p && entry.Model.Size.Parameters >= 20e9 && float64(entry.Model.Files[0].Bytes) < 0.8*budget {
						betterExists = true
					}
				}
			}
			used := float64(top.Estimate.Memory.Total.Value) / budget
			t.Logf("%-14s %-12s top %-22s uses %3.0f%% of the card (%s)", golden, p, top.DisplayName, 100*used, top.Estimate.Category)
			if betterExists && used < 0.4 {
				t.Errorf("%s, %s: top pick %s uses %.0f%% of a 24 GB card while a larger model for the purpose fits", golden, p, top.DisplayName, 100*used)
			}
			if !top.Estimate.Category.FitsOnDevice() {
				t.Errorf("%s, %s: top pick %s does not fit the card (%s)", golden, p, top.DisplayName, top.Estimate.Category)
			}
		}
	}
}

// An unknown GPU yields fit categories without speed numbers.
func TestUnknownGPUGetsFitsWithoutSpeedNumbers(t *testing.T) {
	e := mustEngine(t)
	m := goldenMachine(t, "LinuxNVIDIA")
	m.Profile.GPUs[0].Name = "NVIDIA GeForce RTX 9999 Hyper"
	res := e.Recommend(m, []catalog.Purpose{pCode}, nil, Preferences{})
	if len(res.Recommendations) == 0 {
		t.Fatalf("an unknown card still gets recommendations: %q", res.Empty)
	}
	for _, r := range res.Recommendations {
		if !r.Estimate.Category.FitsOnDevice() {
			t.Errorf("%s: category %q", r.DisplayName, r.Estimate.Category)
		}
		if r.Speed != nil || r.Estimate.Speed.Known || r.Estimate.Speed.Generation != nil || r.Estimate.Speed.Prompt != nil {
			t.Errorf("%s: a speed number for a card the advisor does not know: %+v", r.DisplayName, r.Estimate.Speed)
		}
		said := false
		for _, reason := range r.Reasons {
			if reason.Kind == "speed" && strings.HasPrefix(reason.Text, "No speed estimate: ") && strings.Contains(reason.Text, "RTX 9999") {
				said = true
			}
		}
		if !said {
			t.Errorf("%s: the card must say there is no speed estimate, and why: %+v", r.DisplayName, r.Reasons)
		}
		if r.Confidence != ConfidenceLow || !strings.Contains(r.ConfidenceWhy, "no speed estimate") {
			t.Errorf("%s: confidence %q (%s), want low", r.DisplayName, r.Confidence, r.ConfidenceWhy)
		}
		b, _ := json.Marshal(r)
		if strings.Contains(string(b), `"speed":{"value"`) {
			t.Errorf("%s: serialised a headline speed: %s", r.DisplayName, b)
		}
	}
	// Without a speed the ranking still prefers the model that uses the card.
	if top := res.Recommendations[0]; top.Model.Size.Parameters < 20e9 {
		t.Errorf("top pick %s", top.DisplayName)
	}
}

// A Radeon profile whose runtime path came back cpu gets CPU-sized models
// with the not-used reason first.
func TestRadeonOnTheProcessorSaysSoFirst(t *testing.T) {
	e := mustEngine(t)
	onCard := goldenMachine(t, "WindowsAMDVulkan") // RX 6700 XT, 12 GB; 32 GB of RAM
	onCPU := onCard
	onCPU.ActualPath = hardware.PathCPU

	for _, p := range everyPurpose {
		with := e.Recommend(onCard, []catalog.Purpose{p}, nil, Preferences{})
		without := e.Recommend(onCPU, []catalog.Purpose{p}, nil, Preferences{})
		if len(without.Recommendations) == 0 {
			t.Fatalf("%s: nothing recommended: %q", p, without.Empty)
		}
		if without.RuntimePath != "cpu" || without.PathSource != estimate.PathEstablished {
			t.Errorf("%s: built for %q (%s)", p, without.RuntimePath, without.PathSource)
		}
		if without.GPUNotUsed == nil || without.GPUNotUsed.Kind != estimate.UnusedEstablished || !strings.Contains(without.Warning, "RX 6700 XT") {
			t.Errorf("%s: warning %q, not used %+v", p, without.Warning, without.GPUNotUsed)
		}
		for _, r := range without.Recommendations {
			checkCard(t, r)
			first := r.Reasons[0]
			if first.Kind != "warning" || first.Text != "Your graphics card is not being used by Ollama; these are the numbers without it." {
				t.Errorf("%s, %s: first reason %+v", p, r.DisplayName, first)
			}
			if first.Explainer != ExplainerGPUNotUsed || !strings.Contains(first.Detail, "driver") || !strings.Contains(first.Detail, "Vulkan") {
				t.Errorf("%s, %s: the reason must link the explainer and carry the why (unsupported card, missing driver, Vulkan off): %+v", p, r.DisplayName, first)
			}
			est := r.Estimate
			if est.Request.RuntimePath != hardware.PathCPU || est.BudgetKind != estimate.BudgetSystem || est.Memory.GPUResident.Value != 0 {
				t.Errorf("%s, %s: not built for the processor: path %q, budget %s, %d on the card", p, r.DisplayName, est.Request.RuntimePath, est.BudgetKind, est.Memory.GPUResident.Value)
			}
			if est.Memory.Overhead.Value != 0 {
				t.Errorf("%s, %s: the processor path has no GPU overhead, got %d", p, r.DisplayName, est.Memory.Overhead.Value)
			}
		}
		// CPU-sized: what the card would have been given is bigger and faster.
		a, b := with.Recommendations[0], without.Recommendations[0]
		if with.GPUNotUsed != nil || a.Reasons[0].Kind == "warning" {
			t.Errorf("%s: the same machine on its card must not carry the warning", p)
		}
		if b.Speed == nil || a.Speed == nil || b.Speed.High >= a.Speed.Low && b.File.Bytes >= a.File.Bytes {
			t.Errorf("%s: on the processor %s (%+v) is neither smaller nor slower than %s on the card (%+v)", p, b.DisplayName, b.Speed, a.DisplayName, a.Speed)
		}
	}
	chat := e.Recommend(onCPU, []catalog.Purpose{pChat}, nil, Preferences{})
	if top := chat.Recommendations[0]; effectiveBillions(top.Model.Size, top.File.Header) > 5 {
		t.Errorf("chat on the processor: top pick %s is not CPU-sized", top.DisplayName)
	}

	t.Run("a card Ollama cannot use at all", func(t *testing.T) {
		res := e.Recommend(goldenMachine(t, "LinuxNVIDIAWithNoDriver"), []catalog.Purpose{pChat}, nil, Preferences{})
		first := res.Recommendations[0].Reasons[0]
		if first.Kind != "warning" || !strings.Contains(first.Text, "cannot be used by Ollama") || !strings.Contains(first.Detail, "driver") {
			t.Errorf("first reason %+v", first)
		}
	})
	t.Run("graphics built into the processor are not a card that is being ignored", func(t *testing.T) {
		res := e.Recommend(goldenMachine(t, "LinuxIntegratedIntel"), []catalog.Purpose{pChat}, nil, Preferences{})
		if res.GPUNotUsed != nil || res.Recommendations[0].Reasons[0].Kind == "warning" {
			t.Errorf("not used %+v, first reason %+v", res.GPUNotUsed, res.Recommendations[0].Reasons[0])
		}
	})
}

// If the user already has a model, every recommendation says what it
// changes versus that model, or is dropped.
func TestEveryRecommendationSaysWhatItChanges(t *testing.T) {
	e := mustEngine(t)
	cat := e.Catalogue
	mac := goldenMachine(t, "AppleSiliconM1Pro")
	mac.ActualPath = hardware.PathMetal
	have := []InstalledModel{installedFromCatalogue(t, cat, "llama3.2:1b"), installedFromCatalogue(t, cat, "llama3.1:8b")}

	res := e.Recommend(mac, []catalog.Purpose{pChat}, have, Preferences{})
	if res.Current == nil || res.Current.Name != "llama3.1:8b" || !res.Current.InCatalogue || res.Current.Estimate == nil {
		t.Fatalf("the model compared with should be the installed one that serves chat best: %+v", res.Current)
	}
	if !strings.Contains(res.Current.Verdict, "runs well") {
		t.Errorf("verdict %q", res.Current.Verdict)
	}
	if len(res.Recommendations) == 0 {
		t.Fatal("nothing recommended")
	}
	for _, r := range res.Recommendations {
		if r.PullName == "llama3.1:8b" {
			t.Errorf("the model the user has changes nothing and must be dropped")
		}
		if !strings.HasPrefix(r.VersusCurrent, "Compared with llama3.1:8b, which you have: ") {
			t.Errorf("%s: versus %q", r.DisplayName, r.VersusCurrent)
		}
		last := r.Reasons[len(r.Reasons)-1]
		if last.Kind != "change" || last.Text != r.VersusCurrent {
			t.Errorf("%s: the change belongs among the reasons: %+v", r.DisplayName, last)
		}
	}

	t.Run("a purpose the current model is not for", func(t *testing.T) {
		res := e.Recommend(mac, []catalog.Purpose{pCode}, have, Preferences{CurrentModel: "llama3.1:8b"})
		if len(res.Recommendations) == 0 || !strings.Contains(res.Recommendations[0].VersusCurrent, "made for coding, which that one is not") {
			t.Errorf("%+v", res.Recommendations)
		}
		if !strings.Contains(res.Current.Verdict, "not a model made for coding") {
			t.Errorf("verdict %q", res.Current.Verdict)
		}
	})
	t.Run("a candidate that changes nothing is dropped", func(t *testing.T) {
		// Ministral 3 8B against Llama 3.1 8B for chat on a card where both
		// fit and both are fast: same size, same speed class, same purposes.
		m := goldenMachine(t, "WindowsNVIDIADesktop")
		without := e.Recommend(m, []catalog.Purpose{pChat}, nil, Preferences{})
		with := e.Recommend(m, []catalog.Purpose{pChat}, []InstalledModel{installedFromCatalogue(t, cat, "ministral-3:14b")}, Preferences{})
		if without.Recommendations[0].PullName != "ministral-3:14b" {
			t.Fatalf("fixture drifted: top pick is %s", without.Recommendations[0].PullName)
		}
		for _, r := range with.Recommendations {
			if r.PullName == "ministral-3:14b" {
				t.Error("the installed model was recommended to its owner")
			}
			if r.VersusCurrent == "" {
				t.Errorf("%s: kept without saying what it changes", r.DisplayName)
			}
		}
	})
	t.Run("already installed means nothing to download", func(t *testing.T) {
		res := e.Recommend(mac, []catalog.Purpose{pChat}, []InstalledModel{installedFromCatalogue(t, cat, "llama3.2:1b"), installedFromCatalogue(t, cat, "qwen3.5:9b")}, Preferences{CurrentModel: "llama3.2:1b"})
		found := false
		for _, r := range res.Recommendations {
			if r.PullName == "qwen3.5:9b" {
				found = true
				if !r.Installed || r.DownloadBytes != 0 {
					t.Errorf("installed %v, download %d", r.Installed, r.DownloadBytes)
				}
			}
		}
		if !found {
			t.Errorf("an installed model that is a real change from the current one stays in the list: %+v", res.Recommendations)
		}
	})
	t.Run("a current model the catalogue does not know", func(t *testing.T) {
		res := e.Recommend(mac, []catalog.Purpose{pChat}, []InstalledModel{{Name: "minicpm-v4.6:latest", SizeBytes: 1_637_848_812, ParameterSize: "752M"}}, Preferences{})
		if res.Current == nil || res.Current.InCatalogue || !strings.Contains(res.Current.Verdict, "does not cover") {
			t.Fatalf("current %+v", res.Current)
		}
		if len(res.Recommendations) == 0 || !strings.Contains(res.Recommendations[0].VersusCurrent, "the size") {
			t.Errorf("all that can be compared is size, and the card should say so: %+v", res.Recommendations)
		}
	})
}

// Confidence is derived from which inputs were measured, estimated or unknown.
func TestConfidenceFollowsTheInputs(t *testing.T) {
	e := mustEngine(t)
	find := func(res Result, tag string) Recommendation {
		t.Helper()
		for _, r := range res.Recommendations {
			if r.PullName == tag {
				return r
			}
		}
		t.Fatalf("%s not among %d recommendations", tag, len(res.Recommendations))
		return Recommendation{}
	}
	m := goldenMachine(t, "LinuxNVIDIA")

	// Expected path, estimated speed: medium, and it says what is missing.
	expected := find(e.Recommend(m, []catalog.Purpose{pCode}, nil, Preferences{}), "devstral-small-2:24b")
	if expected.Confidence != ConfidenceMedium || !strings.Contains(expected.ConfidenceWhy, "expected, not seen") {
		t.Errorf("expected path: %q (%s)", expected.Confidence, expected.ConfidenceWhy)
	}

	// The runtime has been seen using the card; a plain dense model is the
	// shape step 0 measured; cuda's range is tight: high.
	m.ActualPath = hardware.PathCUDA
	seen := e.Recommend(m, []catalog.Purpose{pCode}, nil, Preferences{})
	if got := find(seen, "devstral-small-2:24b"); got.Confidence != ConfidenceHigh {
		t.Errorf("established path, validated memory, cuda: %q (%s)", got.Confidence, got.ConfidenceWhy)
	}
	// A hybrid model with a vision encoder is modelled, not measured: medium.
	if got := find(seen, "qwen3.8:27b"); got.Confidence != ConfidenceMedium || !strings.Contains(got.ConfidenceWhy, "has not been measured") {
		t.Errorf("modelled memory: %q (%s)", got.Confidence, got.ConfidenceWhy)
	}

	// A benchmark of that configuration: measured speed and memory, high.
	qwen := entryByTag(t, e.Catalogue, "qwen3.8:27b")
	e.Measurements = map[MeasurementKey]estimate.Measurement{{qwen.Model.Files[0].ID, 16384}: {GenerationTPS: 36.4, PromptTPS: 1800, PeakBytes: 19_500_000_000}}
	got := find(e.Recommend(m, []catalog.Purpose{pCode}, nil, Preferences{}), "qwen3.8:27b")
	if got.Confidence != ConfidenceHigh || !strings.Contains(got.ConfidenceWhy, "measured on this computer") {
		t.Errorf("measured: %q (%s)", got.Confidence, got.ConfidenceWhy)
	}
	if got.Speed == nil || got.Speed.Source != figure.Measured || got.Speed.IsRange() || got.Speed.Value != 36.4 {
		t.Errorf("the headline speed is the measurement, a point: %+v", got.Speed)
	}
	if text := got.Reasons[2].Text + got.Reasons[3].Text; !strings.Contains(text, "Measured on this computer at about 27 words a second") {
		t.Errorf("the speed reason should say it was measured: %+v", got.Reasons)
	}
	e.Measurements = nil

	// Vulkan on an old AMD card: everything known, but the range is wide.
	macpro := goldenMachine(t, "LinuxMacProTwoD700sOnVulkan")
	macpro.ActualPath = hardware.PathVulkan
	for _, r := range e.Recommend(macpro, []catalog.Purpose{pChat}, nil, Preferences{}).Recommendations {
		if r.Confidence == ConfidenceHigh {
			t.Errorf("%s on Vulkan: %q", r.DisplayName, r.Confidence)
		}
	}
}

func TestPreferences(t *testing.T) {
	e := mustEngine(t)
	small := goldenMachine(t, "LinuxMacProTwoD700sOnVulkan") // 6 GB card, 63 GB of RAM

	// Coding wants Devstral-class models; none fits 6 GB. By default the
	// engine suggests what fits rather than something split across the
	// processor; AllowSplit lets the split ones compete; GPUOnly forbids them.
	def := e.Recommend(small, []catalog.Purpose{pCode}, nil, Preferences{})
	for _, r := range def.Recommendations {
		if r.Estimate.Category == estimate.NeedsCPUOffload {
			t.Errorf("default: %s is split although models that fit exist", r.DisplayName)
		}
	}
	split := e.Recommend(small, []catalog.Purpose{pCode}, nil, Preferences{AllowSplit: true})
	n := 0
	for _, r := range split.Recommendations {
		if r.Estimate.Category == estimate.NeedsCPUOffload {
			n++
			if !strings.Contains(r.Reasons[0].Text, "part of it runs on the processor") {
				t.Errorf("%s: a split model must say so first: %q", r.DisplayName, r.Reasons[0].Text)
			}
		}
	}
	if n == 0 {
		t.Errorf("AllowSplit: no split candidates on a 6 GB card with 63 GB of RAM: %+v", split.Recommendations)
	}

	long := e.Recommend(goldenMachine(t, "LinuxNVIDIA"), []catalog.Purpose{pChat}, nil, Preferences{MinContext: 32768})
	for _, r := range long.Recommendations {
		if r.NumCtx < 32768 {
			t.Errorf("%s: context %d under the minimum asked for", r.DisplayName, r.NumCtx)
		}
	}
}

func TestWhenThereIsNothingToRecommend(t *testing.T) {
	e := mustEngine(t)
	m := goldenMachine(t, "LinuxNVIDIA")

	empty := &Engine{Estimator: e.Estimator, Config: e.Config}
	if res := empty.Recommend(m, []catalog.Purpose{pChat}, nil, Preferences{}); len(res.Recommendations) != 0 || !strings.Contains(res.Empty, "has not been fetched yet") || res.EmptyCode != EmptyNoCatalogue {
		t.Errorf("before the first refresh: %+v", res)
	}
	if res := e.Recommend(goldenMachine(t, "MacOSTooOldForOllama"), []catalog.Purpose{pChat}, nil, Preferences{}); len(res.Recommendations) != 0 || !strings.Contains(res.Empty, "too old") || res.EmptyCode != EmptyBlocked {
		t.Errorf("an OS Ollama does not run on: %+v", res)
	}
	if res := e.Recommend(goldenMachine(t, "AppleSiliconMetalUnreadableIsUnknown"), []catalog.Purpose{pChat}, nil, Preferences{}); len(res.Recommendations) != 0 || !strings.Contains(res.Empty, "could not read") || res.EmptyCode != EmptyBudgetUnknown {
		t.Errorf("an unreadable memory budget: %+v", res)
	}
	tiny := goldenMachine(t, "LinuxServerWithoutGPU")
	tiny.Profile.RAMBytes = 3 << 30
	if res := e.Recommend(tiny, []catalog.Purpose{pChat}, nil, Preferences{}); len(res.Recommendations) != 0 || !strings.Contains(res.Empty, "more memory than this computer") || res.EmptyCode != EmptyNothingFits {
		t.Errorf("3 GB of RAM: %+v", res)
	}
}

// The weights live in one exported config, and they matter.
func TestWeightsAreConfiguration(t *testing.T) {
	e := mustEngine(t)
	m := goldenMachine(t, "LinuxNVIDIA")
	before := e.Recommend(m, []catalog.Purpose{pChat}, nil, Preferences{}).Recommendations[0]

	e.Config.Weights.Size = 0 // size no longer counts: the fastest well-fitting chat model wins
	after := e.Recommend(m, []catalog.Purpose{pChat}, nil, Preferences{}).Recommendations[0]
	if before.PullName == after.PullName || after.Model.Size.Parameters >= before.Model.Size.Parameters {
		t.Errorf("without the size weight the top pick should change to something smaller: %s → %s", before.PullName, after.PullName)
	}
	w := DefaultConfig().Weights
	if w.Purpose <= 0 || w.Fit <= 0 || w.Speed <= 0 || w.Size <= 0 {
		t.Errorf("every factor must count by default: %+v", w)
	}
}

// What the engine says to each machine of Itay's fleet, for him to read
// (build-plan step 5, "Done when"): go test ./internal/recommend -run Fleet -v
func TestFleetTopPicks(t *testing.T) {
	e := mustEngine(t)
	fleet := []struct {
		name, golden string
		actual       hardware.RuntimePath
		purpose      catalog.Purpose
		want         []string // any of these as the top pick
	}{
		{"MacBook Pro M1 Pro 16 GB", "AppleSiliconM1Pro", hardware.PathMetal, pChat, []string{"gemma4:12b", "qwen3.5:9b"}},
		{"MacBook Pro M1 Pro 16 GB", "AppleSiliconM1Pro", hardware.PathMetal, pCode, []string{"qwen3.5:9b"}},
		{"Windows RTX 5070 Ti 16 GB", "WindowsNVIDIADesktop", hardware.PathCUDA, pChat, []string{"ministral-3:14b", "gemma4:12b"}},
		{"Windows RTX 5070 Ti 16 GB", "WindowsNVIDIADesktop", hardware.PathCUDA, pCode, []string{"qwen3.5:9b", "gpt-oss:20b"}},
		{"Mac Pro 2× D700 6 GB (Vulkan)", "LinuxMacProTwoD700sOnVulkan", hardware.PathVulkan, pChat, []string{"gemma4:e4b", "qwen3.5:4b"}},
		{"Mac Pro 2× D700 6 GB (Vulkan)", "LinuxMacProTwoD700sOnVulkan", hardware.PathVulkan, pCode, []string{"qwen3.5:4b"}},
	}
	for _, f := range fleet {
		m := goldenMachine(t, f.golden)
		m.ActualPath = f.actual
		res := e.Recommend(m, []catalog.Purpose{f.purpose}, nil, Preferences{})
		if len(res.Recommendations) == 0 {
			t.Errorf("%s, %s: nothing recommended", f.name, f.purpose)
			continue
		}
		t.Logf("%s — %s", f.name, f.purpose)
		for i, r := range res.Recommendations {
			t.Logf("  %d. %s at %d (%s confidence)", i+1, r.DisplayName, r.NumCtx, r.Confidence)
			for _, reason := range r.Reasons {
				t.Logf("       %s", reason.Text)
			}
		}
		top := res.Recommendations[0].PullName
		ok := false
		for _, w := range f.want {
			ok = ok || w == top
		}
		if !ok {
			t.Errorf("%s, %s: top pick %s, want one of %v", f.name, f.purpose, top, f.want)
		}
	}
}
