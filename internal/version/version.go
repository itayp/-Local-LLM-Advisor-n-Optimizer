// Package version carries the build identity of the daemon.
//
// Version is set at link time by the Makefile and CI:
//
//	go build -ldflags "-X advisor/internal/version.Version=v0.1.0"
//
// A binary built without it reports "dev", which is what a developer's own
// build should say — a release is only a release when the build system
// stamped it.
package version

import "runtime"

// Version is the daemon's version string. "dev" unless stamped at link time.
var Version = "dev"

// GoVersion is the Go toolchain the binary was compiled with.
func GoVersion() string { return runtime.Version() }
