# External-source fixtures

Most of these files are **real answers**, cut from what `scripts/verify.command`
captured on the M1 Pro on 2026-09-24 (`advisor catalog external -capture`,
saved to `.captures/external/`, which is gitignored). "Cut" means: a JSON
answer re-indented and otherwise unchanged; a board or a CSV reduced to a
few of its real rows, never edited. The few **shaped** files cover cases no
real answer has shown yet, and say so below. A shape change in a source is a
failed read in words, never a wrong number: the parsers refuse what they do
not recognise.

| File | Real or shaped | What it exercises |
|---|---|---|
| `hf/zai-org_GLM-4.7-Flash.json` | real, whole | two results merged into the maker's repo (task ids `diamond`, `hle`), one in an open pull request (`"pullRequest": 69`) |
| `hf/google_gemma-4-31B-it.json` | real, whole | one merged result (`MMMU/MMMU_Pro` `mmmu_pro_vision`) among twelve in pull requests |
| `hf/nvidia_NVIDIA-Nemotron-3-Nano-30B-A3B-BF16.json` | real, whole | the Hub's own `{"filename", "error"}` entries for files it could not parse (no `data`) |
| `hf/meta-llama_Llama-3.1-8B-Instruct.json` | real, whole | every result pending, one of them an unparsable file inside a pull request |
| `hf/meta-llama_Llama-3.3-70B-Instruct.json`, `hf/Qwen_Qwen3.5-4B.json` | real, whole | every result pending |
| `hf/meta-llama_Llama-3.2-3B-Instruct.json` | real, whole | a repo with no `evalResults` key at all |
| `hf/Qwen_Qwen3.5-9B.json` | **shaped** (huggingface_hub 1.32.0's `ModelInfo` and `parse_eval_result_entries`; task ids corrected to the real ones). Values CHOSEN. | a **verified** result and one whose source is an **excluded publisher** — neither seen in a real answer yet; a metric the map does not list; a value that is not a number |
| `arena/text-latest.parquet` | real rows (the text board's overall and creative-writing rows the dataset viewer served on 2026-09-24, 300 of them), written as a Parquet file the way Arena writes its own — see `internal/catalog/parquet/testdata/README.md` | the file Arena publishes: three aliased sizes, open-licence names nothing maps (candidates), proprietary rows (not candidates), a board the map names that the file lacks (`coding`) |
| `arena/text-latest-no-rating.parquet` | the same rows, the `rating` column dropped | a file without a mapped column: refused in words |
| `epoch/gpqa_diamond.csv` | real rows (six) with the real header | an aliased run and its "_none" twin (not aliased), a name that only looks like a catalogue family, quoted fields spanning lines |
| `epoch/aider_polyglot_external.csv` | real rows (two) with the real header | a file of other people's results, which must never be read |

`TestEpochKeepsTheLatestRun` builds its own small ZIP (shaped): the real file
has one run per name today, so "the latest of two runs stands" has no real
example yet. The ZIP for the other tests is assembled from the CSVs above.
