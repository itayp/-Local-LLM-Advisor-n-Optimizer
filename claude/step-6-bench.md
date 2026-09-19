# Step 6: benchmark harness — closed

**Status (2026-09-19): the gate is met and step 6 is closed.** The M1 Pro
re-ran on suite 2 through `scripts/verify.command` and passed, which was the
last machine owing a result. ARCHITECTURE.md D-51 is the record; D-50 is the
decision it closes.

## Gate results

The gate: two consecutive runs of the same configuration agree within 5% on
generation speed on the NVIDIA machine and the Apple Silicon one, and a
cancelled run leaves nothing loaded.

| Machine | Suite | Repeatability | Cancel |
|---|---|---|---|
| Windows RTX 5070 Ti (Benchmarks screen) | 1 | Passed. llama3.2:1b +0.4% at 4k and −0.1% at 32k (430 tok/s); qwen3:14b +0.2% at 32k (81 tok/s) | Passed. The run showed as "Stopped" and its memory was freed |
| Mac Pro D700 / Vulkan, Ubuntu (screen) | 1 | Passed. llama3.2:1b −0.1% (53.2 tok/s) | Not run on screen |
| M1 Pro (`verify.command`) | 2 | Passed. llama3.2:3b 54.9 → 52.5 tok/s (−4.4%, limit 5%) | Passed in both phases, loading and measuring: the daemon and Ollama's own `/api/ps` both reported nothing loaded |

Two machines are on suite 1 and one on suite 2. Suites are never compared,
so a Windows run on suite 2 is worth taking when that machine is next up —
as confirmation, not as a gate that is owed. That was a deliberate call, not
an oversight.

## What the M1 Pro run showed

- **The gate model was llama3.2:3b, not llama3.1:8b.** D-50 §3 predicted
  llama3.1:8b; the rule it states picks the smallest installed curated model
  of at least 3 billion parameters that fits, and llama3.2:3b is 3.21
  billion. The code is right and D-50's example of it was wrong. D-51 says
  so rather than editing D-50.
- **The margin is the short prompt's.** The headline is the shortest prompt
  with an answering speed, here the 501-token one, whose three timings
  inside run 2 disagreed by 20.6% — the harness said so in words. The
  1,970-token prompt moved 0.8% across the same two runs. Raising the 3B bar
  or headlining a longer prompt would each likely tighten the 4.4%; neither
  is worth a change on one laptop's run, so both are recorded in D-51 as one
  open item for a later step to settle with more fleet data.
- **Suite 2 fixed what it was meant to fix.** Every timed answer ran to the
  full 256-token budget, so nothing fell under `MinAnswerTokens` and no
  prompt lost its answering speed. The 7,472-token prompt was skipped at a
  context of 4,096 with the reason in words.
- **The write-back is visible on a real machine.** Run 2's plan was already
  estimated at 44–60 tok/s where run 1's was 53–85, because run 1's
  measurement had replaced the estimate. Both runs read the metal path and
  the f16 cache from Ollama's log.
- **Wired memory moves between runs.** The same model reported 2.8 GB then
  3.2 GB of graphics memory taken while Ollama's own size stayed 2.4 GB. On
  Apple Silicon that number is wired memory, so it carries other processes
  with it — which makes a Mac weaker evidence than the NVIDIA machine for
  settling the 92% "fits" threshold. Noted in D-51.

The rest of `verify.log` was green: `make test`, `make test-go`, `make
check`, `make build` for all four targets, 40 UI tests, the foreign-Host
rejection (421), and step 4's live catalogue refresh with every size
resolved and no weights downloaded.

## Carried into step 7

1. A Windows run on suite 2, when that machine is next up. `make build` in
   verify refreshes `dist/advisor-windows-amd64.exe`.
2. Whether a memory-paced gate model or a longer headline prompt tightens
   repeatability (D-51). Every run stores its per-prompt spread, so the
   fleet's next benchmarks are the evidence.
3. Capture a real `server.log` stretch around a load on each machine, to
   replace the load-report fixtures shaped from source
   (`internal/backend/ollama/testdata`).
4. Step 5, item 2 (Qwen3.5 4B vs 9B as the M1 Pro's chat #3) has not been
   investigated.
5. Load time: two Windows runs at different contexts both reported 1,675 ms.
   The Mac's loads differ each time (1,865 / 2,800, then 2,362 / 1,576), so
   it stays a probable coincidence with `GET /api/bench/{id}` as the check.

Step 7 is onboarding. Its "Try it" button is
`POST /api/bench {prompts:["500"]}`.
