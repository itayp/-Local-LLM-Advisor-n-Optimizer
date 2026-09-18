// Package data embeds the repo's data files so the daemon ships them inside
// its one binary. The files are the source of truth and stay reviewable as
// YAML; each is read by the package that owns its schema:
//
//	hardware/runtime-support.yaml   internal/hardware (build-plan step 2)
//
// The curated catalogue (catalog/families.yaml) is added here by the step
// that loads it (step 4).
package data

import "embed"

// Files holds every embedded data file, addressed by its path under data/
// ("hardware/runtime-support.yaml").
//
//go:embed hardware/*.yaml
var Files embed.FS

// RuntimeSupportPath is where the GPU runtime-support table lives in Files.
const RuntimeSupportPath = "hardware/runtime-support.yaml"
