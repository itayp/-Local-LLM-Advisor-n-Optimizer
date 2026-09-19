# Local LLM Advisor & Optimizer

A desktop application for people who want to run AI models on their own
computer and do not want to learn VRAM arithmetic, quantization names, or
which runtime to pick. It looks at the machine, finds what is installed,
recommends models and settings that will actually fit, tests them, and says
when something new is worth trying. Ubuntu, macOS and Windows 11.

It is a local daemon (one Go binary, no runtime to install) that serves its
UI at `http://127.0.0.1:27182` and drives Ollama. It does not chat; the user
chats in the app they already use.

| Document | What it is |
|---|---|
| `PRD — Local LLM Advisor & Optimizer.md` | what and why |
| `BUILD_PLAN.md` | the fourteen steps, the decisions, the product rules |
| `ARCHITECTURE.md` | the decisions restated with their consequences (an ADR) |
| `CLAUDE.md` | the product rules and the repo conventions — read first |
| `scripts/probe0/README.md` | step 0: the estimator experiment and its result |
| `scripts/calibrate/README.md` | the dev-side instrument that measures the speed model's constants on a fleet machine |

## Status

Step 6 of the build plan: the benchmark harness. The daemon reads the
machine, finds Ollama and what it has installed, keeps the curated model list
with each file's header metadata, estimates how much memory a model needs
here and how fast it should run, and recommends up to three models that fit.
Now it also measures: the Benchmarks screen (and `POST /api/bench`) runs a
fixed suite of the advisor's own text through an installed model, timed with
Ollama's own counters while the machine's graphics memory, load, temperature
and power are sampled once a second; every run is stored with everything
that makes it comparable, and a measurement replaces the estimate of its
configuration and narrows the estimates of similar ones. First-run
onboarding is step 7.

## Run it

Needs Go (the version in `go.mod`) and Node 22+.

```sh
make dev      # daemon + Vite dev server; open the address Vite prints
make test     # everything CI runs
make build    # dist/advisor-<os>-<arch> for all four targets, UI embedded
```

`CLAUDE.md` has the rest.
