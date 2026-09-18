//go:build noui

package server

import (
	"net/http"
	"testing/fstest"
)

// Built with -tags noui: the UI is not embedded. `make dev` uses this so
// the Go side compiles before the UI has been built; Vite serves the UI on
// its own port and proxies /api to the daemon.
func uiHandler() http.Handler {
	return spaHandler(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<!doctype html>
<meta charset="utf-8">
<title>Advisor (no UI embedded)</title>
<p>This daemon was built with <code>-tags noui</code>, so the UI is not embedded.
<p>For development, open the Vite dev server (<code>make dev</code> prints its address).
For a real build, run <code>make build</code>.
`)},
	})
}
