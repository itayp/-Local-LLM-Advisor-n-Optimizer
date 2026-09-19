# Ollama server-log fixtures

Each file is one stretch of Ollama's `server.log` around a model load, as
`parseLoadReport` and `pathFromLog` read it. None was captured from a fleet
machine: every line is shaped from the format strings in the source of the
builds Ollama v0.34.2 ships — Ollama's `llm/llama_server.go` ("starting
llama-server", the llama-server flags it passes, `--log-verbosity 4
--no-log-prefix --no-log-timestamps`) and llama.cpp b10969
(`src/llama-model.cpp` "offloaded %d/%d layers to GPU" and "%12s model
buffer size = %8.2f MiB", `src/llama-context.cpp` "flash_attn = %s" and
resolve_fused_ops' "%s enabled", `src/llama-kv-cache.cpp` "size = … K (%s):
…, V (%s): …", `ggml/src/ggml-backend-reg.cpp` "loaded %s backend from %s").
Sizes and paths are chosen to match the fleet's machines. Replace a file with
a captured log when one is at hand (keep a note here).

- `windows-cuda-load.log` — the RTX 5070 Ti PC: backends loaded as DLLs,
  CPU last; full offload; flash attention auto → enabled; f16 cache.
- `darwin-metal-load.log` — the M1 Pro: Metal built in (no load_backend
  lines), MTL0_Mapped buffers; q8_0 cache set through OLLAMA_KV_CACHE_TYPE.
- `linux-vulkan-split.log` — the Mac Pro's D700 on Vulkan with a model too
  large for 6 GB: 20 of 33 layers offloaded, flash attention not supported.
- `two-loads.log` — a load of another model, then ours: the report is the
  last load's.
