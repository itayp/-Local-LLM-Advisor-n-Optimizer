# Step 9b: external benchmark ingestion and the public/local split — open

**Status (2026-09-24, evening): the gate has run once and failed on
coverage; fixed, and waiting for the second run.** The first
`scripts/verify.command` on the M1 Pro (20:58) was all green as code but
stored no public value at all — 0 of the curated sizes on each source. The
causes and fixes are below ("First gate run") and in ARCHITECTURE.md D-54.
The same session fixed what Itay found testing on Windows (the model list
limbo, the Benchmarks picker, waits with nothing moving). `make check` and
`make test` pass (Go with the UI embedded; 94 UI tests).

## What exists

- **Data.** `data/catalog/external.yaml` — the three approved sources
  (Hugging Face Eval Results, Arena's leaderboard dataset, Epoch AI's
  benchmark ZIP), each with its hosts, terms URL and date read, licence,
  attribution template and cadence; the excluded publishers; the metric →
  purpose map with plain words and scale. `data/catalog/aliases.yaml` —
  Arena's and Epoch's exact names for catalogue sizes. `families.yaml` —
  `hf_base_repo` on every size (the original model's repo), optional
  `ollama_quant` (none filled in: it is read by hand from the Ollama
  library). `advisor catalog check` validates all three.
- **Go.** `internal/catalog/external`: `config.go` (strict loaders,
  validation, `PermittedHosts` — the code's ceiling on hosts), `fetch.go`
  (one polite client: throttle, Retry-After, bounded retries, no token or
  cookie, redirects only to the permitted hosts, `-capture`), `hfevals.go`,
  `arena.go`, `epoch.go`, `run.go` (cadence, per-source state, the coverage
  report, `Stored` for `-report`), `public.go` (the `View`: a size's public
  entries with positions in words, the card's one line, what the engine may
  score, the "last updated" sentence). `internal/figure/public.go`
  (`Public`, `Origin`, `Provenance`) and `CheckSeparation`. Migration 0006
  and `internal/store/external.go`. `recommend.Config.ExternalWeight` (0.15)
  and `ExternalMinCovered` (5), `Engine.Public`, `Factors.Public`; the
  engine prefers a size's `ollama_quant` when stated. `refresh.Options.External`
  runs the sources after the sizes. Server: `GET /api/models/{id}/detail`,
  the card's `public` line, `/fit` refactored into `modelFit`. CLI:
  `advisor catalog external`, `catalog refresh -no-external`,
  `advisor recommend -detail`.
- **UI.** `components/PublicFigure.tsx` (`PublicFigure`, `PublicLine`),
  `screens/ModelDetail.tsx` at `/models/:id` (a routed screen, not in the
  navigation — `Screen.hidden`), the public line on both recommendation
  components (Recommend and onboarding's Recommendations — step 8's lesson),
  a "Details" link from Recommend cards and from matched rows on Models,
  the `public_benchmark` glossary entry, copy in `en.ts`, the Advanced
  "Public scores" row.
- **Tests.** Every item of the note's list 12: an excluded-source entry is
  dropped; an open pull request is counted, not stored or scored; an alias
  miss is reported, never guessed; no score crosses sizes; every public value
  marshals with its attribution and date (`figure.Public` refuses
  otherwise). Plus: `ExternalWeight = 0` reproduces every golden result
  byte for byte; public data moves the purpose term by at most ±15% and
  nothing else (engine and wire); `TestPublicAndLocalNeverShareAStruct`;
  requests are identical on two different installs; a failed source keeps
  its rows and says so; the detail view's public block has no `<Figure>` and
  the machine block no `<PublicFigure>`; `<Figure>` cannot type-check a
  public value.
- **Seen rendered.** A daemon built here, with a seeded database, served
  the Recommend card's public line and the detail view's two blocks as
  designed (screenshots were checked in the session, not committed).

## Decisions worth knowing next session

- **`/api/models/{id}/detail`, not `/api/models/{id}`**: the bare path
  conflicts with `/api/models/installed` and `/pull` in Go's mux.
- **`publisher`, not `source`**, is the origin's who-field on the wire, so a
  public value has no key a local figure's could match.
- **Only mapped values are stored.** An unmapped Arena or Epoch name goes
  to the report as a candidate; a Hugging Face metric the map does not list
  is reported and stored nowhere — it would have no plain words to show.
- **Maker values are shown with a position** among the sizes with that
  score on Hugging Face, labelled "reported by the model's maker", with a
  line saying they are not used to rank. They are never scored.
- **The card's line is the API's, not the engine's**: the engine scores;
  the server picks the line from the same `View`.

## First gate run (verify.log, 2026-09-24 20:58) — what it showed, what changed

- **Hugging Face Eval Results, 0 of 22.** The real task ids are `diamond`
  (GPQA), `hle` (HLE) and `swe_bench_%_resolved` (SWE-bench Verified) —
  huggingface_hub's docs had `gpqa_diamond` / `default`. Fixed in
  external.yaml; MMMU-Pro vision added (merged into the Gemma 4 26B/31B
  repos). Almost everything else is in open pull requests (222 results),
  which D-53 does not read: **a product decision for Itay**, not changed.
  The Hub's own `{"filename", "error"}` entries (Nemotron's unparsable
  files) are now reported in the Hub's words.
- **Arena, 0 of 22.** Every board failed part-way with HTTP 500 "the dataset
  index is loading". A 500 is now retried (a "loading" one after 5 s, then
  10, 20), and a source read in part is due again at the next refresh, not
  in 24 hours. Aliases added for every curated name seen in the rows read
  (Gemma 4 31B and 26B, Qwen3.8 27B, GLM-4.7-Flash, Nemotron 3 Nano); the
  Llama and gpt-oss names are below row 200 and still unconfirmed.
- **Epoch, 0 of 22.** aliases.yaml had no Epoch entries. Added for every
  curated size Epoch ran (thinking-on runs; gpt-oss at medium), with the
  near misses listed as deliberately not mapped. OTIS mock AIME added as a
  second reasoning metric.
- **Base-model warnings** for Ministral 3 and Llama 3.1 were the same
  weights under another name: `hf_base_same_as` in families.yaml.
- **Fixtures** are now cut from `.captures/external/` (the testdata README
  says which two cases are still shaped).
- Replaying the captured answers through the new configuration: public
  values for 9 sizes (Epoch for all nine, the Hub for three), before Arena
  is read whole.

## Second gate run (verify.log, 2026-09-24 22:28)

Public values for Hugging Face 3 sizes (GLM-4.7-Flash, Gemma 4 26B/31B),
Epoch 9, Arena 10 — the Llama and gpt-oss aliases confirmed; recommendations
now carry public lines ("Llama 3.1 8B: among the weaker for everyday chat of
the 10 models here that Arena has rated", purpose term ×0.917). Arena's
creative-writing and vision boards failed again with "index is loading".
The run was not stuck, but Arena alone took about fifteen minutes (43
requests) — and on Windows that looked like a hang, with Epoch waiting
behind it. Fixed (ARCHITECTURE.md D-55): two requests per Arena board (one
row, then the aliased names only), a three-minute deadline per source,
Arena read last, and the progress names the board or repo being read.
The chat top pick on the M1 Pro, Ministral 3 8B, still has no public score
from any approved source.

## Third gate run (2026-09-24 23:14) — Arena from its files, in the background

Arena gave up after three minutes of "the dataset index is loading". Itay
chose a small Parquet reader of our own (no new dependency) and public
scores loading in the background (ARCHITECTURE.md D-56): Arena is now read
from the files it publishes on the Hub (the text leaderboard, 589 KB, all
categories at once; two requests per subset), and `POST
/api/catalog/refresh` answers once the list is in while the public scores
follow, shown as a quiet note on every screen that needs the list.
The reader is tested against pyarrow-written files of real Arena rows;
the fourth verify run reads Arena's actual file and saves it for a real
fixture.

## Also fixed this session (Itay, testing on Windows)

- **No dead end without the model list.** `GET /api/catalog/status`, and
  one component on Recommend, Benchmarks, Models and onboarding that offers
  "Get the model list (a few MB of descriptions, no models)" whenever the
  list was never fetched — whatever else the machine has — and shows the
  fetch's progress (step 1 of 2 the list, step 2 of 2 the public scores,
  parts done, what is being read, seconds so far). Recommend keeps a quiet
  "fetched on … · get it again" line. The detail view offers the fetch when
  public scores were never read (the Windows symptom behind "Public scores
  have not been fetched yet").
- **Benchmarks offers every model.** `GET /api/bench/models`: "Models you
  have installed", then "Models you don't have yet (downloaded first)" —
  the list's sizes that would run here, with their download size. For one
  of those, the button is "Download 6.4 GB, then run the test": it shows
  the download, then starts the test by itself unless the plan refuses it.
  "Test it on this computer" (Recommend cards, the detail view) opens
  Benchmarks with that model picked (`?model=`).
- **Every wait moves.** An indeterminate bar and the seconds so far for
  loading and planning; a test shows its progress from the click (onboarding's
  "Try it" no longer shows its button again between the click and the first
  event); and if the progress stream is held back (seen on Windows), the
  screen polls `GET /api/bench/{id}/progress` instead.

## What the gate still needs (on the M1 Pro, through `scripts/verify.command`)

1. **A third run, for time.** The second report confirmed coverage; the
   third should show the public-data step taking a minute or two, not a
   quarter of an hour (the report now prints retries and seconds waited).
   Earlier plan for this item: Expected:
   Hugging Face hits for GLM-4.7-Flash and the Gemma 4 26B/31B; Epoch for
   the nine sizes above; Arena — if the dataset viewer has finished loading
   — the aliased sizes on each board, and "aliases the source never
   answered with" confirming or removing the Llama and gpt-oss names.
2. **The done-when (unchanged):** `advisor recommend -purposes chat -detail`
   shows the top pick's public value with its source and date and, apart,
   this Mac's measured speed. Note the top pick for chat on the M1 Pro was
   Ministral 3 8B, which no approved source covers yet — the detail block
   will honestly say "no public scores for this size yet"; the gate is met
   by any recommended size that has one (Gemma 4, Qwen3.5 9B, Llama 3.1 8B
   for reasoning). Itay reads the recommendations with public scores on and
   says whether ±15% is right.
3. **Itay's decision:** keep ignoring Hugging Face results in open pull
   requests (the D-53 rule), or read them labelled as unreviewed.
4. **On Windows:** the fetch button, its progress, and the download-then-test
   flow on a real machine; and whether the test's progress arrives (stream or
   polling).

## Carried forward

- Step 10: the notification bullet ("Strong coding results (Arena,
  September 2026)") when a size is in the top third of a scored pair — never
  a reason on its own.
- A curator flag for an installed model whose quant differs from its size's
  `ollama_quant`, once a size states one.
- Step 12 re-reads every source's terms (dates in `external.yaml`) and adds
  `PermittedHosts` to the allow-list audit.
