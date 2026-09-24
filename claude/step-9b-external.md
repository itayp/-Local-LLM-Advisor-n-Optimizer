# Step 9b: external benchmark ingestion and the public/local split — open

**Status (2026-09-24): built and green here; the gate is not run yet.**
Everything in `research/EXTERNAL_SOURCES.md`'s build list for 9b exists and
passes `make check` and `make test` (Go with the UI embedded, and 85 UI
tests). What only a fleet machine can do is still owed, because the session
that built it could not reach any of the three sources (see "What the gate
still needs"). ARCHITECTURE.md D-53 is the record.

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

## What the gate still needs (on the M1 Pro, through `scripts/verify.command`)

1. **Real fixtures.** verify.command now saves every answer the sources give
   to `.captures/external/` (gitignored). The next session cuts fixtures
   from those (small: one repo's model-info answer, one `/splits`, one
   `/filter` page, a few rows of one Epoch CSV — never the whole ZIP),
   replaces `internal/catalog/external/testdata/`'s shaped ones, and fixes
   whatever shape differs. The README there says which files are shaped.
2. **The coverage report** it prints — each source's hits and misses across
   the curated sizes — confirms or corrects every "unconfirmed" metric id,
   Arena category and Epoch file name in `external.yaml`, and every alias in
   `aliases.yaml` (a wrong one shows as "aliases the source never answered
   with"; the candidates list shows the right names). If Epoch covers no
   curated size, switch it off in `external.yaml` and say why there.
3. **The done-when:** `advisor recommend -purposes chat -detail` (the last
   step of verify.command) shows the top pick's public value with its source
   and date and, apart, this Mac's measured speed from the step 6 run — the
   same thing the Recommend card and the detail view show in the UI. Itay
   also reads the recommendations with public scores on (the note: re-read
   `advisor recommend` on each fleet machine) and says whether ±15% is right.

## Carried forward

- Step 10: the notification bullet ("Strong coding results (Arena,
  September 2026)") when a size is in the top third of a scored pair — never
  a reason on its own.
- A curator flag for an installed model whose quant differs from its size's
  `ollama_quant`, once a size states one.
- Step 12 re-reads every source's terms (dates in `external.yaml`) and adds
  `PermittedHosts` to the allow-list audit.
