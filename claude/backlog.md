# Backlog

Small, concrete asks that surfaced from actually using the app, not tied to
one build-plan step. Not a replacement for BUILD_PLAN.md/PRD's own phase
lists — this is where a specific, dated ask lives until a step picks it up.

## a. A copy button anywhere a model name is shown

**From:** Itay, testing step 7 (2026-09-20), on the Benchmarks screen
especially.

Onboarding's "Use it" screen (`ui/src/onboarding/UseIt.tsx`) already has
one: `navigator.clipboard.writeText(modelName)`, a small state flip to
"Copied" for two seconds, `en.onboarding.useit.copy`/`.copied` for the
strings. Worth pulling into a small shared `<CopyButton value={...} />` in
`ui/src/components/` so it isn't retyped, then wiring it onto every place a
model's *exact* name is shown and might be pasted somewhere — the
Benchmarks model picker and its history rows, the Models screen (both step
8), and Recommend's cards (`<code>{r.pull_name}</code>`, step 5, currently
just text). Step 8's own scope; no API change needed.

## b. An explainer next to tok/s for what a speed is actually good for

**From:** Itay, same session.

`copy/glossary.ts`'s `tokens_per_sec` entry (step 7) explains what a
token and tok/s *are*; it says nothing about what a given number is good
*for*. `internal/recommend/reasons.go` already does a coarse version of
this in one templated sentence ("Estimated to answer at roughly 59 to 85
words a second — much faster than you can read") — the ask is to make that
comparison a persistent, reusable thing attached to every generation-speed
`<Figure>`, not just one reason on one card.

Needs product judgement before it's built, not just UI work: real
thresholds for "comfortable for back-and-forth chat" vs. "fine for a
document you'll read once in the background" vs. "too slow to be useful."
Reading speed (~200–250 wpm, so roughly 3–4 tok/s as "keeps up with
reading") is a reasonable anchor for the bottom end; the top end (when
more speed stops mattering) is less obvious and probably wants real user
reaction, not a guess. Flag for whoever picks this up: bring real numbers,
don't invent thresholds in the UI layer.

## c. Help setting up multiple GPUs (e.g. iGPU + dedicated), and checking Ollama sees the same

**From:** Itay, same session.

Already known and already surfaced, just not acted on:
`internal/hardware/derive.go:202` detects more than one discrete GPU and
adds a plain-language note — *"This computer has N graphics cards. The
advisor plans for the first one, the X; using several cards together is
not optimised in this version"* — but the profile only ever plans for
`p.GPUs[0]` (`derive.go:226-289`). The ask has two parts:

1. **Setup guidance** when multiple GPUs are found (especially the common
   laptop/desktop case of an integrated GPU alongside a dedicated one):
   which one Ollama will actually use, and how to steer that (Ollama env
   vars, disabling iGPU offload) rather than leaving the person with only
   the "not optimised yet" note.
2. **Checking the advisor's plan against what Ollama actually did.** The
   per-GPU `RuntimePaths` a benchmark run establishes (D-39, step 3 — read
   from the runtime's own log after a load) is the existing "what actually
   happened" signal; the gap is comparing it against which GPU the advisor
   *expected* and saying so explicitly when they disagree, rather than
   only recording both separately.

Both need real multi-GPU machines to build against responsibly — check
whether one exists in the fleet before scoping this into a step.

## d. llama.cpp can pool multiple GPUs "as one" on the Mac Pro

**From:** Itay, same session (2026-09-20). *Itay: you said you'd share the
documentation for this — once you do, attach it here or point me at it and
I'll fold in the details.*

Directly relevant to (c)'s limitation: Ollama (the only backend built so
far — step 3) plans for one GPU at a time, but Itay can already get both
cards on the Mac Pro working as a single pool through llama.cpp directly.
PRD §15 already lists llama.cpp as a future alternative backend, and §18
(Phase 2) lists it as a bucket; this is the specific capability that makes
it worth doing sooner rather than later — it's not just "another runtime,"
it's the fix for (c)'s stated gap on multi-GPU machines. Worth becoming
its own line in ARCHITECTURE.md once the llama.cpp backend is scoped,
citing whatever Itay shares.

## e. Progress visualization when downloading the models list — done (2026-09-24, ARCHITECTURE.md D-54)

**From:** Itay, step 8 closure (2026-09-20).

The model list fetch (`POST /api/catalog/refresh`) takes a minute or two
and showed only a button, then "Fetching…", then a result — nothing that
moved while it ran.

**Done** in the session after step 9b's first gate run: `GET
/api/catalog/status` carries the running fetch's phase (the list, then the
public scores), parts done of total and what is being read, and
`ui/src/components/ModelList.tsx` shows them — a bar and the seconds so far
— on every screen that needs the list. Itay's Windows findings of the same
day (the list never fetched and no way to fetch it; Benchmarks offering
only installed models; waits with nothing moving) are in
`claude/step-9b-external.md`, "Also fixed this session".
