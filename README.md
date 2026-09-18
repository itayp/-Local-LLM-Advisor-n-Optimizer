# probe0 — step 0 of the Local LLM Advisor & Optimizer

One throwaway program. It exists to answer one question before anything else is
built: **can memory use be predicted from model metadata accurately enough that
"this will fit your machine" is a claim and not a guess?**

It does two things and prints a report.

1. **Labels the machine** — OS and version, CPU model and cores, RAM, and per
   GPU the vendor, name and VRAM. Anything that cannot be read is printed as
   `unknown`. Nothing is inferred from a model name or a rule of thumb.
2. **Runs the estimator experiment** — for every model Ollama has installed, at
   `num_ctx` 4096 and 32768: predict total memory, load the model, read what
   Ollama reports, read the real VRAM delta from the vendor tool, and print the
   error.

No UI, no database, no other packages, no dependencies outside the Go standard
library. It makes no network request other than to the local Ollama. It moves to
`scripts/probe0/` in step 1 — it is not deleted.

---

## Requirements

- **Go 1.27.1** (the current stable release). `go.mod` declares it; a Go 1.21+
  toolchain will fetch it automatically on a machine with internet access.
  The source itself uses nothing newer than Go 1.21, so if a machine is pinned
  to an older toolchain, lowering the `go` line in `go.mod` also builds.
- **Ollama running**, with some models pulled. `ollama list` should show them.
- Optional, and only for the "actual VRAM" column:
  - NVIDIA: `nvidia-smi` (ships with the driver).
  - AMD on Linux: nothing — the sysfs file is read directly; `rocm-smi` or
    `amd-smi` are used only if sysfs is unavailable.
  - Apple Silicon: nothing — `vm_stat` and `ioreg` are built in.

## Build, and cross-compile for all three test machines

Run these on **one** machine — the Mac. Go cross-compiles without a toolchain
per target, and this program has no cgo, so the binaries are self-contained.

```sh
cd "/Users/itay.pollak/Local LLM Advisor n Optimizer"
go version          # needs go1.27.1 or newer
rm -rf dist         # so a failed build cannot leave a stale binary behind

CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -o dist/probe0-darwin-arm64      ./cmd/probe0
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -o dist/probe0-linux-amd64       ./cmd/probe0
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o dist/probe0-windows-amd64.exe ./cmd/probe0
```

Check them — `-version` prints a build stamp, and a stale binary will print an
older one (or fail on `-version` entirely, which is itself the answer):

```sh
./dist/probe0-darwin-arm64 -version
```

It must print `2026-09-18.4` or later. **Delete `dist/` before rebuilding**:
`go build -o dist/...` leaves the existing file untouched when the build fails,
so a failed build gives you no new binary and a perfectly working old one. Every
report also records the stamp in its header and in `build` in the JSON, so a run
made with the wrong binary says so.

## Before running: what has to be installed

The gate is about **dense text models**. MoE, vision and embedding models are
measured but excluded, so a machine that has only those produces a report with
nothing in it — which is exactly what happened on the first round, on all three
machines. Pull a spread of dense, text-only models first, chosen so
`embedding_length` varies; otherwise the overhead term cannot be fitted either.

probe0 knows the set and will fetch what is missing. Same command on every OS:

```sh
./probe0 -pull-only          # download the test set and stop
./probe0 -pull -label mybox  # download anything missing, then run
```

It prints the whole list and the total size before downloading anything, skips
what is already installed, and never removes a model. The set is:

| model | embedding_length | ~size |
|---|---|---|
| `llama3.2:1b` | 2048 | 1.3 GB |
| `qwen3:4b` | 2560 | 2.6 GB |
| `llama3.2:3b` | 3072 | 2.0 GB |
| `llama3.1:8b` | 4096 | 4.9 GB |
| `qwen3:14b` | 5120 | 9.3 GB — only on a device ≥ 12 GB |
| `phi4:14b` | 5120 | 9.1 GB — only on a device ≥ 12 GB |

The two wide models are pulled only where a device can actually hold them at 32k
context; elsewhere they would produce nothing but CPU-split rows, and probe0
prints the reason it skipped them. The threshold is `-big-device-gib`. On unified
memory the budget is taken as 60% of RAM when `iogpu.wired_limit_mb` is unset,
since the GPU never gets all of it.

Every model in the set is dense and text-only on purpose, and the widths are
spread so the fixed and graph parts of the overhead term can be told apart — four
models all 5120 wide would fit any straight line. If a tag has been superseded,
`-pull` says which one failed; pass replacements with
`-pull-models a:1b,b:4b,...`, which replaces the built-in set entirely.

Doing it by hand is `ollama pull <name>` for each row above. Either way: avoid
gemma3 4b and above and gemma4 (vision towers), and anything tagged `-moe`,
`a3b` or `gpt-oss`. On a 6 GB card `llama3.1:8b` will not fit and Ollama will
split it to the CPU — that is useful, not a problem, it exercises the split path.

## Run it

### This Mac (Apple Silicon)

```sh
cd "/Users/itay.pollak/Local LLM Advisor n Optimizer"
./dist/probe0-darwin-arm64 -label mac-m-series | tee probe0-mac.txt
```

### The Ubuntu Mac Pro (AMD FirePro D700)

Copy `dist/probe0-linux-amd64` over, then:

```sh
chmod +x probe0-linux-amd64
./probe0-linux-amd64 -label macpro-ubuntu-d700 | tee probe0-macpro.txt
```

This is the machine BUILD_PLAN wants tried first: if Ollama's Vulkan backend
drives the D700s, the `PATH` column says `vulkan` and it becomes the AMD test
box. If it says `cpu`, that is the correct answer for those cards, not a bug.

### The Windows PC with the NVIDIA card

Copy `dist/probe0-windows-amd64.exe` over, then in PowerShell:

```powershell
Unblock-File .\probe0-windows-amd64.exe
.\probe0-windows-amd64.exe -label win-nvidia | Tee-Object -FilePath probe0-win.txt
```

Each run also writes `probe0-<label>-<timestamp>.json` next to itself. **Send
back both files from each machine** — the text is for reading, the JSON is what
step 5 gets calibrated against.

### Combining the three

```sh
./dist/probe0-darwin-arm64 -refit probe0-mac-*.json,probe0-macpro-*.json,probe0-win-*.json
```

## What it costs to run

One model load per row, and a row per (model, context). Twelve installed models
is 24 loads. Large models take a minute or two each to load from disk the first
time, so budget 10–40 minutes and don't use the machine for anything heavy while
it runs. Every model is unloaded between rows (`keep_alive: 0`) so the
measurements do not contaminate each other, and everything is unloaded at the
end.

---

## The prediction

```
predicted = weights_bytes
          + KV cache
          + overhead

KV cache  = 2 * block_count * head_count_kv * (embedding_length / head_count)
            * num_ctx * 2 bytes            (2 for K and V, 2 bytes for an f16 cache)
```

`weights_bytes` is the blob size from `/api/tags`; every other term comes from
`model_info` in `/api/show`.

### What round 3 changed, and why

The build plan's starting formula scored 82% pooled but **failed on the AMD
machine**. Three corrections, each measured against 28 dense rows on CUDA, Metal
and Vulkan, took it to 96% pooled and a pass on all three:

**1. `head_dim` comes from `attention.key_length` where a model states one.**
`embedding_length / head_count` is not the head dimension; it merely usually
equals it. `qwen3:4b` states `key_length = 128` against a computed 80, so its KV
cache is 1.6× what the plan's formula predicted, and the 32k rows under-shot by
18–30% on every machine. Using the stated value fixes all three.
`-head-dim spec` restores the old behaviour.

**2. `num_ctx` is clamped to the model's trained `context_length`,** because
Ollama clamps it. Asking `phi4:14b` (trained to 16384) for 32768 produced a
+29% error against a cache Ollama never allocated. `-clamp-ctx=false` restores
the old behaviour.

**3. The overhead is a property of the runtime backend, not the model's width.**

```
overhead = cuda 250 MiB, metal 0 MiB, vulkan 50 MiB, rocm 50 MiB, cpu 0 MiB
```

The original guess — a fixed term plus one scaling with `embedding_length` —
does not survive the data: fitted against width the slope comes out *negative*.
What is there instead is a per-backend constant. CUDA's ~250 MiB is the CUDA
context, which Ollama's own scheduler does not count; Metal's is near zero;
Vulkan's is small. `-overhead-model fixed` restores the two-term model, whose
parts remain tunable with `-overhead-fixed-mib` and `-overhead-graph-factor`.

Every row still prints its **implied overhead** (`measured − weights − KV`) and
the report still fits it, so the numbers above stay checkable as more machines
report.

### Checking a formula change without re-running the machines

`-recompute` re-derives every prediction from the data the reports already
carry, so a change can be scored against reports in hand:

```sh
./probe0 -refit a.json,b.json,c.json -recompute
./probe0 -refit a.json,b.json,c.json -recompute \
    -head-dim spec -clamp-ctx=false -overhead-model fixed   # the original formula
```

## What is measured against what

| column | where it comes from |
|---|---|
| `OLLAMA` | `/api/ps` `size` — Ollama's own view of the whole model |
| `VRAM` | `/api/ps` `size_vram` — the part of it on the GPU |
| `ACTUAL` | device memory after the load minus before, from `nvidia-smi`, AMD sysfs / `rocm-smi` / `amd-smi`, or `vm_stat` wired memory on Apple Silicon |
| `ERR%` | `(predicted − measured) / measured`, the scoring number |
| `ERRps%` | the same error taken against `OLLAMA`, for comparison only |

`measured` is `ACTUAL` when the whole model sat on the GPU, and `OLLAMA` when it
did not: once layers are split to the CPU, the VRAM delta describes only the GPU
part, and comparing a whole-model prediction against it would be meaningless.
The row says which was used.

**`/api/ps size` is Ollama's estimate, not a measurement.** The same model at the
same context reported 673,972,222 bytes on Metal and 674,590,882 on Vulkan —
0.09% apart across two completely different backends. Real allocations never
agree that closely. So `ERRps%` is context, never evidence, and the split-to-CPU
fallback above is a known weak spot rather than a clean measurement.

Every reading the machine can give is recorded per row under `measurements` in
the JSON, with the scoring one flagged `preferred`. When two readings disagree by
more than 2×, the row says so — at most one of them is measuring the model.

**Gate:** predicted within 15% of measured on at least four of every five rows,
on each machine.

## What is excluded, and why

`CLASS` printed in capitals means the row is excluded from the averages and from
the gate. It is still measured and still printed — the data is useful — it just
does not get to hide inside the average.

- **MOE** — mixture-of-experts (detected by `expert_count`, or an architecture
  named `…moe`, `mixtral`, `deepseek2`, `gptoss`, …). Only some experts are
  active per token but all of them are resident, and several use a compressed
  KV (MLA) the formula above does not describe.
- **VISION** — detected by the `vision` capability or a `projector_info` block.
  The vision tower and its projector are memory the text-model formula knows
  nothing about.
- **EMBEDDING** — not chat models; loaded through `/api/embed` instead of
  `/api/generate` so they still produce a row.
- **UNKNOWN** — `model_info` did not carry the fields the formula needs. The row
  says so instead of guessing.

A model whose **weights alone exceed the machine** (RAM, or RAM + VRAM on a
discrete-GPU box) is never loaded; the row says `SKIPPED` and gives both figures.

## Things that will make the KV term wrong, and are flagged when seen

- **A sliding window.** Architectures that declare
  `attention.sliding_window` (gemma3 and friends) cache far less than
  `num_ctx`, so the prediction will read high. The row is annotated.
- **`attention.key_length` ≠ `embedding_length / head_count`.** Some models
  state the head dimension explicitly and it differs. The formula uses
  `embedding_length / head_count` as specified; the row records both.
- **A quantized KV cache.** If the Ollama server runs with
  `OLLAMA_KV_CACHE_TYPE=q8_0` or `q4_0`, the cache is half or a quarter of the
  f16 size. The report prints the value it can see and warns.
- **Per-layer head counts.** A few architectures give arrays rather than a
  single number; the maximum is used and the row says so.

Each of these is a finding for step 5, not a defect to paper over.

## Which path Ollama actually took

`PATH` names the backend: `cuda`, `metal`, `rocm`, `vulkan` or `cpu`. It comes
from Ollama's own device-discovery lines, read from the server log
(`~/.ollama/logs/server.log`, `journalctl -u ollama`, or
`%LOCALAPPDATA%\Ollama\server.log`) — the lines appended during each load, so
the path is per row rather than per machine. When no line is found, it falls
back to inferring from `/api/ps` (`size_vram = 0` means CPU) and says the figure
was inferred.

## Flags

| flag | default | |
|---|---|---|
| `-host` | `$OLLAMA_HOST` or `http://127.0.0.1:11434` | |
| `-ctx` | `4096,32768` | contexts to test |
| `-models` | all | comma-separated substrings to narrow the run |
| `-label` | hostname | how this machine is named in the report |
| `-hardware-only` | | print section 1 and stop |
| `-out` / `-no-json` | auto | JSON report path |
| `-overhead-fixed-mib` | `208` | |
| `-overhead-graph-factor` | `12` | |
| `-batch` | `512` | `n_batch` the overhead term assumes |
| `-keep-alive` | `5m` | |
| `-settle` | `4s` | pause after a load or unload before sampling VRAM |
| `-timeout` | `20m` | per model load |
| `-refit` | | comma-separated JSON reports: print the combined summary and stop |
| `-pull` | | download any missing model from the test set, then run |
| `-pull-only` | | download any missing model from the test set, then stop |
| `-pull-models` | built-in set | comma-separated list replacing the test set |
| `-big-device-gib` | `12` | a device this large also gets the 5120-wide models |
| `-head-dim` | `keylen` | `keylen` uses `attention.key_length` where stated; `spec` uses `embedding_length / head_count` |
| `-clamp-ctx` | `true` | clamp `num_ctx` to the model's trained `context_length`, as Ollama does |
| `-overhead-model` | `path` | `path` for the per-backend term; `fixed` for the original two-term model |
| `-overhead-by-path` | `cuda=250,metal=0,vulkan=50,rocm=50,cpu=0` | per-backend overhead, MiB |
| `-recompute` | | with `-refit`: recompute predictions from stored row data under the current flags |
| `-version` | | print the build stamp and exit |

## Honest limits

- On Apple Silicon there is no VRAM to read. `ACTUAL` comes from the delta in
  `vm_stat`'s "Pages wired down": a Metal buffer is wired, and
  `iogpu.wired_limit_mb` caps exactly that, so wired memory is the closest thing
  the OS has to "VRAM in use". It is system-wide, so close other GPU work before
  running. `ioreg`'s "In use system memory" is still recorded for comparison but
  is **not** scored against — in the 2026-09-18 round it fell as context grew
  eightfold, so it does not track the model. The GPU budget line reports
  `iogpu.wired_limit_mb`; when it is 0 (the default) the effective limit is
  macOS's own and is reported as unknown rather than estimated.
- VRAM is never summed across devices for the "will it fit" budget. A model runs
  on one device unless the runtime splits it — the Mac Pro's two 6 GB D700s are
  not a 12 GB pool, and Ollama took one of them. The report gives the largest
  single device as the budget and the sum separately, clearly labelled.
- Remote-desktop and virtual display adapters are filtered out of the GPU list
  and named under `filtered_adapters`; they are display sinks, not GPUs.
- On Windows, VRAM comes from the registry value
  `HardwareInformation.qwMemorySize` under the display-class key, not from
  `Win32_VideoController.AdapterRAM`, which is a 32-bit field and wraps at 4 GB.
  There is no per-process VRAM delta for non-NVIDIA cards on Windows, so
  `ACTUAL` will read `unknown` there.
- The VRAM delta includes anything else that allocated on the card during the
  load. Close other GPU work before running.
# -Local-LLM-Advisor-n-Optimizer
