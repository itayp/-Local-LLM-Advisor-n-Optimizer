# External benchmark sources — research note (step 9a)

**Read on 2026-09-24.** Every term quoted below was read on that date at the
URL beside it. Terms change: step 12 re-reads each clause before release, and
each client in `internal/catalog/external` carries its terms URL and the date
it was last checked, the way `data/hardware/gpus.yaml` rows carry theirs.

This is an engineering reading of published terms, done to decide what the
advisor may build on. It is not legal advice. The one commercial question it
raises (Artificial Analysis, below) is Itay's to decide, not a build step's.

Inputs: PRD §10 (external data, kept distinct from local measurements) and
§21 (comparability, subjectivity, rapid change, privacy, confidence);
BUILD_PLAN.md's Decisions ("official APIs and permitted sources only", no
telemetry, no LLM at runtime); ARCHITECTURE.md D-10, D-14, D-21, D-41; schema
v0's `catalog_external`.

---

## The decision

**Ingest, in this order:**

1. **Hugging Face Eval Results** — structured benchmark scores kept in each
   model's own repo, read through the Hub API the catalogue already calls.
   No new host.
2. **Arena (formerly LMArena) leaderboard dataset** — Arena's own CC BY 4.0
   dataset on Hugging Face, read through Hugging Face's Dataset Viewer API.
   One new host, still Hugging Face's.
3. **Epoch AI Benchmarking Hub** — Epoch's CC BY download, Epoch's own runs
   only. One new host.

**Hardware throughput: no external source in the MVP.** Nothing permitted
measures Ollama on consumer hardware. The speed term stays the estimator's,
narrowed by this machine's own benchmarks (D-48). The llama.cpp scoreboards
remain what they are today: a dev-side, cited input to `estimate.Config` and
`gpus.yaml`, never read at runtime.

**Excluded:** Artificial Analysis (its terms forbid exactly this use, at every
tier we could sign up for); OpenRouter's Data API; ollama.com's library pages
and registry, called directly; arena.ai's web pages and every third-party
mirror of them; the Open LLM Leaderboard (retired); LocalScore (no API, no
data licence); the llama.cpp discussion scoreboards at runtime; the Aider
leaderboard (stale); and any Hugging Face eval result whose own stated source
is an excluded publisher.

**Display rule, in one line:** public data and local data sit in two
separately titled blocks and are different types from Go to React, so a
public number can never be rendered by `<Figure>` or share a struct, a table
column or a sentence with an estimate or a measurement. The full rule (P-1 to
P-8) and the build list for step 9b are at the end.

---

## What makes a source permitted

A source is on the list only if all five hold.

1. **Official channel.** A documented API, or a download its publisher offers
   for reuse. Never a page parsed for its HTML, never an undocumented
   endpoint.
2. **Terms that allow this product.** The values may be shown inside software
   handed to other people, with obligations a desktop app can meet
   (attribution, a link, a date).
3. **No account and no key per install.** A beginner cannot be sent to sign up
   somewhere (product rule 1), and a key shipped inside the binary is a shared
   credential.
4. **Requests that reveal nothing about the machine.** The set of requests is a
   function of `families.yaml` alone — never of what is installed, the
   hardware, or the purposes picked (product rule 7, D-9). Every install sends
   the same requests.
5. **Relevant and current.** It says something about models a beginner runs
   locally, recently enough to matter.

## What the catalogue could gain

| Field | Where it would live | Candidates |
|---|---|---|
| Quality per purpose | `catalog_external` rows → the purpose-fit term only (BUILD_PLAN 9b) | HF Eval Results, Arena, Epoch; Artificial Analysis (excluded) |
| The original model's Hub repo, per size | new `hf_base_repo` per size in families.yaml | HF models API; the GGUF repo's `cardData.base_model` cross-checks it |
| Release date, for display and step 10 | new `catalog_models.released_at` | HF models API `createdAt` |
| Licence cross-check | `catalog_models.license_json` | HF `cardData.license` |
| The quant an Ollama tag pulls (ARCHITECTURE open item) | new `ollama_quant` per size | Ollama library page, **read by the curator by hand**; checked after a pull via the local `/api/show` |
| Hardware throughput | `gpus.yaml`, `estimate.Config` | llama.cpp scoreboards (dev-side, already); LocalScore (by eye only) |

---

## The candidates

### 1. Hugging Face

#### 1a. Hub models API (in use since step 4)

- **Exposes.** Per repo: files with sizes and LFS hashes, card metadata
  (licence, `base_model`, tags, gating), creation date, downloads and likes,
  parameter counts; GGUF headers by range read.
- **Through.** `GET https://huggingface.co/api/models/{repo}` (with `expand[]`
  fields) and range reads on `/resolve/` URLs.
- **Terms.** Hugging Face Terms of Service
  (<https://huggingface.co/terms-of-service>): content in a public repo is
  licensed to every user — the uploader agrees to "grant each User a
  perpetual, irrevocable, worldwide, royalty-free, non-exclusive license" to
  use and reproduce it through Hugging Face's services — and content carrying
  its own licence stays under that licence. Rate limits
  (<https://huggingface.co/docs/hub/rate-limits>) are per 5-minute window;
  the table (dated September 2025) gives an anonymous IP 500 API calls and
  3,000 resolver calls, with a 429 and `RateLimit` headers when exceeded.
- **Cost.** Free, anonymous. Paid tiers only raise limits we do not need.
- **Freshness.** Live.
- **Would fill.** Already fills `catalog_models` / `catalog_files`. Adds: the
  `base_model` cross-check for `hf_base_repo`, a licence cross-check, and
  `released_at`.
- **Not ingested from it:** downloads and likes. Popularity is not quality,
  and it would push older, better-known families back up — the opposite of
  what step 5's "quality beyond size" item needs.

#### 1b. Eval Results / Community Evals (launched February 2026)

- **Exposes.** Per model repo, YAML files in `.eval_results/`: the benchmark
  (a dataset repo registered as a benchmark, such as GPQA, MMLU-Pro, HLE,
  SWE-bench Verified), the task id, the value, optionally a date, a source
  (URL, name, user) and notes, and optionally a verification token. Results
  carry badges: *verified* (the run is provably reproducible), *community*
  (submitted through a pull request not yet merged), *source*.
- **Through.** `GET /api/models/{repo}?expand[]=evalResults` — the REST form
  of `huggingface_hub`'s `model_info(repo, expand=["evalResults"])`
  (<https://huggingface.co/docs/hub/leaderboard-data-guide>). Also a
  per-benchmark leaderboard (`GET /api/datasets/{id}/leaderboard`) and an
  aggregated Parquet file (`OpenEvals/leaderboard-data`); neither is needed
  for a curated list.
- **Terms.** The Hub ToS above; the results live inside the model repo, so the
  repo's own licence (Apache-2.0 for most curated families) travels with
  them. Hugging Face's own page on the feature says: "This is a work in
  progress feature." (<https://huggingface.co/docs/hub/eval-results>)
- **Cost.** Free, anonymous.
- **Freshness.** As fresh as the repo. Hugging Face staff are opening pull
  requests that transcribe the maker's model-card tables into this format —
  Qwen3.5-9B has one adding GPQA Diamond and MMLU-Pro, open since March and
  still open when read. So much of what exists today is **the maker's own
  numbers**, and some of it is unmerged.
- **A trap.** Anyone can attribute a result to a third party. The
  community-evals tooling's own example credits a score to Artificial
  Analysis. A Hugging Face entry is only as permitted as its stated source.
- **Would fill.** Quality per purpose, per size, via the metric → purpose map.
- **Verdict: INGEST, first.** Rules in "The three clients" below: read the
  original model's repo, not the GGUF repo; keep merged and verified entries,
  skip open pull requests; drop entries whose source is an excluded
  publisher; label the maker's own numbers as the maker's.

#### 1c. Open LLM Leaderboard and the OpenEvals aggregate

- The Open LLM Leaderboard is past tense in Hugging Face's own docs: it "was a
  project curated by the Hugging Face team"
  (<https://huggingface.co/docs/leaderboards/index>). Its data is frozen from
  before any curated family was released. **Excluded: stale.**
- `OpenEvals/leaderboard-data` bundles the official benchmarks into one
  Parquet file. Not needed — one Hub call per curated size gets the same
  numbers — and it would add a Parquet reader (D-22). Its own licence was not
  checked. **Not ingested.**

### 2. Artificial Analysis

- **Exposes.** Composite intelligence, coding and math indices;
  per-benchmark scores; output speed and time to first token **of hosted API
  providers**; prices.
- **Through.** Free Data API (`GET /api/v2/data/llms/models`, `x-api-key`
  header, 1,000 requests a day, attribution required); a paid Pro plan; a
  Commercial plan by order form (<https://artificialanalysis.ai/api-reference>,
  <https://artificialanalysis.ai/data-api>).
- **Terms.** Data Platform Terms v1.1, revised 2026-08-19, which state that
  they govern the free tier too
  (<https://artificialanalysiscdn.com/legal/ProDataPlatformTerms.pdf>). §2.4(c)
  forbids customers to "Embed or otherwise make raw Data available through any
  customer-facing product". §2.4(a) and (d) forbid making raw data available
  to third parties or combining it with other data into a product for them.
  §2.5 forbids using the data for any third-party product whose primary
  purpose is model or provider selection guidance, without written consent —
  which is this product's purpose. §2.6(c) forbids sharing credentials, so a
  key cannot ship inside the binary; the API docs separately ask that keys stay
  out of client-side code. The data-API page describes the free key as for
  internal use only, with no redistribution.
- **Cost.** Free tier (internal only); Pro paid; Commercial by quote.
- **Freshness.** Continuous.
- **Would fill.** Quality per purpose. Its speed figures describe cloud
  providers, not this machine — no field.
- **Verdict: EXCLUDED**, at every tier short of a Commercial order form that
  grants §2.5 consent in writing. That is a business decision for Itay. It
  stays excluded when it arrives indirectly: through OpenRouter's benchmarks
  endpoint (section 6), and through Hugging Face eval results whose source is
  Artificial Analysis (section 1b).

### 3. Arena (formerly LMArena)

- **Exposes.** Every published leaderboard as data: one subset per arena
  (`text`, `text_style_control`, `vision`, `vision_style_control`, `webdev`
  = Code Arena, `search`, `document`, `agent` and its per-signal subsets, and
  media arenas), each row with model name, organisation, licence, rating and
  its bounds, vote count, rank, category and the leaderboard's publish date;
  a `latest` split and a `full` history split. The agent subsets use a
  different score (IPS) with its own confidence bounds.
- **Through.** The Hugging Face dataset `lmarena-ai/leaderboard-dataset`
  (<https://huggingface.co/datasets/lmarena-ai/leaderboard-dataset>),
  announced by Arena as the way it releases its full leaderboard history
  (<https://arena.ai/blog/arena-leaderboard-dataset>). Read through the
  Dataset Viewer API (`https://datasets-server.huggingface.co`, `/splits` and
  `/filter`, JSON, up to 100 rows per call), so no Parquet reader is needed.
- **Terms.** The dataset card states "License: cc-by-4.0". CC BY 4.0 allows
  sharing and adapting, commercially included, with credit, a link to the
  licence and an indication of changes.
- **Cost.** Free, anonymous.
- **Freshness.** Snapshots published every few days to weeks; the agent board's
  latest was dated 2026-09-15 when read.
- **Coverage, honestly.** Arena rates what people chat with: mostly large and
  hosted models. The larger curated open models appear (Qwen 3.8 27B was on
  the latest agent board when read); the small sizes mostly do not, and are
  shown as having no public score — never borrowing a bigger sibling's.
  Ratings are relative, so only positions mean anything. Names carry
  effort variants ("(High)", "(Max)") from hosted runs; an alias pins the one
  variant that matches how Ollama runs the model by default, or none.
- **Would fill.** Quality for chat, writing, coding, reasoning, vision and
  agentic use, through categories and subsets named in the metric map (data,
  not code).
- **Verdict: INGEST, second.** The arena.ai pages themselves, and every
  third-party copy (daily-scrape GitHub repos, `api.wulong.dev`, scraping
  actors), are excluded: the official dataset exists.

### 4. Ollama library and registry

- **Exposes.** ollama.com/library pages: tags, sizes, the quant each tag pulls,
  pull counts, update dates. `registry.ollama.ai`: per-tag manifests (layer
  digests, sizes, media types), used by Ollama's own client.
- **Through.** Web pages, and registry endpoints that Ollama's client uses
  but Ollama does not document as a public API. There is no public API for
  browsing the library — third-party scrapers advertise exactly that gap.
  Ollama's documented web APIs (web search, cloud models) require an Ollama
  account key.
- **Terms.** Ollama Terms of Service, updated May 2026
  (<https://ollama.com/terms>), §4 lists what users may not do, including
  "Use automated means to access our services without permission", and using
  the services to develop competing products.
- **Cost.** Free.
- **Freshness.** Live.
- **Would fill.** The quant an Ollama tag pulls (ARCHITECTURE.md's open item),
  whether a tag exists, pull counts.
- **Verdict: EXCLUDED as an automated source.** What stays permitted, and is
  enough: (a) the user's own Ollama pulling on the user's click, exactly as
  steps 3 and 7 built it, after which the local `/api/show` reports the quant
  actually pulled; (b) the curator reading the library page by hand and
  writing `ollama_quant` per size into families.yaml. An installed model
  whose quant differs from `ollama_quant` is a curator flag, like an unknown
  model.
- **For step 12, not 9a:** `install.go` downloads the Ollama installer from
  `ollama.com/download/*` on the user's click. That is Ollama's documented
  install path, not a data source, but it touches the same §4 clause and is
  worth one look. Ollama's GitHub releases are the alternative host.

### 5. Epoch AI Benchmarking Hub

- **Exposes.** Benchmark results across many models, small and large, open and
  proprietary: Epoch's own runs (Inspect, consistent documented settings;
  GPQA Diamond, MATH Level 5, mock AIME, FrontierMath and others) and
  separately, results collected from official external leaderboards.
- **Through.** A ZIP of CSVs at `https://epoch.ai/data/benchmark_data.zip`
  (<https://epoch.ai/benchmarks/use-this-data>); also an Airtable API via a
  Python client, not needed. Standard-library `archive/zip` and
  `encoding/csv` read it: no new dependency.
- **Terms.** Epoch's data is "free to use, distribute, and reproduce provided
  the source and authors are credited" under CC BY. Data from external
  projects keeps its own licence (Epoch names Aider-derived data as Apache
  2.0), and benchmark questions belong to their creators.
- **Cost.** Free, no key.
- **Freshness.** The ZIP was marked updated on the day of reading; Epoch says
  new models often get results on release day.
- **Would fill.** Reasoning (GPQA, math) and coding where Epoch runs a coding
  benchmark, through the metric map.
- **Verdict: INGEST, third — Epoch's own runs only.** How many curated sizes
  it covers is unknown until 9b's first run. If the coverage report shows
  none, 9b ships the client switched off in `external.yaml` and records why,
  rather than a source that never shows anything.

### 6. OpenRouter Data API

- **Exposes.** Usage rankings of hosted models and apps, and a
  `/api/v1/benchmarks` endpoint re-serving Artificial Analysis indices,
  Design Arena ratings and OpenRouter's own search evals
  (<https://openrouter.ai/docs/cookbook/administration/data-api>).
- **Through.** REST, "gated by any valid OpenRouter API key"; 30 requests a
  minute per key, 500 a day per account.
- **Terms.** OpenRouter's own datasets are CC BY 4.0 with a citation line;
  the licence covers the aggregated data its endpoints return, and the data
  is not meant to be re-served as a competing free API.
- **Cost.** Free with an account.
- **Would fill.** Nothing local: hosted traffic, hosted prices.
- **Verdict: EXCLUDED.** It needs an account key per install or a shared key
  (criterion 3); it measures cloud use, not models on this machine; and its
  Artificial Analysis feed carries Artificial Analysis's restrictions whatever
  the wrapper's licence says — the advisor does not rely on a relicensing of
  a third party's data it cannot check.

### 7. LocalScore (Mozilla Builders)

- **Exposes.** A public database of prompt speed, generation speed and time to
  first token by CPU/GPU, for a fixed set of Llama 3.1/3.2 test models run
  through llamafile.
- **Through.** The website only. Its author wrote: "We're considering opening
  up APIs for the website" (<https://www.localscore.ai/blog>).
- **Terms.** The code is open source (Apache 2.0 / MIT); no licence is stated
  for the database.
- **Cost / freshness.** Free; community submissions.
- **Would fill.** Hardware throughput.
- **Verdict: EXCLUDED at runtime.** No official channel, no data licence, a
  different runtime (llamafile, not Ollama), fixed test models. A person may
  read it as a sanity check on `gpus.yaml`. If it ever publishes an API with a
  data licence, it belongs with PRD §11's community hardware data (Phase 2).

### 8. llama.cpp performance scoreboards (GitHub Discussions)

- **Exposes.** Community llama-bench results by GPU and backend — discussions
  #15013 (CUDA), #4167 (Apple), #10879 (Vulkan), #15021 (ROCm), #15396
  (gpt-oss): the sources behind step 5's speed constants.
- **Through.** Discussion comments on GitHub (web, GraphQL API).
- **Terms.** Comments are users' content under GitHub's terms, with no data
  licence of their own. GitHub's Acceptable Use Policies separate API access
  from scraping and allow scraping for research and archiving: "Researchers
  may scrape public, non-personal information from the Service for research
  purposes" (as worded in GitHub's site-policy history; step 12 re-reads the
  current text).
- **Cost / freshness.** Free; ongoing.
- **Would fill.** `estimate.Config` efficiency ranges and `gpus.yaml` rows —
  which it already does, by hand, with citations.
- **Verdict: stays dev-side; no runtime ingestion.** Strangers' numbers from
  other builds and settings, shown at runtime, would be exactly the "public
  benchmark as a guarantee of local performance" PRD §10 warns against.

### 9. Aider polyglot leaderboard

- **Exposes.** Code-editing pass rates, published on aider.chat and as YAML
  in Aider's GitHub repo.
- **Terms.** Apache 2.0, per Epoch's licensing note (not re-checked at the
  repo).
- **Freshness.** Trackers in 2026 still show 2025 models at the top; it looks
  unmaintained.
- **Verdict: EXCLUDED: stale, coding-only, mostly hosted models.** Epoch lists
  it among its external results, which the Epoch client does not take.

### 10. Mirrors, scrapers and aggregators

Scraping actors, daily-snapshot repos, hosted "free APIs" of other sites'
leaderboards, Hugging Face datasets that merge several sources: **excluded**.
They are copies that inherit their upstream's terms without the upstream's
channel. Where the upstream offers an official channel, the advisor uses that;
where it offers none, a copy does not create one.

---

## The three clients (what 9b builds)

### E-1. Hugging Face Eval Results

- **Request.** For each curated size, one
  `GET https://huggingface.co/api/models/{hf_base_repo}?expand[]=evalResults&expand[]=cardData&expand[]=createdAt`,
  conditional on the stored ETag. Confirm the `expand[]` spelling against the
  Hub's OpenAPI spec, and capture one real response as a fixture before
  writing the parser — step 4's method for GGUF headers.
- **Keep.** Entries merged into the repo's main branch (the maker's) and
  `verified` entries. Skip open pull-request (`community`) entries; the
  coverage report counts them so the curator sees what is pending.
- **Drop.** Any entry whose `source.url` host is on `external.yaml`'s excluded
  list (artificialanalysis.ai, openrouter.ai, …).
- **Store.** `metric` = `hf:<benchmark dataset id>/<task id>`; `value` as
  published; `source_date` = the entry's date, else its commit time;
  `provenance` = `maker` or `verified`; `license` = `HF-ToS; repo:<SPDX>`;
  `attribution` = "Reported by <maker> on Hugging Face" or "Verified on
  Hugging Face".
- **Also.** The GGUF repo's `cardData.base_model` must name `hf_base_repo`;
  a mismatch is a refresh warning. `createdAt` → `catalog_models.released_at`,
  shown as "released March 2026", never scored.

### E-2. Arena

- **Request.** `GET https://datasets-server.huggingface.co/splits?dataset=lmarena-ai/leaderboard-dataset`
  to learn which subsets exist, then `/filter` on split `latest` for each
  subset named in the metric map, 100 rows a page. Skip the whole run when the
  newest `leaderboard_publish_date` is unchanged.
- **Keep.** Rows whose `model_name` is in `aliases.yaml`. Unmapped rows with a
  non-proprietary licence go into the curator report — they are candidate
  aliases, or families step 10 may flag.
- **Store.** `metric` = `arena:<subset>/<category>`; `value` = rating (or IPS
  score); bounds, vote count, rank and the number of rows on that board in
  `detail_json`; `source_date` = `leaderboard_publish_date`; `provenance` =
  `crowd`; `license` = `CC-BY-4.0`; `attribution` = "Arena leaderboard
  dataset (arena.ai), <date>, CC BY 4.0 — position computed by the advisor".

### E-3. Epoch AI

- **Request.** `GET https://epoch.ai/data/benchmark_data.zip`, conditional
  (`If-None-Match` / `If-Modified-Since`), at most weekly; the refresh report
  records the bytes read, as step 4's does.
- **Keep.** Epoch's own runs only, for models in `aliases.yaml`. If the ZIP
  stops telling Epoch's runs apart from external ones, the client refuses the
  file and says why in words.
- **Store.** `metric` = `epoch:<benchmark>`; `provenance` = `independent`;
  `license` = `CC-BY-4.0`; `attribution` = "Epoch AI, 'Capabilities &
  Benchmarking', epoch.ai, accessed <date>".

### Common to all three

- **Aliases are data** (`data/catalog/aliases.yaml`): source → exact source
  model name → family id + parameters. Exact strings only; never
  fuzzy-matched at runtime. A miss is a curator flag, like an unknown
  installed model; the report may suggest the closest names but never writes
  them.
- **Per size, never per family.** No score is copied from one size to another,
  from a family to a size, or from one variant to another the source did not
  test.
- **When.** After the catalogue refresh, on its triggers (CLI, API, step 10's
  daily watch). Each source at most daily; Epoch weekly.
- **Failure.** Keep the last good rows, record the error in words in
  `catalog_refreshes.report_json`, show "public scores last updated <date>",
  and never block a recommendation.
- **Never delete.** A value that leaves its source gets `present = 0` and
  stops being shown or scored (the D-30 / D-35 pattern).
- **Polite and anonymous.** No token, no cookies, a User-Agent naming the
  advisor and its version.
- **Allow-list (D-10).** Adds `datasets-server.huggingface.co` and `epoch.ai`;
  `huggingface.co` is already on it.

---

## The display rule (PRD §10's split) — for step 9b

**P-1. Three kinds of number, three treatments.** *Estimated* and *measured*
describe this machine (D-14, `<Figure>`). *Public* describes the model, from
other people's tests, and has its own type and its own look. A public number
is never rendered by `<Figure>`, never styled like either of the other two,
and never shown as a range or a point.

**P-2. Two blocks, never one column.** Wherever both appear, the screen shows
a block titled "Public data" and a block titled "Your machine", as PRD §10's
example does. No table, row, column, cell, chart axis or sentence holds a
public number together with an estimate or a measurement. Screens that list
local numbers — Models, Benchmarks and its compare view — carry no public
column at all.

**P-3. Where public data appears in the MVP, and only there:** the model
detail view (both blocks); one "Public data" line on a Recommend card, below
and apart from the card's reasons — it is not a reason; and one bullet of its
own in a step 10 notification.

**P-4. Every public value says, directly beneath it and not behind a tap:**
who published it, what it tested in plain words ("a science exam",
"people's votes comparing answers"), and the source's own date — the date of
the leaderboard or the evaluation, never the day the advisor fetched it — plus
the attribution its licence requires. Provenance in words: *maker* → "reported
by the model's maker"; *verified* → "verified by Hugging Face"; *independent*
→ "tested by Epoch AI"; *crowd* → "rated by people comparing answers on
Arena".

**P-5. Words first, raw values under Advanced** (product rule 2). The default
view gives the size's position among the curated sizes that source has
scored, and how many that is: "Among the strongest for coding of the 9 models
here that Arena has rated." Thirds (strongest / middle / weaker); with fewer
than 6 scored, only "3rd of 5 rated". Advanced shows the raw value and its
scale, bounds or vote counts, the benchmark id, the provenance badge and the
fetch date.

**P-6. Public data carries no speed and no memory.** No external tok/s, GB or
load time is shown in the MVP. "Will it fit" and "how fast" come only from the
Your machine block.

**P-7. Absent is shown as absent.** "No public scores for this size yet" —
never a sibling size's score, never a family average, never a default (D-21).
Once in the block, a standing caveat: the scores are for the original model as
its maker published it; the download is a compressed copy (its quantization,
with the glossary `<Term>`), which may score a little lower.

**P-8. Public data never moves local numbers.** It does not narrow a range,
replace an estimate, or change D-42's confidence; a local measurement never
overwrites a public row. In the engine it reaches the purpose-fit term and
nothing else (next section).

**Copy.** Block headings "Public data — how others rated this model" and "Your
machine — estimated or measured here". A glossary entry, `public_benchmark`:
"A test someone else ran on this model, on their computers. It says how good
the model is at a kind of task, not how fast it runs on yours."

**Enforcement — the D-14 pattern, so the rule cannot erode one field at a
time:**

- **Go.** `figure.Public{Value, Scale, Origin}` with
  `Origin{Source, SourceURL, Date, Licence, Attribution, Provenance}`;
  marshalling refuses an empty `Source`, `Date` or `Attribution`, as
  `figure.Source` refuses its zero value. `figure.Check` accepts `Public` as a
  provenance-carrying type. New test `TestPublicAndLocalNeverShareAStruct`:
  in every type `server.APITypes()` lists, no struct directly holds both a
  `figure.Public` (or a slice of them) and a `figure.Bytes` / `figure.Rate`;
  they sit in sibling sub-objects (`public`, `local`).
- **UI.** `PublicValue` in `ui/src/api/types.ts` has no `source` field;
  `components/PublicFigure.tsx` is the only thing that renders it, and
  `<Figure>`'s props cannot accept it. A component test holds that the
  detail view's public block contains no `<Figure>` and the machine block no
  `<PublicFigure>`.
- **Store.** Unchanged: `catalog_external` versus `benchmark_runs` /
  `estimates`.
- **Engine.** A test that deleting every `catalog_external` row changes no fit
  category, speed range or confidence.

## How public data enters the recommendation — for step 9b

The purpose-fit term only (BUILD_PLAN 9b; D-41's other three factors
untouched). For each purpose asked for:

1. Take the (source, metric) pairs the metric map assigns to that purpose,
   keeping only those that cover at least `ExternalMinCovered` curated sizes
   (5, CHOSEN).
2. **Score only `verified`, `independent` and `crowd` values.** The maker's
   own numbers are shown (labelled, P-4) but not scored in the MVP: makers run
   their benchmarks with different settings, so ranking one maker's
   self-report against another's is the comparability risk PRD §21 names
   first.
3. For each remaining pair, the size's percentile among the covered curated
   sizes (direction from the metric map). Average them → *p* in 0..1.
4. Multiply that purpose's fit by `1 + ExternalWeight × (2p − 1)`, with
   `ExternalWeight` = 0.15 (CHOSEN, in `recommend.Config`): ±15% at most.
5. No covering pair → no adjustment, and the card says nothing public. That
   is the absence of a signal, not a default score: the UI shows the absence
   (P-7).

`ExternalWeight = 0` must reproduce today's golden outcomes exactly —
`recommend_test.go` unchanged — and Itay re-reads `advisor recommend` on each
fleet machine once it is on. A step 10 notification may carry "Strong coding
results (Arena, September 2026)" as one bullet when the size is in the top
third of a scored pair; it never qualifies a model on its own.

---

## Build list for step 9b

1. **Migration 0006.** `catalog_external` gains `source_date`, `source_url`,
   `provenance` (CHECK in `maker`, `verified`, `independent`, `crowd`),
   `attribution`, `detail_json`, `present`, `updated_at`. `catalog_models`
   gains `hf_base_repo`, `released_at`, `ollama_quant`.
2. **families.yaml.** Per size: `hf_base_repo` and optional `ollama_quant`
   (read by hand from the Ollama library). Header comments explain both;
   `advisor catalog check` validates them.
3. **`data/catalog/aliases.yaml`.** Exact source names → family + parameters,
   per source; the header states the rules above.
4. **`data/catalog/external.yaml`.** The approved sources (id, terms URL, date
   checked, licence, attribution template, cadence, on/off), the excluded
   hosts that the Hugging Face filter reads, and the metric → purpose map with
   direction and a plain-words benchmark name. The only place a source is
   switched on.
5. **`internal/catalog/external`.** One client per approved source, fixtures
   from real responses, a coverage report; `advisor catalog external
   [--report]`; runs after `catalog refresh`, including from
   `POST /api/catalog/refresh`.
6. **`internal/figure`.** `Public`, `Check`, `TestPublicAndLocalNeverShareAStruct`.
7. **`internal/recommend`.** The purpose-fit factor, `ExternalWeight`,
   `ExternalMinCovered`, tests.
8. **API.** A size's detail response with sibling `public` and `local`
   objects; a `public_line` per Recommend card; types mirrored in
   `ui/src/api/types.ts`; every new type in `APITypes()`.
9. **UI.** The model detail view's two blocks, the card line,
   `PublicFigure`, the glossary entry, copy in `en.ts`, the Advanced raw table.
   Check `Recommendations.tsx` (onboarding) and `Recommend.tsx` together —
   step 8's lesson about the duplicated screen.
10. **Network.** Allow-list and CLAUDE.md's network convention gain
    `datasets-server.huggingface.co` and `epoch.ai`.
11. **ARCHITECTURE.md.** D-53 records the approved list, the exclusions and
    the display rule, citing this note; D-10 points to it.
12. **Tests beyond the above.** An excluded-source entry in a Hugging Face
    fixture is dropped; an open pull-request entry is not scored; an alias
    miss is flagged, not guessed; no score crosses sizes; every public value
    in the API has a non-empty attribution and source date.

**Done when** (BUILD_PLAN's gate, plus one line): a recommended model shows a
public quality signal with its source and date, and a local measurement beside
it in a different treatment, on the same screen — **and** the coverage report,
run on the M1 Pro through `scripts/verify.command`, lists each approved
source's hits and misses across the curated sizes.

## Open items

- **Coverage is unknown until 9b's first run.** It decides whether Epoch is
  switched on and how often the public block says "no public scores yet".
- **Hugging Face Eval Results is a work in progress.** Fixtures, tolerant
  parsing and "keep the last good rows" are the mitigation; a shape change is
  a failed refresh, never a wrong number.
- **Arena's subsets and categories change.** They are read from the metric
  map; an unknown one is reported, not guessed.
- **Artificial Analysis** is excluded until and unless Itay signs a Commercial
  order form granting §2.5 consent.
- **Terms re-read in step 12**, with the checked date kept in `external.yaml`.
- **Community hardware data** (PRD §11) is Phase 2; LocalScore and the
  llama.cpp scoreboards are the prior art to read then.

## Sources read, 2026-09-24

- Hugging Face ToS — <https://huggingface.co/terms-of-service>
- Hub rate limits — <https://huggingface.co/docs/hub/rate-limits>
- Eval Results — <https://huggingface.co/docs/hub/eval-results>
- Leaderboard data API — <https://huggingface.co/docs/hub/leaderboard-data-guide>
- Leaderboards overview — <https://huggingface.co/docs/leaderboards/index>
- Community Evals announcement — <https://huggingface.co/blog/community-evals>
- Dataset Viewer API — <https://huggingface.co/docs/dataset-viewer/quick_start>
- Artificial Analysis Data Platform Terms v1.1 — <https://artificialanalysiscdn.com/legal/ProDataPlatformTerms.pdf>
- Artificial Analysis API — <https://artificialanalysis.ai/api-reference>, <https://artificialanalysis.ai/data-api>
- Arena leaderboard dataset — <https://huggingface.co/datasets/lmarena-ai/leaderboard-dataset>, <https://arena.ai/blog/arena-leaderboard-dataset>
- Ollama ToS — <https://ollama.com/terms>
- Epoch AI data — <https://epoch.ai/benchmarks/use-this-data>, <https://epoch.ai/benchmarks/about>
- OpenRouter Data API — <https://openrouter.ai/docs/cookbook/administration/data-api>
- LocalScore — <https://www.localscore.ai/blog>, <https://github.com/cjpais/LocalScore>
- GitHub Acceptable Use Policies — <https://docs.github.com/en/site-policy/acceptable-use-policies/github-acceptable-use-policies>
