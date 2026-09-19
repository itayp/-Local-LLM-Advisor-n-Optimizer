// Package refresh resolves the curated catalogue against Hugging Face and
// stores the result: for every size in families.yaml, the repo's listing
// (file names, sizes, content hashes), then — for each tracked quant and the
// vision encoder — the GGUF header, read with range requests and parsed
// (internal/catalog/hf, internal/catalog/gguf). One row per quant variant
// goes to catalog_files. No weights are downloaded, ever.
//
// It is `advisor catalog refresh` and POST /api/catalog/refresh; step 10's
// nightly watch calls the same Run. It also maps the runtime's installed
// models onto the catalogue (MapInstalled), so a refresh ends with the
// curator's list of installed models the catalogue does not know.
package refresh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"advisor/internal/catalog"
	"advisor/internal/catalog/gguf"
	"advisor/internal/catalog/hf"
	"advisor/internal/store"
)

// Options is what a refresh needs.
type Options struct {
	Catalogue *catalog.Catalogue
	Store     *store.Store
	HF        *hf.Client
	Log       *slog.Logger
	Trigger   string // who asked: "cli", "api", "watch"
	// Only, when not empty, limits the refresh to these family ids.
	Only []string
}

// Report is what a refresh did, for the CLI, the API and catalog_refreshes.
type Report struct {
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Trigger    string `json:"trigger"`

	Sizes    int `json:"sizes" source:"n/a"`    // sizes in the catalogue
	Resolved int `json:"resolved" source:"n/a"` // sizes whose files were read and stored this time
	Files    int `json:"files" source:"n/a"`    // files stored (weights and projectors)

	HeaderReads int   `json:"header_reads" source:"n/a"` // headers read over the network
	CacheHits   int   `json:"cache_hits" source:"n/a"`   // headers the cache already held (same repo, file, content hash)
	Requests    int   `json:"requests" source:"n/a"`     // HTTP requests to Hugging Face
	NotModified int   `json:"not_modified" source:"n/a"` // listings answered 304 (unchanged repo)
	BytesRead   int64 `json:"bytes_read" source:"n/a"`   // bytes downloaded, listings included — no weights

	Failures []SizeFailure `json:"failures"` // sizes that did not resolve
	// Stopped says why the refresh ended before trying every size (Hugging
	// Face unreachable); "" when it tried them all.
	Stopped  string   `json:"stopped,omitempty"`
	Warnings []string `json:"warnings"` // resolved, but something the curator should look at

	Unknown []UnknownModel `json:"unknown_installed"` // installed models the catalogue does not know
}

// SizeFailure is one size that did not resolve, and why, in words.
type SizeFailure struct {
	FamilyID  string `json:"family_id"`
	OllamaTag string `json:"ollama_tag"`
	HFRepo    string `json:"hf_repo"`
	Error     string `json:"error"`
}

// UnknownModel is an installed model the catalogue does not know.
type UnknownModel struct {
	Backend string `json:"backend"`
	Name    string `json:"name"`
	Note    string `json:"note"`
}

// Run refreshes every size (or Options.Only's families) and returns the
// report. A size that fails is recorded and the refresh moves on; Run
// returns an error only when it could not work at all (the store failed,
// the context was cancelled).
func Run(ctx context.Context, o Options) (Report, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	rep := Report{StartedAt: store.Now(), Trigger: o.Trigger, Failures: []SizeFailure{}, Warnings: []string{}, Unknown: []UnknownModel{}}
	before := o.HF.Stats()

	ids, err := o.Store.SyncCatalogModels(ctx, o.Catalogue)
	if err != nil {
		return rep, err
	}
	only := map[string]bool{}
	for _, id := range o.Only {
		only[id] = true
	}
	for _, fam := range o.Catalogue.Families {
		if len(only) == 0 || only[fam.ID] {
			rep.Sizes += len(fam.Sizes)
		}
	}
sizes:
	for _, fam := range o.Catalogue.Families {
		if len(only) > 0 && !only[fam.ID] {
			continue
		}
		for _, size := range fam.Sizes {
			if err := ctx.Err(); err != nil {
				return rep, err
			}
			id := ids[store.CatalogKey{FamilyID: fam.ID, Parameters: size.Parameters}]
			res, err := refreshSize(ctx, o, fam, size)
			rep.HeaderReads += res.headerReads
			rep.CacheHits += res.cacheHits
			rep.Warnings = append(rep.Warnings, res.warnings...)
			if err != nil {
				if ctx.Err() != nil {
					return rep, ctx.Err()
				}
				msg := err.Error()
				o.Log.Warn("catalogue size did not resolve", "family", fam.ID, "tag", size.OllamaTag, "repo", size.HFRepo, "err", msg)
				rep.Failures = append(rep.Failures, SizeFailure{FamilyID: fam.ID, OllamaTag: size.OllamaTag, HFRepo: size.HFRepo, Error: msg})
				if serr := o.Store.RecordCatalogModelError(ctx, id, msg); serr != nil {
					return rep, serr
				}
				if errors.Is(err, hf.ErrUnreachable) {
					// Every other size would fail the same way, slowly.
					rep.Stopped = "Hugging Face could not be reached, so the refresh stopped; what earlier refreshes stored is unchanged"
					o.Log.Warn("catalogue refresh stopped", "reason", rep.Stopped)
					break sizes
				}
				continue
			}
			if err := o.Store.RecordCatalogModelRefresh(ctx, id, res.write); err != nil {
				return rep, err
			}
			rep.Resolved++
			rep.Files += len(res.write.Files)
			o.Log.Info("catalogue size resolved", "family", fam.ID, "tag", size.OllamaTag, "files", len(res.write.Files))
		}
	}

	unknown, err := MapInstalled(ctx, o.Store)
	if err != nil {
		return rep, err
	}
	rep.Unknown = unknown

	after := o.HF.Stats()
	rep.Requests = after.Requests - before.Requests
	rep.NotModified = after.NotModified - before.NotModified
	rep.BytesRead = after.BytesRead - before.BytesRead
	rep.FinishedAt = store.Now()
	body, err := json.Marshal(rep)
	if err != nil {
		return rep, err
	}
	if _, err := o.Store.AddCatalogRefresh(ctx, store.CatalogRefreshRow{
		StartedAt: rep.StartedAt, FinishedAt: rep.FinishedAt, Trigger: rep.Trigger,
		Sizes: rep.Sizes, Resolved: rep.Resolved, Report: body,
	}); err != nil {
		return rep, err
	}
	return rep, nil
}

type sizeResult struct {
	write       store.CatalogModelRefresh
	headerReads int
	cacheHits   int
	warnings    []string
}

// refreshSize resolves one size: listing → tracked files → headers.
func refreshSize(ctx context.Context, o Options, fam catalog.Family, size catalog.Size) (sizeResult, error) {
	var res sizeResult
	repo := size.HFRepo
	where := fmt.Sprintf("%s (%s)", size.OllamaTag, repo)

	etag, cached, _, err := o.Store.CachedListing(ctx, repo)
	if err != nil {
		return res, err
	}
	listing, err := o.HF.ModelInfo(ctx, repo, etag, cached)
	if err != nil {
		return res, describe(err, repo)
	}
	if !listing.NotModified {
		if err := o.Store.PutCachedListing(ctx, repo, listing.ETag, listing.Body); err != nil {
			return res, err
		}
	}
	info := listing.Info
	res.write.HFSHA = info.SHA
	if info.GGUF != nil {
		res.write.ParametersCounted = info.GGUF.Total
	}

	var files []catalog.RepoFile
	for _, s := range info.Siblings {
		f := catalog.RepoFile{Path: s.RFilename, Size: s.Size}
		if s.LFS != nil {
			f.SHA256 = s.LFS.SHA256
			if f.Size == 0 {
				f.Size = s.LFS.Size
			}
		}
		files = append(files, f)
	}
	groups, problems := catalog.GroupGGUF(files)
	for _, p := range problems {
		res.warnings = append(res.warnings, where+": "+p)
	}

	// The weights: one file per tracked quant (the first by path if a repo
	// somehow has two). The projector: one, preferring F16 — what Ollama
	// ships and what llama.cpp's docs use.
	byQuant := map[string]catalog.GGUFFile{}
	var projectors []catalog.GGUFFile
	for _, g := range groups {
		switch g.Role {
		case catalog.RoleModel:
			if o.Catalogue.Tracks(g.Quant) {
				if _, dup := byQuant[g.Quant]; !dup {
					byQuant[g.Quant] = g
				}
			}
		case catalog.RoleProjector:
			projectors = append(projectors, g)
		}
	}
	if len(byQuant) == 0 {
		return res, fmt.Errorf("no GGUF file with a tracked quant (%s) in %s", strings.Join(o.Catalogue.Quants, ", "), repo)
	}
	selected := make([]catalog.GGUFFile, 0, len(byQuant)+1)
	for _, q := range o.Catalogue.Quants {
		if g, ok := byQuant[q]; ok {
			selected = append(selected, g)
		}
	}
	if p, ok := pickProjector(projectors); ok {
		selected = append(selected, p)
	}

	// The Hub counts parameters from one GGUF's tensor shapes; that is the
	// better denominator for bits per weight — unless it plainly counted a
	// different file (a draft model, a vision encoder), in which case the
	// model card's figure stands and the curator hears about it.
	params := size.Parameters
	if c := res.write.ParametersCounted; c > 0 {
		if ratio := float64(c) / float64(size.Parameters); ratio < 0.5 || ratio > 2 {
			res.warnings = append(res.warnings, fmt.Sprintf("%s: Hugging Face counts %d parameters, families.yaml says %d; using families.yaml's",
				where, c, size.Parameters))
			res.write.ParametersCounted = 0
		} else {
			params = c
		}
	}
	hasVision := false
	for _, g := range selected {
		if g.Role == catalog.RoleProjector {
			hasVision = true
		}
	}
	for _, g := range selected {
		ph, fromCache, err := header(ctx, o, repo, info.SHA, g)
		if err != nil {
			return res, fmt.Errorf("%s: %w", g.Path, err)
		}
		if fromCache {
			res.cacheHits++
		} else {
			res.headerReads++
		}
		if len(ph.Missing) > 0 {
			return res, fmt.Errorf("%s: the header does not state %s", g.Path, strings.Join(ph.Missing, ", "))
		}
		f := catalog.File{
			Filename: g.Path, Role: g.Role, Quant: g.Quant, SHA: g.SHA, Parts: len(g.Parts),
			Bytes: g.Bytes, Present: true, Header: ph.Header, FetchedAt: time.Now().UTC(),
		}
		f.Header.HasVision = hasVision
		if g.Role == catalog.RoleModel {
			f.Layout = catalog.NewLayout(f.Header, ph.KV)
			f.BitsPerWeight = float64(g.Bytes) * 8 / float64(params)
			res.warnings = append(res.warnings, checkModelFile(where, g, ph, size)...)
		} else if !ph.Projector {
			res.warnings = append(res.warnings, fmt.Sprintf("%s: %s is named like a vision encoder but its header says %q", where, g.Path, ph.Header.Architecture))
		}
		hj, err := json.Marshal(headerJSON{Type: ph.Type, KV: ph.KV, Skipped: ph.Skipped, KVCount: ph.KVCount, KVRead: ph.KVRead, StoppedAt: ph.StoppedAt})
		if err != nil {
			return res, fmt.Errorf("%s: encoding the header: %w", g.Path, err)
		}
		res.write.Files = append(res.write.Files, store.CatalogFileWrite{File: f, HeaderJSON: hj})
	}
	return res, nil
}

// checkModelFile compares a weights file's header with its name and with
// families.yaml. Disagreements are warnings for the curator, not failures:
// the header is what the estimator uses either way.
func checkModelFile(where string, g catalog.GGUFFile, ph parsedHeader, size catalog.Size) []string {
	var out []string
	h := ph.Header
	if h.FileTypeName != "" && !catalog.QuantMatchesFileType(g.Quant, h.FileTypeName) {
		out = append(out, fmt.Sprintf("%s: %s is named %s but its header says %s", where, g.Path, g.Quant, h.FileTypeName))
	}
	if h.ContextLength > 0 && h.ContextLength != size.ContextLength {
		out = append(out, fmt.Sprintf("%s: families.yaml says context_length %d, %s's header says %d",
			where, size.ContextLength, g.Quant, h.ContextLength))
	}
	if ph.Projector {
		out = append(out, fmt.Sprintf("%s: %s is listed as weights but its header is a vision encoder", where, g.Path))
	}
	return out
}

func pickProjector(ps []catalog.GGUFFile) (catalog.GGUFFile, bool) {
	if len(ps) == 0 {
		return catalog.GGUFFile{}, false
	}
	rank := map[string]int{"F16": 0, "BF16": 1, "Q8_0": 2, "F32": 3}
	sort.SliceStable(ps, func(i, j int) bool {
		ri, ok := rank[ps[i].Quant]
		if !ok {
			ri = 9
		}
		rj, ok := rank[ps[j].Quant]
		if !ok {
			rj = 9
		}
		return ri < rj
	})
	return ps[0], true
}

// parsedHeader is what the refresh keeps of a header, and what the cache
// holds per (repo, file, content hash). The typed fields are computed once
// from the parse; KV is for header_json and the Advanced view.
type parsedHeader struct {
	Header    catalog.GGUFHeader `json:"header"`
	Type      string             `json:"type"`      // general.type
	Projector bool               `json:"projector"` // a vision/audio encoder file
	Missing   []string           `json:"missing,omitempty"`
	KV        map[string]any     `json:"kv"`
	Skipped   []string           `json:"skipped,omitempty"`
	KVCount   uint64             `json:"kv_count"`
	KVRead    uint64             `json:"kv_read"`
	StoppedAt string             `json:"stopped_at,omitempty"`
	BytesRead int64              `json:"bytes_read"`
}

// headerJSON is catalog_files.header_json: every metadata pair the parser
// kept, and how much of the header it read.
type headerJSON struct {
	Type      string         `json:"type,omitempty"`
	KV        map[string]any `json:"kv"`
	Skipped   []string       `json:"skipped,omitempty"`
	KVCount   uint64         `json:"kv_count"`
	KVRead    uint64         `json:"kv_read"`
	StoppedAt string         `json:"stopped_at,omitempty"`
}

// header returns g's parsed header from the cache or, on a miss, from a
// range read at revision (the listing's commit).
func header(ctx context.Context, o Options, repo, revision string, g catalog.GGUFFile) (parsedHeader, bool, error) {
	var ph parsedHeader
	if b, ok, err := o.Store.CachedHeader(ctx, repo, g.Path, g.SHA); err != nil {
		return ph, false, err
	} else if ok {
		if err := json.Unmarshal(b, &ph); err == nil {
			return ph, true, nil
		}
		// An unreadable cache entry is re-read, not trusted.
	}
	h, n, err := o.HF.ReadHeader(ctx, repo, revision, g.Path, g.Parts[0].Size, gguf.Options{StopAtTokenizer: true})
	if err != nil {
		return ph, false, describe(err, repo)
	}
	m := h.Metadata()
	ph = parsedHeader{
		Header: catalog.HeaderFromGGUF(h), Type: m.Type, Projector: m.Projector, Missing: h.Missing(),
		KV: h.KV, Skipped: h.Skipped, KVCount: h.KVCount, KVRead: h.KVRead, StoppedAt: h.StoppedAt, BytesRead: n,
	}
	b, err := json.Marshal(ph)
	if err != nil {
		return ph, false, fmt.Errorf("encoding the header: %w", err)
	}
	if err := o.Store.PutCachedHeader(ctx, repo, g.Path, g.SHA, b, n); err != nil {
		return ph, false, err
	}
	return ph, false, nil
}

// describe turns a client error into the sentence a curator can act on.
func describe(err error, repo string) error {
	switch {
	case errors.Is(err, hf.ErrNotFound):
		return fmt.Errorf("Hugging Face has no repo or file by that name (%s): check hf_repo in families.yaml: %w", repo, err)
	case errors.Is(err, hf.ErrGated):
		return fmt.Errorf("%s needs a Hugging Face login (gated or private); families.yaml needs an ungated GGUF repo: %w", repo, err)
	case errors.Is(err, hf.ErrUnreachable):
		return err
	case errors.Is(err, hf.ErrRateLimited):
		return fmt.Errorf("Hugging Face is rate-limiting requests; try the refresh again later: %w", err)
	case errors.Is(err, gguf.ErrMalformed):
		return fmt.Errorf("the file's header is not valid GGUF: %w", err)
	}
	return err
}

// MapInstalled maps every present installed model onto the stored
// catalogue, stores the result, and returns the ones the catalogue does not
// know. It is cheap (no network) and runs after every refresh and after
// every inventory refresh.
func MapInstalled(ctx context.Context, st *store.Store) ([]UnknownModel, error) {
	rows, err := st.CatalogModels(ctx, false)
	if err != nil {
		return nil, err
	}
	cands := make([]catalog.Candidate, 0, len(rows))
	for _, r := range rows {
		c := catalog.Candidate{ModelID: r.Model.ID, FamilyID: r.Model.FamilyID, Size: r.Model.Size}
		for _, f := range r.Model.Files {
			if f.Role == catalog.RoleModel {
				c.Files = append(c.Files, catalog.FileRef{ID: f.ID, Quant: f.Quant})
			}
		}
		cands = append(cands, c)
	}
	installed, err := st.AllInstalledModels(ctx)
	if err != nil {
		return nil, err
	}
	matches := make([]store.InstalledMatch, 0, len(installed))
	unknown := []UnknownModel{}
	for _, in := range installed {
		m := catalog.MatchInstalled(catalog.Installed{
			Name: in.Name, Family: in.Family, ParameterSize: in.ParameterSize, Quantization: in.Quantization,
		}, cands)
		matches = append(matches, store.InstalledMatch{
			InstalledID: in.ID, ModelID: m.ModelID, FileID: m.FileID, Kind: string(m.Kind), Note: m.Note,
		})
		if m.Kind == catalog.MatchUnknown {
			unknown = append(unknown, UnknownModel{Backend: in.BackendName, Name: in.Name, Note: m.Note})
		}
	}
	if err := st.SetInstalledModelMatches(ctx, matches); err != nil {
		return nil, err
	}
	return unknown, nil
}
