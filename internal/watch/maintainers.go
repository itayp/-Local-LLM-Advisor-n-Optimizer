package watch

import (
	"context"
	"sort"
	"strings"
	"time"

	"advisor/internal/catalog"
)

// checkMaintainers looks for new repos from the curated families' own
// maintainers (BUILD_PLAN.md step 10, item 1's second half): for every
// distinct owner behind a curated size's hf_base_repo, the Hub's newest
// repos under that owner, compared against every repo the catalogue already
// names. A repo the catalogue already knows (as a base repo, one of its
// aliases, or the GGUF repo itself) is not new. What is new is flagged for
// the curator and never scored — the recommendation engine only knows sizes
// families.yaml lists (ARCHITECTURE.md D-57); a repo nobody has curated has
// no Entry to run Fit or Recommend against.
//
// A maintainer flagged once stays flagged: like a model, a repo is logged
// as new at most once, ever (existing holds every watch_state row already
// read this run, keyed the same way Run reads it).
func checkMaintainers(ctx context.Context, o Options, existing map[string]State, now time.Time) ([]LogEntry, error) {
	if o.Catalogue == nil || o.HF == nil {
		return nil, nil
	}
	known := knownRepoSet(o.Catalogue)
	limit := o.Config.MaintainerRepoLimit

	var entries []LogEntry
	var firstErr error
	for _, owner := range maintainerOwners(o.Catalogue) {
		listing, err := o.HF.ListByAuthor(ctx, owner, "")
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if o.Log != nil {
				o.Log.Warn("watch: reading a maintainer's repos", "maintainer", owner, "err", err)
			}
			continue
		}
		for i, repo := range listing.Repos {
			if limit > 0 && i >= limit {
				break
			}
			if repo.Private || repo.Disabled || repo.Gated != "" {
				continue
			}
			if known[strings.ToLower(repo.ID)] {
				continue
			}
			key := "repo:" + repo.ID
			if _, seen := existing[key]; seen {
				continue
			}
			entries = append(entries, LogEntry{
				At: now, Key: key, Name: repo.ID, Outcome: OutcomeFlagged,
				Detail: "a new repo from " + owner + ", not yet in the catalogue",
			})
		}
	}
	return entries, firstErr
}

// knownRepoSet is every repo name the catalogue already points at, lower
// cased: a size's original repo, its aliases (hf_base_same_as — a rename or
// a same-weights copy), and the GGUF repo used for metadata. Nothing in
// this set is "new".
func knownRepoSet(cat *catalog.Catalogue) map[string]bool {
	known := map[string]bool{}
	add := func(repo string) {
		if repo = strings.TrimSpace(repo); repo != "" {
			known[strings.ToLower(repo)] = true
		}
	}
	for _, fam := range cat.Families {
		for _, size := range fam.Sizes {
			add(size.HFBaseRepo)
			add(size.HFRepo)
			for _, alt := range size.HFBaseSameAs {
				add(alt)
			}
		}
	}
	return known
}

// maintainerOwners is the distinct Hugging Face owner behind every curated
// size's hf_base_repo — the maker's own namespace, never the GGUF
// quantizer's (families.yaml always points hf_repo at a quantizer such as
// bartowski; hf_base_repo is the maker's own repo, and its owner segment is
// what "the same maintainers" means here). Sorted, so a run's log order is
// deterministic.
func maintainerOwners(cat *catalog.Catalogue) []string {
	seen := map[string]bool{}
	var owners []string
	for _, fam := range cat.Families {
		for _, size := range fam.Sizes {
			owner, _, ok := strings.Cut(size.HFBaseRepo, "/")
			owner = strings.TrimSpace(owner)
			if !ok || owner == "" {
				continue
			}
			key := strings.ToLower(owner)
			if seen[key] {
				continue
			}
			seen[key] = true
			owners = append(owners, owner)
		}
	}
	sort.Strings(owners)
	return owners
}
