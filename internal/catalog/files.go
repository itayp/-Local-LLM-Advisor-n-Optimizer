package catalog

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RepoFile is one file in a Hugging Face repo listing.
type RepoFile struct {
	Path   string // "Qwen_Qwen3.5-9B-Q4_K_M.gguf", or "Q8_0/Model-Q8_0-00001-of-00002.gguf"
	Size   uint64
	SHA256 string // the LFS sha256 of the content; "" when the Hub gave none
}

// GGUFFile is one logical GGUF file in a repo: a single file, or every part
// of a split one ("-00001-of-00003.gguf").
type GGUFFile struct {
	Path  string     // part 1
	Parts []RepoFile // in part order; len 1 for an unsplit file
	Bytes uint64     // summed across parts
	Role  FileRole
	Quant string // upper case, as the name spells it: "Q4_K_M", "UD-Q4_K_XL", "BF16", "MXFP4"
	SHA   string // part 1's LFS sha256 — the header lives in part 1
}

var splitPattern = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})\.gguf$`)

// quantToken matches the quant part of a llama.cpp-style file name, upper
// cased: Q4_K_M, IQ4_XS, Q8_0, Q2_K_L, Q3_K_XL, TQ1_0, BF16, F16, F32,
// MXFP4 (and MXFP4_MOE).
var quantToken = regexp.MustCompile(`^(I?Q[1-8](_[0-9A-Z]+)*|TQ[12]_0|BF16|F16|FP16|F32|FP32|MXFP4(_MOE)?)$`)

// QuantFromFilename returns the quant a GGUF file's name carries, upper
// cased, or "" when the name carries none. Unsloth's dynamic quants keep
// their prefix ("UD-Q4_K_XL"), because they are not the same file as a plain
// Q4_K_XL.
func QuantFromFilename(name string) string {
	base := path.Base(name)
	if m := splitPattern.FindStringSubmatch(base); m != nil {
		base = m[1]
	} else {
		base = strings.TrimSuffix(base, ".gguf")
		base = strings.TrimSuffix(base, ".GGUF")
	}
	tokens := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '.' })
	for i := len(tokens) - 1; i >= 0; i-- {
		t := strings.ToUpper(tokens[i])
		if quantToken.MatchString(t) {
			if i > 0 && strings.EqualFold(tokens[i-1], "UD") {
				return "UD-" + t
			}
			return t
		}
	}
	return ""
}

// roleOf classifies a GGUF file by its name: "" means the file is neither
// weights nor a vision encoder (an importance matrix, a multi-token
// prediction head, a speculative-decoding draft model) and is ignored.
func roleOf(name string) FileRole {
	base := strings.ToLower(path.Base(name))
	switch {
	case strings.HasPrefix(base, "mmproj"):
		return RoleProjector
	case strings.Contains(base, "imatrix"),
		strings.HasPrefix(base, "mtp-"),
		strings.HasPrefix(base, "eagle"),
		strings.HasPrefix(base, "draft-"):
		return ""
	}
	return RoleModel
}

// GroupGGUF turns a repo listing into its logical GGUF files. A split file
// whose parts are not all listed is reported and left out — its size would
// be wrong. Files that are not .gguf, and GGUF files that are neither
// weights nor a projector, are skipped silently.
func GroupGGUF(files []RepoFile) (out []GGUFFile, problems []string) {
	type split struct {
		of    int
		parts map[int]RepoFile
	}
	splits := map[string]*split{} // key: directory + base name without the part suffix
	for _, f := range files {
		if !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			continue
		}
		role := roleOf(f.Path)
		if role == "" {
			continue
		}
		dir, base := path.Split(f.Path)
		if m := splitPattern.FindStringSubmatch(base); m != nil {
			part, _ := strconv.Atoi(m[2])
			of, _ := strconv.Atoi(m[3])
			key := dir + m[1]
			s := splits[key]
			if s == nil {
				s = &split{of: of, parts: map[int]RepoFile{}}
				splits[key] = s
			}
			if s.of != of || part < 1 || part > of {
				problems = append(problems, fmt.Sprintf("%s: part numbering disagrees with its siblings", f.Path))
				continue
			}
			s.parts[part] = f
			continue
		}
		out = append(out, GGUFFile{
			Path: f.Path, Parts: []RepoFile{f}, Bytes: f.Size, Role: role,
			Quant: QuantFromFilename(f.Path), SHA: f.SHA256,
		})
	}
	keys := make([]string, 0, len(splits))
	for k := range splits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := splits[k]
		if len(s.parts) != s.of {
			problems = append(problems, fmt.Sprintf("%s: %d of %d parts are listed; left out", k, len(s.parts), s.of))
			continue
		}
		g := GGUFFile{Role: roleOf(s.parts[1].Path), Quant: QuantFromFilename(s.parts[1].Path)}
		for i := 1; i <= s.of; i++ {
			g.Parts = append(g.Parts, s.parts[i])
			g.Bytes += s.parts[i].Size
		}
		g.Path, g.SHA = g.Parts[0].Path, g.Parts[0].SHA256
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, problems
}

// QuantMatchesFileType reports whether a file name's quant label agrees
// with the general.file_type its header states. bartowski's "_L"/"_XL"
// variants keep the embedding and output tensors at Q8_0 but are still
// written with the base type ("Q4_K_L" states Q4_K_M, "Q3_K_XL" Q3_K_L), and
// an MXFP4 file states MXFP4_MOE.
func QuantMatchesFileType(label, fileType string) bool {
	label = strings.TrimPrefix(strings.ToUpper(label), "UD-")
	fileType = strings.ToUpper(fileType)
	if label == fileType {
		return true
	}
	switch label {
	case "MXFP4":
		return fileType == "MXFP4_MOE"
	case "FP16":
		return fileType == "F16"
	case "FP32":
		return fileType == "F32"
	}
	// Q4_K_L → Q4_K_*, Q3_K_XL → Q3_K_*, Q2_K_L → Q2_K*: same block type.
	if strings.HasSuffix(label, "_L") || strings.HasSuffix(label, "_XL") {
		stem := label[:strings.LastIndex(label, "_")]
		return strings.HasPrefix(fileType, stem)
	}
	return false
}
