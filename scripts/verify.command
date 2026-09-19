#!/bin/bash
# The CI sequence, run on the Mac: double-click this file in Finder, or run
# `bash scripts/verify.command`. go mod tidy, make test, make build, then a
# resolves the curated model catalogue against Hugging Face (`advisor catalog
# refresh`, into a throwaway folder), then a smoke test of the built binary
# on that catalogue, which prints this Mac's hardware profile (GET
# /api/hardware), what the advisor recommends for it (`advisor
# recommend`), and build plan step 6's gate: the benchmark run twice on an
# installed model (they must agree within 5%), then cancelled twice (nothing
# may be left loaded) — `advisor bench`. Ollama must be running with at
# least one model installed; BENCH_MODEL=name picks the model (default: the
# smallest installed one the curated list knows). Everything it prints also
# goes to verify.log in the repo root so the result can be read back later.
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
  BIN=dist/advisor-darwin-arm64
  [ "$(uname -m)" = x86_64 ] && BIN=dist/advisor-darwin-amd64
  "$BIN" -version
  DATA="$(mktemp -d)"
  echo; echo "== the curated catalogue against Hugging Face (build plan step 4's gate: does every size resolve, with no weights downloaded?)"
  "$BIN" catalog check || exit 1
  CATALOGUE=ok
  "$BIN" catalog refresh -data-dir "$DATA" || CATALOGUE=failed
  echo; echo "== smoke test: start the binary on that catalogue, hit /api/health and the UI"
  ADVISOR_DATA_DIR="$DATA" "$BIN" -port 27183 -no-browser &
  PID=$!
  for i in $(seq 1 20); do curl -sf http://127.0.0.1:27183/api/health >/dev/null && break; sleep 0.5; done
  curl -sf http://127.0.0.1:27183/api/health; echo
  curl -sf http://127.0.0.1:27183/settings | grep -qi '<!doctype html>' && echo "UI served: ok"
  CODE=$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: evil.example.com' http://127.0.0.1:27183/api/health)
  echo "foreign Host header -> HTTP $CODE (want 421)"
  echo; echo "== this Mac, as GET /api/hardware reports it (build plan step 2's gate: is it right?)"
  curl -sf --max-time 120 http://127.0.0.1:27183/api/hardware || echo "GET /api/hardware FAILED"
  echo
  echo; echo "== what the advisor recommends for this Mac (build plan step 5's gate: is the top pick one you would give it, with reasons you would say out loud?)"
  for PURPOSES in chat coding long_context,vision; do
    echo; "$BIN" recommend -port 27183 -purposes "$PURPOSES" || echo "advisor recommend -purposes $PURPOSES FAILED"
  done
  echo; echo "== build plan step 6's gate: two runs of the same configuration agree within 5% on generation speed, and a cancelled run leaves nothing loaded"
  BENCH=ok
  BENCH_ARGS=(-port 27183)
  [ -n "$BENCH_MODEL" ] && BENCH_ARGS+=(-model "$BENCH_MODEL")
  "$BIN" bench "${BENCH_ARGS[@]}" -runs 2 || BENCH=failed
  echo; "$BIN" bench "${BENCH_ARGS[@]}" -cancel-after 4s || BENCH=failed
  echo; "$BIN" bench "${BENCH_ARGS[@]}" -cancel-after 20s || BENCH=failed
  echo; echo "== what the advisor recommends now that it has measured (the measured model's cards say so)"
  "$BIN" recommend -port 27183 -purposes chat || echo "advisor recommend FAILED"
  echo; "$BIN" bench -port 27183 -history
  kill $PID; wait $PID 2>/dev/null
  echo; echo "== git status"
  git status --short
  echo
  if [ "$CATALOGUE" != ok ]; then echo "ALL GREEN EXCEPT THE CATALOGUE REFRESH: not every size resolved (see FAIL / STOPPED above)"; exit 3; fi
  if [ "$BENCH" != ok ]; then echo "ALL GREEN EXCEPT THE BENCHMARK GATE (see NOT REPEATABLE / CANCEL above; is Ollama running with a model installed?)"; exit 4; fi
  echo "ALL GREEN"
} 2>&1 | tee "$LOG"
STATUS=${PIPESTATUS[0]}
echo
if [ "$STATUS" -eq 0 ]; then echo "Finished: everything passed. Output is in $LOG."; else echo "Finished with errors (exit $STATUS). Output is in $LOG."; fi
echo "You can close this window."
read -r -n1 -p "" _ 2>/dev/null
