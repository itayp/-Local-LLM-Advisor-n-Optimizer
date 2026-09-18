package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The embedded families.yaml is valid, and every size in it names an Ollama
// tag and a Hugging Face repo that look like one (whether they resolve is
// `advisor catalog refresh`'s job — build-plan step 4's gate).
func TestEmbeddedCatalogueIsValid(t *testing.T) {
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Families) == 0 || len(c.Quants) == 0 {
		t.Fatal("the embedded catalogue is empty")
	}
	for _, f := range c.Families {
		for _, s := range f.Sizes {
			if s.Parameters < 500_000_000 || s.Parameters > 80_000_000_000 {
				t.Errorf("%s %s: %d parameters is outside the catalogue's ~1B-70B range", f.ID, s.OllamaTag, s.Parameters)
			}
		}
	}
	// Every purpose is credible for at least one family, or the UI offers a
	// checkbox that can never be satisfied.
	for _, p := range Purposes {
		found := false
		for _, f := range c.Families {
			found = found || slices.Contains(f.Purposes, p)
		}
		if !found {
			t.Errorf("no family is credible for %q", p)
		}
	}
}

// Product rule 8: the docs describe the catalogue; they never count it.
func TestDocsNeverCountTheCatalogue(t *testing.T) {
	counted := regexp.MustCompile(`(?i)\b(\d+|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|twenty)\s+(curated\s+)?(model\s+)?famil(y|ies)\b`)
	for _, doc := range []string{"CLAUDE.md", "ARCHITECTURE.md", "README.md", filepath.Join("data", "catalog", "families.yaml")} {
		b, err := os.ReadFile(filepath.Join("..", "..", doc))
		if err != nil {
			t.Fatal(err)
		}
		// The rule quotes its own counter-example; that is not a count.
		text := strings.ReplaceAll(string(b), `"the 12 families"`, "")
		for _, m := range counted.FindAllString(text, -1) {
			if strings.EqualFold(m, "one family") {
				continue
			}
			t.Errorf("%s counts the catalogue: %q (product rule 8)", doc, m)
		}
	}
}

// The purpose enum is shared with the UI (ui/src/api/types.ts); the two
// lists must be the same values in the same order.
func TestPurposesMatchTheUI(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "ui", "src", "api", "types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "export type Purpose =")
	if start < 0 {
		t.Fatal("ui/src/api/types.ts has no Purpose type")
	}
	decl := src[start:]
	if end := strings.Index(decl, "\n\n"); end > 0 {
		decl = decl[:end]
	}
	var ui []Purpose
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(decl, -1) {
		ui = append(ui, Purpose(m[1]))
	}
	if !slices.Equal(ui, Purposes) {
		t.Errorf("UI purposes %v, Go purposes %v", ui, Purposes)
	}
}

const validYAML = `
quants: [Q4_K_M, Q8_0]
families:
  - id: demo
    display_name: Demo
    maintainer: Someone
    license: {spdx: Apache-2.0}
    purposes: [chat]
    reviewed_at: 2026-09-18
    source: https://example.com/card
    sizes:
      - parameters: 1000000000
        context_length: 8192
        ollama_tag: demo:1b
        hf_repo: owner/demo-GGUF
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if c.Families[0].Sizes[0].OllamaTag != "demo:1b" || !c.Tracks("q4_k_m") || c.Tracks("Q2_K") {
		t.Errorf("parsed %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name, from, to, want string
	}{
		{"unknown key", "maintainer: Someone", "maintainer: Someone\n    maintaner: typo", "unknown field"},
		{"no quants", "quants: [Q4_K_M, Q8_0]", "quants: []", "tracked quants is empty"},
		{"lower-case quant", "quants: [Q4_K_M, Q8_0]", "quants: [q4_k_m]", "not a quant name"},
		{"bad id", "id: demo", "id: Demo Model", "id must be lower case"},
		{"no display name", "display_name: Demo", "display_name: ''", "display_name is missing"},
		{"licence without spdx or name", "license: {spdx: Apache-2.0}", "license: {}", "license needs"},
		{"licence name without url", "license: {spdx: Apache-2.0}", "license: {name: Custom}", "https url"},
		{"unknown purpose", "purposes: [chat]", "purposes: [gaming]", `purpose "gaming"`},
		{"no purposes", "purposes: [chat]", "purposes: []", "purposes is empty"},
		{"bad date", "reviewed_at: 2026-09-18", "reviewed_at: last week", "YYYY-MM-DD"},
		{"no source", "source: https://example.com/card", "source: ''", "source must be"},
		{"no parameters", "parameters: 1000000000", "parameters: 0", "parameters is missing"},
		{"active ≥ total", "parameters: 1000000000", "parameters: 1000000000\n        active_parameters: 2000000000", "active_parameters must be fewer"},
		{"no context", "context_length: 8192", "context_length: 0", "context_length is missing"},
		{"bad tag", "ollama_tag: demo:1b", "ollama_tag: 'Demo 1B'", "not an Ollama library"},
		{"bad repo", "hf_repo: owner/demo-GGUF", "hf_repo: demo-GGUF", "owner/name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			y := strings.Replace(validYAML, tc.from, tc.to, 1)
			if y == validYAML {
				t.Fatalf("test case did not change the YAML")
			}
			_, err := Parse([]byte(y))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	dup := validYAML + strings.Replace(strings.SplitN(validYAML, "families:\n", 2)[1], "hf_repo: owner/demo-GGUF", "hf_repo: owner/other", 1)
	_, err := Parse([]byte(dup))
	var ve *ValidationError
	if !errors.As(err, &ve) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate family: err = %v", err)
	}
	joined := strings.Join(ve.Problems, "\n")
	if !strings.Contains(joined, "id is used twice") || !strings.Contains(joined, "also used by") {
		t.Errorf("duplicate family and tag not both reported:\n%s", joined)
	}
}

func TestNormalizeTag(t *testing.T) {
	for in, want := range map[string]string{
		"llama3.1:8b":                            "llama3.1:8b",
		"glm-4.7-flash":                          "glm-4.7-flash:latest",
		"registry.ollama.ai/library/qwen3:4b":    "qwen3:4b",
		"Qwen3.5":                                "qwen3.5:latest",
		"hf.co/bartowski/Llama-3.2-1B-GGUF:Q8_0": "hf.co/bartowski/llama-3.2-1b-gguf:q8_0",
		"hf.co/bartowski/Llama-3.2-1B-GGUF":      "hf.co/bartowski/llama-3.2-1b-gguf:latest",
	} {
		if got := NormalizeTag(in); got != want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuantFromFilename(t *testing.T) {
	for in, want := range map[string]string{
		"Qwen_Qwen3.5-0.8B-Q4_K_M.gguf":   "Q4_K_M",
		"Qwen_Qwen3.5-0.8B-IQ4_XS.gguf":   "IQ4_XS",
		"google_gemma-4-E2B-it-bf16.gguf": "BF16",
		"gpt-oss-20b-MXFP4.gguf":          "MXFP4",
		"gpt-oss-20b-mxfp4.gguf":          "MXFP4",
		"Qwen3.5-9B-UD-Q4_K_XL.gguf":      "UD-Q4_K_XL",
		"Llama-3.3-70B-Instruct-Q6_K/Llama-3.3-70B-Instruct-Q6_K-00001-of-00002.gguf": "Q6_K",
		"mmproj-Qwen_Qwen3.5-0.8B-f16.gguf":                                           "F16",
		"Qwen_Qwen3.5-0.8B-Q2_K_L.gguf":                                               "Q2_K_L",
		"model.Q8_0.gguf":                                                             "Q8_0",
		"Qwen3.5-9B.gguf":                                                             "",
		"gemma-4-E4B-it.gguf":                                                         "",
	} {
		if got := QuantFromFilename(in); got != want {
			t.Errorf("QuantFromFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGroupGGUF(t *testing.T) {
	files := []RepoFile{
		{Path: "README.md", Size: 10},
		{Path: ".gitattributes", Size: 10},
		{Path: "M-Q4_K_M.gguf", Size: 100, SHA256: "a"},
		{Path: "M-Q8_0/M-Q8_0-00002-of-00002.gguf", Size: 50, SHA256: "c"},
		{Path: "M-Q8_0/M-Q8_0-00001-of-00002.gguf", Size: 150, SHA256: "b"},
		{Path: "M-BF16/M-BF16-00001-of-00003.gguf", Size: 1},
		{Path: "M-BF16/M-BF16-00003-of-00003.gguf", Size: 1},
		{Path: "mmproj-M-f16.gguf", Size: 20, SHA256: "p"},
		{Path: "M-imatrix.gguf", Size: 5},
		{Path: "mtp-M-Q8_0.gguf", Size: 5},
		{Path: "eagle3-M-BF16.gguf", Size: 5},
	}
	got, problems := GroupGGUF(files)
	if len(problems) != 1 || !strings.Contains(problems[0], "2 of 3 parts") {
		t.Errorf("problems = %v", problems)
	}
	type row struct {
		path  string
		parts int
		bytes uint64
		role  FileRole
		quant string
		sha   string
	}
	var rows []row
	for _, g := range got {
		rows = append(rows, row{g.Path, len(g.Parts), g.Bytes, g.Role, g.Quant, g.SHA})
	}
	want := []row{
		{"M-Q4_K_M.gguf", 1, 100, RoleModel, "Q4_K_M", "a"},
		{"M-Q8_0/M-Q8_0-00001-of-00002.gguf", 2, 200, RoleModel, "Q8_0", "b"},
		{"mmproj-M-f16.gguf", 1, 20, RoleProjector, "F16", "p"},
	}
	if !slices.Equal(rows, want) {
		t.Errorf("GroupGGUF:\n got  %+v\n want %+v", rows, want)
	}
}

func TestQuantMatchesFileType(t *testing.T) {
	yes := [][2]string{{"Q4_K_M", "Q4_K_M"}, {"Q4_K_L", "Q4_K_M"}, {"Q3_K_XL", "Q3_K_L"}, {"MXFP4", "MXFP4_MOE"},
		{"UD-Q4_K_XL", "Q4_K_M"}, {"Q2_K_L", "Q2_K"}, {"q8_0", "Q8_0"}}
	no := [][2]string{{"Q4_K_M", "Q8_0"}, {"Q8_0", "Q4_0"}, {"IQ4_XS", "Q4_K_S"}, {"MXFP4", "Q4_K_M"}}
	for _, p := range yes {
		if !QuantMatchesFileType(p[0], p[1]) {
			t.Errorf("%s should agree with %s", p[0], p[1])
		}
	}
	for _, p := range no {
		if QuantMatchesFileType(p[0], p[1]) {
			t.Errorf("%s should not agree with %s", p[0], p[1])
		}
	}
}

func TestParseParameterSize(t *testing.T) {
	for in, want := range map[string]uint64{"8.0B": 8e9, "752M": 752e6, "1.5b": 1.5e9, "70B": 70e9, "35B-A3B": 35e9, "0.8b": 8e8} {
		if got, ok := ParseParameterSize(in); !ok || got != want {
			t.Errorf("ParseParameterSize(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "e4b", "latest", "B", "-1B", "8"} {
		if _, ok := ParseParameterSize(in); ok {
			t.Errorf("ParseParameterSize(%q) parsed", in)
		}
	}
}

func TestMatchInstalled(t *testing.T) {
	cands := []Candidate{
		{ModelID: 1, FamilyID: "llama3.2", Size: Size{Parameters: 1_230_000_000, OllamaTag: "llama3.2:1b", HFRepo: "bartowski/Llama-3.2-1B-Instruct-GGUF"},
			Files: []FileRef{{ID: 11, Quant: "Q4_K_M"}, {ID: 12, Quant: "Q8_0"}}},
		{ModelID: 2, FamilyID: "llama3.2", Size: Size{Parameters: 3_210_000_000, OllamaTag: "llama3.2:3b", HFRepo: "bartowski/Llama-3.2-3B-Instruct-GGUF"},
			Files: []FileRef{{ID: 21, Quant: "Q4_K_M"}}},
		{ModelID: 3, FamilyID: "gpt-oss", Size: Size{Parameters: 21_000_000_000, OllamaTag: "gpt-oss:20b", HFRepo: "ggml-org/gpt-oss-20b-GGUF"},
			Files: []FileRef{{ID: 31, Quant: "MXFP4"}}},
		{ModelID: 4, FamilyID: "glm-4.7-flash", Size: Size{Parameters: 31_000_000_000, OllamaTag: "glm-4.7-flash", HFRepo: "o/glm"}},
	}
	cases := []struct {
		in     Installed
		kind   MatchKind
		model  int64
		file   int64
		by     string
		noteIn string
	}{
		{Installed{Name: "llama3.2:1b", Family: "llama", ParameterSize: "1.2B", Quantization: "Q8_0"}, MatchFile, 1, 12, "tag", ""},
		{Installed{Name: "llama3.2:latest", Family: "llama", ParameterSize: "3.2B", Quantization: "Q4_K_M"}, MatchFile, 2, 21, "family+size", ""},
		{Installed{Name: "llama3.2:3b-instruct-q2_K", Family: "llama", ParameterSize: "3.2B", Quantization: "Q2_K"}, MatchModel, 2, 0, "family+size", "Q2_K is not one of the quants"},
		{Installed{Name: "llama3.2:1b-instruct-q4_K_M", Quantization: "Q4_K_M"}, MatchFile, 1, 11, "family+size", ""},
		{Installed{Name: "hf.co/bartowski/Llama-3.2-1B-Instruct-GGUF:Q4_K_M", ParameterSize: "1.24B"}, MatchFile, 1, 11, "hf_repo", ""},
		{Installed{Name: "gpt-oss:20b", ParameterSize: "20.9B", Quantization: "MXFP4"}, MatchFile, 3, 31, "tag", ""},
		{Installed{Name: "glm-4.7-flash:latest", Quantization: "Q4_K_M"}, MatchModel, 4, 0, "tag", "has not read its files"},
		{Installed{Name: "qwen3:4b", Family: "qwen3", ParameterSize: "4.0B", Quantization: "Q4_K_M"}, MatchUnknown, 0, 0, "", "qwen3 is not in the catalogue (architecture qwen3, 4.0B parameters, Q4_K_M)"},
		{Installed{Name: "llama3.2:90b", ParameterSize: "88.6B"}, MatchUnknown, 0, 0, "", "no catalogue size is near"},
		{Installed{Name: "llama3.2:custom"}, MatchUnknown, 0, 0, "", "does not say which size"},
		{Installed{Name: "hf.co/someone/else-GGUF:Q4_K_M"}, MatchUnknown, 0, 0, "", "which no catalogue size uses"},
	}
	for _, tc := range cases {
		t.Run(tc.in.Name, func(t *testing.T) {
			m := MatchInstalled(tc.in, cands)
			if m.Kind != tc.kind || m.ModelID != tc.model || m.FileID != tc.file || m.By != tc.by {
				t.Errorf("match = %+v; want kind %s model %d file %d by %q", m, tc.kind, tc.model, tc.file, tc.by)
			}
			if tc.noteIn != "" && !strings.Contains(m.Note, tc.noteIn) {
				t.Errorf("note %q, want it to say %q", m.Note, tc.noteIn)
			}
		})
	}
}
