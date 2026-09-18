//go:build !noui

package server

import (
	"embed"
	"io/fs"
	"net/http"
)

// The Vite build lands in internal/server/ui/dist (ui/vite.config.ts sets
// the outDir). `make ui` produces it; `make build` and CI embed it. A build
// with `-tags noui` (what `make dev` uses, where Vite serves the UI itself)
// compiles without it — see ui_noui.go.
//
//go:embed all:ui/dist
var uiFiles embed.FS

func uiFS() fs.FS {
	sub, err := fs.Sub(uiFiles, "ui/dist")
	if err != nil {
		panic(err) // the directory is embedded at compile time; this cannot fail
	}
	return sub
}

func uiHandler() http.Handler { return spaHandler(uiFS()) }
