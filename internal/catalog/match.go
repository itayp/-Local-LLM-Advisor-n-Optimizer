package catalog

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Installed is what matching needs to know about a model a runtime has on
// disk (backend.Installed, as the store keeps it).
type Installed struct {
	Name          string // "llama3.1:8b", "qwen3.5", "hf.co/bartowski/Llama-3.2-1B-Instruct-GGUF:Q4_K_M"
	Family        string // the runtime's architecture family: "llama", "qwen35"
	ParameterSize string // as the runtime reports it: "8.0B", "752M"; "" when it does not say
	Quantization  string // "Q4_K_M", "Q8_0", "MXFP4"; "" when it does not say
}

// Candidate is one stored catalogue size with its weight files, as matching
// sees it.
type Candidate struct {
	ModelID  int64
	FamilyID string
	Size     Size
	Files    []FileRef // role "model" only
}

// FileRef is the part of a stored file matching needs.
type FileRef struct {
	ID    int64
	Quant string
}

// MatchKind is how well an installed model maps onto the catalogue.
type MatchKind string

const (
	// MatchFile: the catalogue knows this size and has this quant's file.
	MatchFile MatchKind = "file"
	// MatchModel: the catalogue knows this size but not this quant (it is not
	// tracked, or the catalogue has not been refreshed yet).
	MatchModel MatchKind = "model"
	// MatchUnknown: the catalogue does not know this model. A signal for the
	// curator, not an error (ARCHITECTURE.md D-7).
	MatchUnknown MatchKind = "unknown"
)

// Match is the result of mapping one installed model.
type Match struct {
	Kind    MatchKind
	ModelID int64  // 0 when Kind is unknown
	FileID  int64  // 0 unless Kind is file
	By      string // how it was matched: "tag", "hf_repo", "family+size"; "" when unknown
	Note    string // in words, for the curator
}

// MatchInstalled maps inst to the catalogue — by family, size and quant, as
// build-plan step 4 asks:
//
//  1. a model pulled from Hugging Face ("hf.co/owner/repo:quant") matches the
//     size whose hf_repo it is;
//  2. otherwise the exact Ollama tag ("llama3.1:8b") matches the size that
//     names it;
//  3. otherwise the family is the Ollama library name before the ":" and the
//     size is the one whose parameter count is nearest the runtime's
//     parameter_size (within 25%) — which catches "llama3.2:latest",
//     "qwen3.5:9b-q8_0" and friends;
//
// then the quant picks the file.
func MatchInstalled(inst Installed, cands []Candidate) Match {
	name := NormalizeTag(inst.Name)
	quant := strings.ToUpper(strings.TrimSpace(inst.Quantization))

	// 1. Hugging Face pulls.
	for _, prefix := range []string{"hf.co/", "huggingface.co/"} {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		repo, tag, _ := strings.Cut(rest, ":")
		if q := QuantFromFilename(tag + ".gguf"); q != "" && quant == "" {
			quant = q
		}
		for _, c := range cands {
			if strings.EqualFold(c.Size.HFRepo, repo) {
				return withQuant(c, quant, "hf_repo")
			}
		}
		return Match{Kind: MatchUnknown, Note: fmt.Sprintf(
			"pulled from Hugging Face repo %s, which no catalogue size uses", repo)}
	}

	// 2. The exact tag.
	for _, c := range cands {
		if NormalizeTag(c.Size.OllamaTag) == name {
			return withQuant(c, quant, "tag")
		}
	}

	// 3. Family (the library name) + size.
	base, tag, _ := strings.Cut(name, ":")
	var same []Candidate
	for _, c := range cands {
		if b, _, _ := strings.Cut(NormalizeTag(c.Size.OllamaTag), ":"); b == base {
			same = append(same, c)
		}
	}
	if len(same) == 0 {
		return Match{Kind: MatchUnknown, Note: unknownNote(inst, base)}
	}
	params, known := ParseParameterSize(inst.ParameterSize)
	if !known {
		// The tag often starts with the size: "9b", "9b-q8_0", "e4b".
		first, _, _ := strings.Cut(tag, "-")
		params, known = ParseParameterSize(first)
	}
	if !known {
		if len(same) == 1 {
			return withQuant(same[0], quant, "family+size")
		}
		return Match{Kind: MatchUnknown, Note: fmt.Sprintf(
			"%s is a catalogue family, but the runtime does not say which size %s is", base, inst.Name)}
	}
	best, bestDiff := -1, math.Inf(1)
	for i, c := range same {
		d := math.Abs(float64(params)-float64(c.Size.Parameters)) / float64(c.Size.Parameters)
		if d < bestDiff {
			best, bestDiff = i, d
		}
	}
	if bestDiff > 0.25 {
		return Match{Kind: MatchUnknown, Note: fmt.Sprintf(
			"%s is a catalogue family, but no catalogue size is near %s parameters", base, inst.ParameterSize)}
	}
	return withQuant(same[best], quant, "family+size")
}

func withQuant(c Candidate, quant, by string) Match {
	m := Match{Kind: MatchModel, ModelID: c.ModelID, By: by}
	if len(c.Files) == 0 {
		m.Note = "the catalogue knows this size but has not read its files yet (run a catalogue refresh)"
		return m
	}
	if quant == "" {
		m.Note = "the runtime does not say which quant this is"
		return m
	}
	for _, f := range c.Files {
		if strings.EqualFold(f.Quant, quant) {
			m.Kind, m.FileID = MatchFile, f.ID
			return m
		}
	}
	m.Note = fmt.Sprintf("%s is not one of the quants the catalogue tracks for this size", quant)
	return m
}

func unknownNote(inst Installed, base string) string {
	var bits []string
	if inst.Family != "" {
		bits = append(bits, "architecture "+inst.Family)
	}
	if inst.ParameterSize != "" {
		bits = append(bits, inst.ParameterSize+" parameters")
	}
	if inst.Quantization != "" {
		bits = append(bits, inst.Quantization)
	}
	s := fmt.Sprintf("%s is not in the catalogue", base)
	if len(bits) > 0 {
		s += " (" + strings.Join(bits, ", ") + ")"
	}
	return s + "; add it to data/catalog/families.yaml if it should be recommended"
}

// ParseParameterSize reads a parameter count the way runtimes and model
// names write one: "8.0B", "752M", "1.5b", "70B", "35B-A3B" (the total).
// Gemma's "e4b" is the effective size, not the total, and does not parse.
func ParseParameterSize(s string) (uint64, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if total, _, ok := strings.Cut(s, "-A"); ok {
		s = total
	}
	if s == "" {
		return 0, false
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'K':
		mult = 1e3
	case 'M':
		mult = 1e6
	case 'B':
		mult = 1e9
	case 'T':
		mult = 1e12
	default:
		return 0, false
	}
	v, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return uint64(v * mult), true
}
