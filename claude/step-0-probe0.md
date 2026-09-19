# Step 0 — probe0: does the estimator hold?

**Status: GATE PASSED on all three machines, after three corrections to the
formula. Step 0 is done. Step 1 may start.**

Round 3, 2026-09-18, probe0 build `2026-09-18.5`. 28 dense rows across three
machines and three runtime backends.

## Verdict

| | dense rows | within 15% | verdict |
|---|---|---|---|
| windows_NVIDIA (RTX 5070 Ti, **cuda**) | 12 | 12 | **PASS** |
| macOS (M1 Pro, **metal**) | 8 | 7 | **PASS** |
| MacProAMD (2× D700, **vulkan**) | 8 | 8 | **PASS** |
| pooled | 28 | 27 (96%) | mean \|err\| 5.5%, median 3.9% |

The build plan's original formula scored 82% pooled and **failed on the AMD
machine** (6/8). The corrections below took it to 96% and a pass everywhere.

**Worst row:** `llama3.2:3b` @ 4096 on macOS, −20.8% (predicted 2.32 GB,
measured 2.93 GB). This is the measurement, not the formula: the Mac is a
working laptop and its wired-memory baseline moves between samples, and on the
other two backends the same model at the same context lands within 8%.

## The formula that passed

```
predicted = weights_bytes + KV + overhead
KV        = 2 * block_count * head_count_kv * head_dim * ctx * 2 B   (f16)
head_dim  = attention.key_length where the model states one, else embedding_length / head_count
ctx       = min(num_ctx, the model's trained context_length)
overhead  = cuda 250 MiB | metal 0 MiB | vulkan 50 MiB | rocm 50 MiB | cpu 0 MiB
```

### The three corrections, each earned

**1. `head_dim` comes from `attention.key_length`.** `embedding_length /
head_count` is not the head dimension — it merely usually equals it.
`qwen3:4b` states `key_length = 128` against a computed 80, so its KV cache is
1.6× the plan's prediction; the 32k rows under-shot by 18%, 25% and 30% on the
three machines. Using the stated value fixes all three. **This is the single
most important finding for step 4's GGUF parser and step 5's estimator.**

**2. `num_ctx` is clamped to the model's trained `context_length`,** because
Ollama clamps it. `phi4:14b` is trained to 16384; asking for 32768 produced a
+29% error against a cache Ollama never allocated. Ollama reports the clamped
value in `/api/ps` `context_length`.

**3. The overhead is a property of the runtime backend, not the model's width.**
Fitted against `n_batch * embedding_length * 4 B`, the slope comes out
*negative* — the graph term the plan hypothesised is not there. What is there is
a per-backend constant:

| backend | median implied overhead | n |
|---|---|---|
| cuda | **+249 MiB** | 12 |
| vulkan | +80 MiB | 8 |
| metal | −49 MiB | 8 |

CUDA's ~250 MiB is the CUDA context, which Ollama's own scheduler does not
count. Metal's is near zero. A recommendation built on `/api/ps size` alone
therefore under-predicts by ~250 MiB on every NVIDIA machine.

Reproduce either formula from the reports already in hand, without touching the
machines:

```sh
probe0 -refit a.json,b.json,c.json -recompute
probe0 -refit a.json,b.json,c.json -recompute -head-dim spec -clamp-ctx=false -overhead-model fixed
```

## Runtime path per machine

| machine | path | note |
|---|---|---|
| windows_NVIDIA | **cuda** | RTX 5070 Ti, 15.9 GB, driver 610.62 |
| macOS | **metal** | M1 Pro, 16 GB unified; `iogpu.wired_limit_mb` unset |
| MacProAMD | **vulkan** | 2× Tahiti XT (D700) 6 GB each under `amdgpu` |

**The Mac Pro drives the D700s on Vulkan** — BUILD_PLAN's open question is
answered; it is the AMD test box, not the CPU-only tier. Ollama uses **one** card
(`id=0 available="5.5 GiB"`), so the 12.9 GB total is not a pool. probe0 now
reports the largest single device as the budget and the sum separately.

## Findings step 4 / step 5 inherit

1. **`/api/ps size` is Ollama's estimate, not a measurement.** `minicpm-v4.6` at
   4096 reported 673,972,222 B on Metal and 674,590,882 B on Vulkan — 0.09%
   apart across different backends. Score against a device-memory delta.
2. **Blob size ≠ resident weights.** `gemma4:e4b` blob 9.61 GB, ~4.7 GB reached
   the card. Vision towers, projectors and elastic/MatFormer parameters live in
   the blob without becoming resident. For dense text models the blob is sound.
3. **`ioreg "In use system memory"` does not track the model on Apple Silicon.**
   Replaced by `vm_stat` "Pages wired down"; `ioreg` is still recorded for
   comparison. Even wired memory is noisy on a machine in use — the Mac is the
   only machine with a row outside 15%.
4. **Vulkan's sysfs VRAM counter under-reads for some models** — `llama3.2:1b`
   read ~190 MB below Ollama's figure at both contexts, `llama3.1:8b` 391 MB low
   at 4096. Elsewhere it agrees within ~30 MB. Part of the model may sit in GTT
   rather than VRAM.
5. **Sliding-window architectures still over-predict** (gemma4, phi4 at 4096).
   Excluded from the gate as vision, or inside it and passing, but step 5 should
   read `attention.sliding_window` and cap the KV term.

## probe0 as it stands

`go.mod` (`module advisor`, `go 1.27.1`), `cmd/probe0/main.go`, `README.md` in
`/Users/itay.pollak/Local LLM Advisor n Optimizer/`. Moves to `scripts/probe0/`
in step 1, unchanged. Build stamp `2026-09-18.5`; `probe0 -version` prints it,
and every report records it in `build` — added because two rounds were run with
stale binaries before anyone noticed.

Other capabilities worth keeping: `-pull` / `-pull-only` fetch the dense
text-only test set sized to the machine; `measurements` records every available
device-memory reading per row with the scoring one flagged; `INCONCLUSIVE` below
5 dense rows and a per-machine verdict breakdown; virtual display adapters
filtered out.

## Decisions taken while writing probe0

- `/api/show` is POST in Ollama, not GET as the plan's prompt says.
- `model_info` keys are architecture-prefixed; some architectures give per-layer
  arrays for head counts — the maximum is used and the row is annotated.
- Windows VRAM comes from registry `HardwareInformation.qwMemorySize`, never
  `Win32_VideoController.AdapterRAM` (32-bit, wraps at 4 GB).
- MoE, vision and embedding models are measured and printed but excluded from
  the averages and the gate.
