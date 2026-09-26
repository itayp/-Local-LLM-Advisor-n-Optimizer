# Local LLM Advisor & Optimizer — build entry points.
#
#   make dev        daemon (no embedded UI) + Vite dev server with /api proxied
#   make ui         build the UI into internal/server/ui/dist (what the binary embeds)
#   make build      one binary per target in dist/: linux/amd64, darwin/arm64,
#                   darwin/amd64, windows/amd64 — UI embedded, no cgo
#   make test       go test ./... (with the embedded UI) + the UI tests
#   make test-go    go test -tags noui ./... — no Node needed
#   make check      gofmt, go vet, tsc
#   make dmg        macOS only: dist/macos/Local LLM Advisor.dmg
#   make installer  Windows only (needs Inno Setup's iscc on PATH): the
#                   installer .exe, next to dist/
#   make appimage   Linux only (needs appimagetool on PATH): a self-
#                   contained dist/*.AppImage
#   make clean
#
# Needs: Go (the version in go.mod; the toolchain downloads it if older),
# Node 22+ and npm. Everything else is fetched by go and npm. `make dmg`,
# `make installer` and `make appimage` additionally need that OS's own
# packaging tool (Xcode Command Line Tools, Inno Setup, appimagetool —
# each target's own comment below says which) and only ever build their
# own OS's artifact; RELEASING.md is what runs all three from one CI
# release.

SHELL := /bin/bash
.DEFAULT_GOAL := help

MODULE   := advisor
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PORT     ?= 27182
LDFLAGS  := -s -w -X $(MODULE)/internal/version.Version=$(VERSION)
GOFLAGS_BUILD := -trimpath
TARGETS  := linux/amd64 darwin/arm64 darwin/amd64 windows/amd64
UI_DIST  := internal/server/ui/dist

.PHONY: help dev ui ui-deps build test test-go test-ui check fmt clean dmg installer appimage

help:
	@grep -E '^#   make' Makefile | sed 's/^#   //'

# ---------------------------------------------------------------- dev

## Runs the daemon with -tags noui (so the Go side compiles before the UI has
## ever been built) and the Vite dev server, which serves the UI and proxies
## /api to the daemon. Ctrl-C stops both. Vite prints the address to open.
dev: ui-deps
	@echo "daemon on http://127.0.0.1:$(PORT)  •  UI on the Vite address below"
	@trap 'kill 0' INT TERM EXIT; \
	ADVISOR_DATA_DIR=$${ADVISOR_DATA_DIR:-$(CURDIR)/.dev-data} \
	  go run -tags noui ./cmd/advisor -port $(PORT) -no-browser & \
	cd ui && ADVISOR_PORT=$(PORT) npm run dev -- --open; \
	wait

# ---------------------------------------------------------------- ui

ui-deps: ui/node_modules/.package-lock.json

ui/node_modules/.package-lock.json: ui/package.json ui/package-lock.json
	cd ui && npm ci
	@touch $@

## The production UI build. internal/server/ui.go embeds this directory.
ui: ui-deps
	cd ui && npm run build

# ---------------------------------------------------------------- build

## One self-contained binary per target. CGO_ENABLED=0 is what makes the
## cross-compile possible (pure-Go SQLite); -trimpath keeps build paths out.
build: ui
	@mkdir -p dist
	@for t in $(TARGETS); do \
	  os=$${t%/*}; arch=$${t#*/}; ext=""; \
	  if [ "$$os" = windows ]; then ext=.exe; fi; \
	  out=dist/advisor-$$os-$$arch$$ext; \
	  echo "building $$out ($(VERSION))"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(GOFLAGS_BUILD) -ldflags "$(LDFLAGS)" -o $$out ./cmd/advisor || exit 1; \
	done
	@ls -la dist/advisor-*

# ---------------------------------------------------------------- test

## Everything CI runs, in the same order.
test: ui test-go-embedded test-ui

test-go-embedded:
	go test ./...

## The quick loop when the UI has not been built: same tests, placeholder UI.
test-go:
	go test -tags noui ./...

test-ui: ui-deps
	cd ui && npm test

check: ui-deps
	@test -z "$$(gofmt -l . | grep -v '^ui/' | grep -v node_modules)" || { echo "gofmt needed:"; gofmt -l . | grep -v '^ui/'; exit 1; }
	go vet -tags noui ./...
	cd ui && npm run check

fmt:
	gofmt -w cmd internal scripts data

# ---------------------------------------------------------------- packaging
#
# Each target here is a thin wrapper: `make build` first (so the binaries
# it needs already exist under dist/, exactly as CI's own release job
# does it), then one hand-rolled script from packaging/ does the OS-
# specific assembly. None of the three touches the other two OSes'
# artifacts — that split is deliberate (see .goreleaser.yaml's own
# comment on why GoReleaser OSS doesn't build any of them itself).

## macOS only. Needs the Xcode Command Line Tools (iconutil, sips,
## codesign, hdiutil — install with `xcode-select --install`). Ad-hoc
## signs unless the Apple secrets in RELEASING.md's table are set in the
## environment. Output: dist/macos/Local LLM Advisor.dmg
dmg: build
	@mkdir -p dist/macos
	packaging/macos/build_app.sh "$(VERSION)" dist/macos dist/advisor-darwin-arm64 dist/advisor-darwin-amd64
	packaging/macos/build_dmg.sh "dist/macos/Local LLM Advisor.app" "dist/macos/Local LLM Advisor.dmg"

## Windows only. Needs Inno Setup's `iscc` on PATH
## (https://jrsoftware.org/isinfo.php, or `choco install innosetup`).
## Unsigned by default. Pass SIGN=1 and SIGNTOOL="<a full signtool.exe
## command line, ending in $f>" to sign — the exact form CI uses once a
## certificate secret exists is in packaging/windows/setup.iss's header
## comment and RELEASING.md's secrets table.
## Output: dist/LocalLLMAdvisor-Setup-<version>.exe
installer: build
	@command -v iscc >/dev/null 2>&1 || { echo "installer: iscc (Inno Setup) not found on PATH — see packaging/windows/setup.iss"; exit 1; }
ifdef SIGN
	iscc "/DSIGN" "/DMyAppVersion=$(VERSION)" "/DSourceExePath=$(CURDIR)/dist/advisor-windows-amd64.exe" "/Ssigntool=$(SIGNTOOL)" packaging/windows/setup.iss
else
	iscc "/DMyAppVersion=$(VERSION)" "/DSourceExePath=$(CURDIR)/dist/advisor-windows-amd64.exe" packaging/windows/setup.iss
endif

## Linux only. Needs `appimagetool` on PATH
## (https://github.com/AppImage/AppImageKit/releases — RELEASING.md names
## the pinned version) and, to actually run the result afterward, FUSE.
## Output: dist/LocalLLMAdvisor-<version>-x86_64.AppImage
appimage: build
	@command -v appimagetool >/dev/null 2>&1 || { echo "appimage: appimagetool not found on PATH — see packaging/linux/build_appimage.sh"; exit 1; }
	packaging/linux/build_appimage.sh dist/advisor-linux-amd64 "dist/LocalLLMAdvisor-$(VERSION)-x86_64.AppImage"

clean:
	rm -rf dist $(UI_DIST) ui/node_modules/.tmp .dev-data
