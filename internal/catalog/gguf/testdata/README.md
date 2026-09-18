# GGUF header fixtures

Captured, not chosen. Each `*.gguf.head.gz` is the first bytes of a real
model file — the whole header (magic, version, counts, every metadata pair,
tokenizer included) rounded up to the next 4 KiB, and none of the weights —
gzipped. They were read on the fleet's M1 Pro on 2026-09-18 from Ollama's
blob store (`~/.ollama/models/blobs`), because Hugging Face was not
reachable from the session that wrote the parser; the files are the same
GGUF format either way, and `llama3.2-1b` carries llama.cpp's own key order,
which is the order Hugging Face GGUFs have.

| Fixture | Ollama model | Blob (sha256) | File size | Header ends at |
|---|---|---|---|---|
| `llama3.2-1b` | `llama3.2:1b` (Q8_0) | `74701a8c35f6c8d9a4b91f3f3497643001d63e0c7a84e085bed452548fa88d45` | 1,321,082,688 | 7,822,528 |
| `qwen3-4b` | `qwen3:4b` (Qwen3 4B Thinking 2507, Q4_K_M) | `3e4cb14174460404e7a233e531675303b2fbf7749c02f91864fe311ab6344e4f` | 2,497,280,480 | 5,933,099 |
| `minicpm-v4.6` | `minicpm-v4.6:latest`, language model (qwen35, Q4_K_M) | `6b0c74962c44bc6bf4b655b9b02c13eda9d5a0491543ae976d1ac18e4b7892e2` | 529,101,504 | 10,936,740 |
| `minicpm-v4.6-projector` | `minicpm-v4.6:latest`, vision encoder (clip, F16) | `ca931d861d0801d9003e50697cd764721a334107c0e0415a51168ee1938462de` | 1,108,746,944 | 1,152 |

What each one is for:

- `llama3.2-1b` — llama.cpp writer order: `general.file_type` before the
  tokenizer, `general.quantization_version` after it. States
  `attention.key_length`.
- `qwen3-4b` — Ollama's writer (keys sorted). ARCHITECTURE.md D-20's case:
  `key_length` 128 against `embedding_length / head_count` = 80.
- `minicpm-v4.6` — a hybrid architecture (`full_attention_interval` 4, SSM
  keys) whose writer put `general.file_type` after the tokenizer, so a parse
  that stops at the tokenizer must read on.
- `minicpm-v4.6-projector` — the separate vision encoder (`clip`,
  `general.type = mmproj`) a multimodal model ships beside its weights.

To add one: take the first N bytes of a model file where N covers the
header (`Header.BytesRead` from a full parse, rounded up), gzip it, add a
row here and a case to `realCases` in `gguf_test.go`. Never commit a whole
model file.
