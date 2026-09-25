// Package data embeds the repo's data files so the daemon ships them inside
// its one binary. The files are the source of truth and stay reviewable as
// YAML; each is read by the package that owns its schema:
//
//	hardware/runtime-support.yaml   internal/hardware (build-plan step 2)
//	catalog/families.yaml           internal/catalog  (build-plan step 4)
//	catalog/external.yaml           internal/catalog/external (build-plan step 9b)
//	catalog/aliases.yaml            internal/catalog/external (build-plan step 9b)
//	hardware/gpus.yaml              internal/estimate (build-plan step 5)
//	bench/suite.yaml, bench/text.txt internal/bench   (build-plan step 6)
//	recommend/speed-needs.yaml       internal/recommend (backlog item (i), ARCHITECTURE.md D-58)
package data

import "embed"

// Files holds every embedded data file, addressed by its path under data/
// ("hardware/runtime-support.yaml").
//
//go:embed hardware/*.yaml catalog/*.yaml bench/*.yaml bench/*.txt recommend/*.yaml
var Files embed.FS

// RuntimeSupportPath is where the GPU runtime-support table lives in Files.
const RuntimeSupportPath = "hardware/runtime-support.yaml"

// CatalogPath is where the curated model catalogue lives in Files.
const CatalogPath = "catalog/families.yaml"

// ExternalPath is where the approved external benchmark sources and the
// metric → purpose map live in Files.
const ExternalPath = "catalog/external.yaml"

// AliasesPath is where the external sources' names for catalogue sizes live
// in Files.
const AliasesPath = "catalog/aliases.yaml"

// DevicesPath is where the memory-bandwidth table of graphics parts and
// processor memory lives in Files.
const DevicesPath = "hardware/gpus.yaml"

// BenchSuitePath is where the benchmark suite lives in Files; the text its
// prompts are cut from is named inside it, in the same folder.
const BenchSuitePath = "bench/suite.yaml"

// SpeedNeedsPath is where the speed-a-purpose-needs table lives in Files
// (ARCHITECTURE.md D-58): the two bars per purpose, graded from this data
// file, that decide a recommendation's speed factor and its speed reason.
const SpeedNeedsPath = "recommend/speed-needs.yaml"
