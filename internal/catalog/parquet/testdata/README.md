# Parquet fixtures

Written by `gen.py` here (pyarrow 25.0.1, the same C++ Parquet writer Arena
uses — its files say `parquet-cpp-arrow`) from **real rows**: the Arena
leaderboard rows Hugging Face's dataset viewer served on 2026-09-24, as
`scripts/verify.command` captured them (`.captures/external/`). Regenerate
with `python3 gen.py <captures folder>`; `expected.json` is pyarrow's own
reading of the files, which the test compares the reader with.

| File | Written as | What it exercises |
|---|---|---|
| `arena-text.parquet` | Arena's own settings, read from the footer of its `text/latest` file on 2026-09-24: Snappy, a dictionary page per chunk then `RLE_DICTIONARY` data pages (v1), every column optional, row groups of 1000 | the path Arena's files take |
| `v2.parquet` | data page v2, row groups of 128 | v2 headers, uncompressed definition levels, several row groups |
| `uncompressed.parquet` | no compression | the uncompressed path |
| `plain.parquet` | no dictionary | PLAIN values |
| `nulls.parquet`, `nulls-v2.parquet` | nulls in every column, int64 and int32 columns, pages of 256 bytes | definition levels, integers, several pages per chunk |

Not yet a byte-for-byte Arena file: Arena's real `text/latest` (589 KB,
10,606 rows, 11 row groups, written by parquet-cpp-arrow 19.0.1) is read
by `advisor catalog external` on the fleet machines, and verify.command
saves it to `.captures/external/`; a cut of it replaces `arena-text.parquet`
when one is taken.
