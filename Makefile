# Local LLM Advisor & Optimizer — build entry points.
#
#   make dev        daemon (no embedded UI) + Vite dev server with /api proxied
#   make ui         build the UI into internal/server/ui/dist (what the binary embeds)
#   make build      one binary per target in dist/: linux/amd64, darwin/arm64,
#                   darwin/amd64, windows/amd64 — UI embedded, no cgo
#   make test       go test ./... (with the embedded UI) + the UI tests
#   make test-go    go test -tags noui ./... — no Node needed
#   make check      gofmt, go vet, tsc
#   make clean
#
# Needs: Go (the version in go.mod; the toolchain downloads it if older),
# Node 22+ and npm. Everything else is fetched by go and npm.

SHELL := /bin/bash
.DEFAULT_GOAL := help

MODULE   := advisor
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PORT     ?= 27182
LDFLAGS  := -s -w -X $(MODULE)/internal/version.Version=$(VERSION)
GOFLAGS_BUILD := -trimpath
TARGETS  := linux/amd64 darwin/arm64 darwin/amd64 windows/amd64
UI_DIST  := internal/server/ui/dist

.PHONY: help dev ui ui-deps build test test-go test-ui check fmt clean

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
	gofmt -w cmd internal scripts

clean:
	rm -rf dist $(UI_DIST) ui/node_modules/.tmp .dev-data
