# calibrate — measuring the speed model on the test fleet

This is the dev-side instrument of build-plan step 5. **It is never something
the customer installs**, and the daemon never runs it. It exists because the
advisor's speed estimate rests on a handful of constants, and a constant
nobody measured on a machine we own is a borrowed one.

## What is being measured

The estimate (`internal/estimate/speed.go`) is

```
generation tok/s ≈ memory bandwidth ÷ bytes read per token × efficiency
prompt tok/s     ≈ the same part's generation speed on the reference model,
                   scaled to this model's parameters, × a prompt ratio
```

`efficiency` and the `prompt ratio` are ranges keyed by the runtime path
(cuda, metal, rocm, vulkan, cpu) in `internal/estimate/config.go`. As shipped
they come from llama.cpp's public llama-bench scoreboards; each entry's
`Basis` says which. This tool turns a llama-bench run on **one of Itay's
machines** into the same two numbers, says whether they fall inside the
configured range, and writes a results file to commit next to step 0's.

Three runs matter most, because nothing public covers them:

| Machine | Run | Why |
|---|---|---|
| Windows PC (RTX 5070 Ti) | GPU, then `-ngl 0` | cuda on the fleet; and the Ryzen 7 5800X3D is the **AVX2 processor** calibration |
| Mac Pro (Ubuntu, 2× D700) | GPU (Vulkan), then `-ngl 0` | an old AMD card on Vulkan; and the Xeon E5 v2 is the **no-AVX2** run that checks `NoAVX2Factor` |
| MacBook Pro (M1 Pro) | GPU | metal on the fleet |

The processor range (`cpu`) is the only one that is **CHOSEN rather than
measured** today — those two `-ngl 0` runs are what replace it.

## 1. Get llama-bench

llama-bench ships with llama.cpp. Download the release build for the machine
from <https://github.com/ggml-org/llama.cpp/releases> and unpack it anywhere:

| Machine | Archive to take |
|---|---|
| Windows + NVIDIA | `llama-…-bin-win-cuda-…-x64.zip` and the matching `cudart-…` zip, unpacked into the same folder |
| Ubuntu + AMD (the Mac Pro) | `llama-…-bin-ubuntu-vulkan-x64.zip` |
| macOS, Apple Silicon | `llama-…-bin-macos-arm64.zip` |

On the Mac, the first run needs `xattr -dr com.apple.quarantine <folder>`
because the binaries are not notarised.

## 2. Pick model files already on the machine

Ollama's models are ordinary GGUF files; nothing needs downloading. Ask
Ollama where a model's weights are:

```sh
ollama show --modelfile llama3.1:8b | grep ^FROM
# FROM /Users/…/.ollama/models/blobs/sha256-667b0c1932bc…
```

Use **dense, text-only** models for the path constants — the ones step 0
measured are ideal (`llama3.2:3b`, `llama3.1:8b`, and `qwen3:14b` or
`phi4:14b` on the 16 GB card). One small and one large model per machine is
enough. A mixture-of-experts model (`gpt-oss:20b`) is a separate run that
checks `MoEGeneration`; pass its active share (see step 4).

## 3. Run it

Stop Ollama first, or unload its models (`ollama stop <model>`), so that the
two do not share the card. Then, from the folder llama-bench was unpacked in:

```sh
# the graphics path (cuda / metal / vulkan, whichever the build is)
./llama-bench -m /path/to/blob -p 512 -n 128 -ngl 99 -r 5 -o json > gpu-llama3.1-8b.json

# the processor: the same command with no layers on the graphics
./llama-bench -m /path/to/blob -p 512 -n 128 -ngl 0 -r 5 -o json > cpu-llama3.1-8b.json
```

On Windows it is `llama-bench.exe` and `>` works the same in PowerShell 7 and
cmd. `-p 512 -n 128` are the tests llama.cpp's scoreboards use, which keeps
the numbers comparable with the public ones in `config.go`.

## 4. Feed the numbers back

Copy the JSON files to the repo (any machine with Go; the tool needs no
network) and run, from the repo root:

```sh
go run ./scripts/calibrate -label windows-5070ti gpu-llama3.1-8b.json cpu-llama3.1-8b.json
```

It prints, per model and path, the measured efficiency and prompt ratio
beside the configured range, with a verdict, and writes
`scripts/calibrate/results/<label>-<date>.json`.

Flags that matter:

- `-bandwidth 51.2` — the memory bandwidth to score against, in GB/s. Needed
  when the part is not in `data/hardware/gpus.yaml` (then add the row too),
  and **worth passing for every processor run**: the table only knows the
  range a processor family supports, and you know what is in the machine.
  Channels × MT/s × 8 ÷ 1000 — two channels of DDR4-3200 is 51.2; the Mac
  Pro's four channels of DDR3-1866 is 59.7.
- `-active-share 0.17` — for a mixture-of-experts model, active ÷ total
  parameters (gpt-oss 20B: 3.6 ÷ 21).
- `-path vulkan` — only when llama-bench's `backends` field does not settle it.

## 5. Act on the verdict

- **Inside the configured range** — commit the results file, and add the
  machine to the `Basis` of that path in `config.go` ("MEASURED (fleet): …").
  A range backed by our own machine is worth more than one backed only by a
  scoreboard.
- **BELOW the range** — the advisor is over-promising on that part. Either
  the path's `Efficiency.Low` comes down (if the part is typical of the
  path), or the part gets an `efficiency:` override with its
  `efficiency_source:` in `gpus.yaml` (if it is an outlier, like the Ultra
  chips and the Radeon VII already are). Say which in the commit.
- **ABOVE the range** — same, upwards. Under-promising is the cheaper
  mistake, but the range is meant to contain the truth.

Then run `go test ./internal/estimate/` — the tests that pin public
measurements inside the ranges will tell you if a change broke one.

## What this does not measure

llama-bench measures **llama.cpp**. Customers run **Ollama**, which embeds
the same engine but schedules and batches for itself, and for some model
families uses its own runner. The build-plan's step 6 benchmark harness
measures Ollama itself, on the customer's machine, and its result replaces
the estimate outright (product rule 4). This tool keeps the *estimate*
honest for the machines that have not run a benchmark yet. When a fleet
machine has both numbers, compare `ollama run --verbose <model>`'s "eval
rate" with llama-bench's `tg128`: a consistent gap is a constant this model
does not have yet, and belongs in ARCHITECTURE.md's open items.

Memory is not this tool's business: `scripts/probe0` measures that, and its
reports in `scripts/probe0/results/` are what `internal/estimate`'s tests
replay.
