package server

import (
	"advisor/internal/backend"
	"advisor/internal/bench"
	"advisor/internal/catalog"
	"advisor/internal/estimate"
	"advisor/internal/hardware"
	"advisor/internal/recommend"
	"advisor/internal/watch"
)

// APITypes lists every type the API serializes to the UI — the ones it
// serves today and the ones the types-only packages promise it will serve.
// apitypes_test.go runs figure.Check over this list, so a numeric field
// that is neither a figure nor tagged `source:"n/a"` fails the build. When
// a step adds an API type, it adds it here; the reviewer looks for the
// addition.
func APITypes() []any {
	return []any{
		Health{},
		APIError{},
		HardwareResponse{},
		HardwareHistory{},
		hardware.Profile{},
		backend.Status{},
		catalog.Family{},
		catalog.Model{},
		catalog.File{},
		catalog.External{},
		estimate.Estimate{},
		recommend.Result{},
		bench.Request{},
		bench.Run{},
		bench.Sample{},
		watch.State{},
		watch.Notification{},
		watch.LogEntry{},
		watch.Settings{},
	}
}
