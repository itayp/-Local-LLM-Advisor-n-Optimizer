package server

import (
	"advisor/internal/backend"
	"advisor/internal/bench"
	"advisor/internal/catalog"
	"advisor/internal/catalog/external"
	"advisor/internal/catalog/refresh"
	"advisor/internal/chatapps"
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
		BackendsResponse{},
		InstalledModelsResponse{},
		CatalogResponse{},
		CatalogStatus{},
		CatalogProgress{},
		BenchModelsResponse{},
		BenchModel{},
		UnknownInstalledResponse{},
		refresh.Report{},
		catalog.Family{},
		catalog.Model{},
		catalog.File{},
		catalog.PublicEntry{}, // step 9b; catalog.External is the stored row, never served
		external.Report{},
		ModelDetailResponse{},
		PublicBlock{},
		estimate.Estimate{},
		recommend.Result{},
		recommend.Preferences{},
		ModelFitResponse{},
		bench.Request{},
		bench.Run{},
		bench.Sample{},
		bench.Progress{},
		bench.Plan{},
		bench.History{},
		watch.State{},
		watch.Notification{},
		watch.LogEntry{},
		watch.Settings{},
		watch.Report{},
		WatchLogResponse{},
		WatchRunSummary{},
		OnboardingStatus{},
		InstallSizeResponse{},
		InstallStatus{},
		BackendStartResponse{},
		PullRequest{},
		PullStatus{},
		chatapps.App{},
		ChatAppsResponse{},
		SettingsResponse{},
		SettingsUpdate{},
		ModelRemoveRequest{},
	}
}
