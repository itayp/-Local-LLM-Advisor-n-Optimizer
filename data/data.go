// Package data embeds the repo's data files so the daemon ships them inside
// its one binary. The files are the source of truth and stay reviewable as
// YAML; each is read by the package that owns its schema:
//
//	hardware/runtime-support.yaml   internal/hardware (build-plan step 2)
//	catalog/families.yaml           internal/catalog  (build-plan step 4)
//	hardware/gpus.yaml              internal/estimate (build-plan step 5)
package data

import "embed"

// Files holds every embedded data file, addressed by its path under data/
// ("hardware/runtime-support.yaml").
//
//go:embed hardware/*.yaml catalog/*.yaml
var Files embed.FS

// RuntimeSupportPath is where the GPU runtime-support table lives in Files.
const RuntimeSupportPath = "hardware/runtime-support.yaml"

// CatalogPath is where the curated model catalogue lives in Files.
const CatalogPath = "catalog/families.yaml"

// DevicesPath is where the memory-bandwidth table of graphics parts and
// processor memory lives in Files.
const DevicesPath = "hardware/gpus.yaml"
