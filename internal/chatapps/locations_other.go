//go:build !darwin && !windows && !linux

// This file exists only so the package builds on an operating system the
// advisor is never shipped for (D-2: linux, darwin, windows are the four
// build targets) — the same reason internal/backend/ollama/install_other.go
// exists. Nothing is ever found there.

package chatapps

func pathCommand(ID) string { return "" }

func installLocations(env, ID) []string { return nil }
