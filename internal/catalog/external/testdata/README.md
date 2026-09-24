# External-source fixtures

**None of these files is a captured response.** The session that wrote
internal/catalog/external (build-plan step 9b, 2026-09-24) had no network
route to huggingface.co, datasets-server.huggingface.co or epoch.ai, so every
fixture here is *shaped from documentation*, and says which. Step 4's
method — capture one real response before writing the parser — is still
owed: `advisor catalog external -capture DIR` writes every answer the
sources give to DIR, and the first run of it on a fleet machine replaces
these files (and, where a shape differs, the parser) before step 9b's gate
is called met. The parsers are written to refuse, in words, any shape they
do not recognise — a shape change is a failed read, never a wrong number.

| File | Shaped from | What it exercises |
|---|---|---|
| `hf/Qwen_Qwen3.5-9B.json` | huggingface_hub 1.32.0: `ModelInfo.__init__` (`evalResults`, `createdAt`, `lastModified`, `cardData`), `parse_eval_result_entries` (an entry is the `.eval_results/*.yaml` object, or that object under `"data"`), `DatasetLeaderboardEntry` (the Hub's own `filename`, `verified`, `pullRequest` beside it). Values CHOSEN. | a maker's merged result; a verified one; one in an open pull request (skipped, counted); one whose source is an excluded publisher (dropped); a metric the map does not list; a result with no date |
| `hf/meta-llama_Llama-3.2-3B-Instruct.json` | the same, with no `evalResults` | a size with no public scores |
| `arena/splits.json` | Hugging Face Dataset Viewer docs, `/splits` (`{"splits": [{"dataset", "config", "split"}]}`); subset names from research/EXTERNAL_SOURCES.md §3 | which subsets exist |
| `arena/text-overall.json` | Dataset Viewer docs, `/filter` (`features`, `rows[].row`, `num_rows_total`); column names from the note (`model_name`, `leaderboard_publish_date`) and external.yaml. Ratings CHOSEN. | aliased rows, an unmapped open-licence row (a candidate), a proprietary row (not one) |
| `epoch/gpqa_diamond.csv` | Epoch AI's "use this data" page (a ZIP of CSVs, Epoch's runs apart from `*_external.csv`); column names are external.yaml's, unconfirmed. Scores CHOSEN. | two runs of one model (the later stands), an unmapped name |
| `epoch/aider_polyglot_external.csv` | the same | a file of other people's results, which must never be read |

The ZIP itself is assembled from the CSVs by the test, not stored.
