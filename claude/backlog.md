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

## f. Top recommendations with no public scores

**From:** Itay, reading the third step 9b gate run (2026-09-24, the M1 Pro).
Parked on purpose: the catalogue has only a few sizes per size class today,
so fixing this before the catalogue grows could mean fixing it twice. When
the catalogue next grows, check whether it still happens.

**The symptom.** Most top picks on a 16 GB Mac have no public data: Ministral 3 8B
(chat #1), Gemma 4 E4B, Qwen3.5 9B for coding, Qwen3.5 4B, Ministral 3 3B.
The only scored card, Llama 3.1 8B, is labelled "among the weaker" and moved
down (×0.917).

**What it is not.** It is not about timing: that run had already read all
three sources and Ministral 3 8B still had nothing. "Don't recommend until
the public data is in" would not change the picks.

**Cause 1: the sources barely cover small, new models.** From the
captures in `.captures/external/` (Arena text board 2026-09-13; Epoch ZIP
2026-09-24):

- Arena rates no size of Ministral 3, Gemma 4 below 26B, or Qwen3.5
  below 27B. The current-generation open models it does rate below ~15B:
  `granite-4.2-3b` (1297), `granite-4.2-8b` (1317), `gemma-3-4b-it` (1291),
  `gemma-3-12b-it` (1334), `gemma-3n-e4b-it` (1305), `molmo-2-8b` (1294). For
  comparison, `llama-3.1-8b-instruct` is 1186 and `llama-3.2-3b-instruct`
  is 1109.
- Epoch (mapped metrics: GPQA Diamond, MATH L5, OTIS AIME, SWE-bench
  Verified) has `qwen3.5-9B` (already aliased), `qwen3-4b-instruct-2507`,
  `qwen3-8b`, `qwen3-14b`, `phi-4`, `gemma-3-{1b,4b,12b}-it`,
  `granite-4.0-{350m,1b,micro}`, `mistral-small-3.2-2506`. It has only
  thinking-off runs of Qwen3.5 2B and 4B (`_none`, not mapped, see
  aliases.yaml) and only the 2410 Ministral, not Ministral 3.
- Scores sit in open pull requests on Hugging Face (skipped under D-53) for
  Gemma 4 E4B (MMMU-Pro), Gemma 4 12B (MMLU-Pro, HLE, MMMU-Pro, AIME 2026) and
  Qwen3.5 4B (MMMU-Pro). They come from the model cards, so even if read they
  would be shown but never used for ranking. This is gate item 3 in
  step-9b-external.md.
- Ministral 3 has no eval results on Hugging Face at all.

**Cause 2: a bias in the engine (`recommend.publicAdjustment`).** A size's position is
computed among *all* curated sizes a signal scored, 70B included, so
any scored small model lands near the bottom and loses up to 15%. An
unscored size gets exactly 1.0 (as if in the middle). So having a score
counts against a small model, and unscored sizes rise to the top. This happens
whatever the catalogue holds.

**Options discussed (none chosen):**

1. Compare positions within a size class (by effective parameters, or
   among the sizes that are candidates on this machine), so a score
   compares like with like. A catch with the "candidates on this machine"
   version: on the M1 Pro the only scored chat candidates are Llama
   3.2/3.1, so Llama 3.1 8B would come *first* for beating even older
   models. Size bands look safer; it needs a test on the golden profiles
   either way.
2. Add scored small families alongside the current ones (not instead):
   Granite 4.2 3B/8B, Qwen3 4B-Instruct-2507, Phi-4 14B, Mistral Small 3.2
   24B. Gemma 3 4B/12B are covered too, but Gemma 4 has replaced them. Each needs
   its GGUF repo, Ollama tag and aliases checked by `advisor catalog
   refresh` / `external -report` before it goes in.
3. Better wording for "public data: none for this size and purpose": say
   why (public leaderboards rarely rate models this small) and point to the
   two-minute test as the evidence that counts here.
4. Only recommend scored sizes. Rejected for now: on small and medium
   machines it swaps in older, weaker models (Llama 3.1 8B over Ministral 3 /
   Gemma 4 E4B), which goes against product rule 6.
5. Read pull-request eval results labelled "unreviewed" (gate item 3). This
   adds shown values for Gemma 4 E4B/12B and Qwen3.5 4B, but nothing scored.

## g. How to run: UI guidance after model selection, or button to start Ollama

**From:** Itay, in-app onboarding feedback (2026-09-25).

When the user selects a model on the Recommend or Models screen, they need
to know the next step: is Ollama running? If not, how do they start it?
Once it runs, what command or button launches the model?

Product rule 1: the user never needs a terminal. Today, there's no in-app
path — they get a recommendation and are left to figure out Ollama on their
own. Options:

1. A button on Recommend/Models that says what it does: "Start Ollama and
   load [Model Name]" — pre-fills the URL in Ollama's CLI or (better) calls
   the Ollama HTTP API to load the model directly if Ollama is already
   running. A check on page load (`GET /api/backend/status`) sees whether
   Ollama is listening.
2. A "How to run" explainer on the Benchmarks and Models screens showing
   the exact incantation the user needs, in plain language (e.g. "Run
   `ollama pull mistral` and then `ollama run mistral`") tailored to the
   selected model name — pull from the model's card (step 4's catalogue) or
   generate it dynamically.
3. Both: a button as primary (faster, one click), the explainer as fallback
   or "next step" guidance.

Needs product scope before building: how much automation is safe (Ollama CLI
call vs. HTTP request vs. just instructions), and what happens when Ollama
is not installed or the load fails. Flag for step assignment: this connects
recommendation to actually running a model — early UX gain and a blocker on
the "I got a recommendation, now what?" question.

## h. Support for other inference backends (llama.cpp, LM Studio, etc.)

**From:** Itay, in-app onboarding feedback (2026-09-25).

PRD §15 already lists llama.cpp and other backends as future phases, but
the question is coming up from testing: "I don't want to use Ollama, I have
llama.cpp / LM Studio / [other tool] set up already — can the advisor work
with that?"

Today only Ollama is supported (`internal/backend/ollama`); the backend
registry lives in `internal/backend/registry.go` (step 3) and a new backend
takes `Load()`, `Unload()` and `PS()` methods.

Related backlog items that affect scope:

- **(c)**: Multiple GPUs — llama.cpp can pool them on the Mac Pro, making
  it a priority backend for that use case sooner than a general "other
  backends" phase.
- **(d)**: Documents this capability for Mac Pro multi-GPU.

Outline for step assignment (not a full scope, just what it unlocks):

1. **llama.cpp backend**: matches the priority of (c)/(d). It reads a Unix
   socket or HTTP endpoint, runs on macOS, Linux, Windows. Exists at
   `~/.local/share/llama.cpp/llama-server` or a configured path; check for
   it on startup, or let the user configure the endpoint. Port it uses is
   typically 8000 (user-configurable). The load and PS calls parse llama.cpp's
   HTTP API (different from Ollama's). Real machine for testing: Itay's Mac
   Pro with both GPUs via llama.cpp.
2. **LM Studio backend**: reads the HTTP API at `localhost:1234`
   (default), similar to Ollama. Simpler to add second, since both expose
   HTTP. Tested against LM Studio on Windows/Mac, not Itay's current fleet.
3. **Detection**: on startup, Ollama responds at `localhost:11434` by default
   (also configurable); llama.cpp at 8000, LM Studio at 1234. If more than
   one is listening, the UI needs to let the user pick which one (step 8, the
   Settings screen already has a backend picker drafted in backlog but not
   wired up). If none is listening, a clear message saying so, with links to
   install guides.

Worth a dedicated build-plan step once product prioritizes multi-GPU support
or the number of "can I use [tool] instead?" questions hits a threshold.
For now, scoped as a backlog idea and a feeder to (c) and (d)'s scope.
