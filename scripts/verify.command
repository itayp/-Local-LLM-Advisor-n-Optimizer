#!/bin/bash
# The CI sequence, run on the Mac: double-click this file in Finder, or run
# `bash scripts/verify.command`. go mod tidy, make test, make build, then a
# smoke test of the built binary, which also prints this Mac's hardware
# profile (GET /api/hardware), then resolves the curated model catalogue
# against Hugging Face (`advisor catalog refresh`, into a throwaway
# folder). Everything it prints also goes to
# verify.log in the repo root so the result can be read back later.
set -o pipefail
cd "$(dirname "$0")/.." || exit 1
export PATH="/usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:$HOME/go/bin:$PATH"
LOG="verify.log"
{
  echo "== $(date) in $(pwd)"
  echo "== toolchain"
  go version || { echo "go not found on PATH ($PATH)"; exit 1; }
  node --version && npm --version
  echo; echo "== go mod tidy (verifies every hash against sum.golang.org)"
  go mod tidy || exit 1
  git --no-pager diff --stat -- go.mod go.sum
  echo; echo "== make test (UI build, go test ./... with the UI embedded, UI tests)"
  make test || exit 1
  echo; echo "== go test -tags noui ./..."
  go test -tags noui ./... || exit 1
  echo; echo "== make build"
  make build || exit 1
  echo; echo "== smoke test: start the darwin/arm64 binary, hit /api/health and the UI, stop it"
  BIN=dist/advisor-darwin-arm64
  [ "$(uname -m)" = x86_64 ] && BIN=dist/advisor-darwin-amd64
  "$BIN" -version
  ADVISOR_DATA_DIR="$(mktemp -d)" "$BIN" -port 27183 -no-browser &
  PID=$!
  for i in $(seq 1 20); do curl -sf http://127.0.0.1:27183/api/health >/dev/null && break; sleep 0.5; done
  curl -sf http://127.0.0.1:27183/api/health; echo
  curl -sf http://127.0.0.1:27183/settings | grep -qi '<!doctype html>' && echo "UI served: ok"
  CODE=$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: evil.example.com' http://127.0.0.1:27183/api/health)
  echo "foreign Host header -> HTTP $CODE (want 421)"
  echo; echo "== this Mac, as GET /api/hardware reports it (build plan step 2's gate: is it right?)"
  curl -sf --max-time 120 http://127.0.0.1:27183/api/hardware || echo "GET /api/hardware FAILED"
  echo
  kill $PID; wait $PID 2>/dev/null
  echo; echo "== the curated catalogue against Hugging Face (build plan step 4's gate: does every size resolve, with no weights downloaded?)"
  "$BIN" catalog check || exit 1
  CATALOGUE=ok
  "$BIN" catalog refresh -data-dir "$(mktemp -d)" || CATALOGUE=failed
  echo; echo "== git status"
  git status --short
  echo
  if [ "$CATALOGUE" = ok ]; then echo "ALL GREEN"; else echo "ALL GREEN EXCEPT THE CATALOGUE REFRESH: not every size resolved (see FAIL / STOPPED above)"; exit 3; fi
} 2>&1 | tee "$LOG"
STATUS=${PIPESTATUS[0]}
echo
if [ "$STATUS" -eq 0 ]; then echo "Finished: everything passed. Output is in $LOG."; else echo "Finished with errors (exit $STATUS). Output is in $LOG."; fi
echo "You can close this window."
read -r -n1 -p "" _ 2>/dev/null
