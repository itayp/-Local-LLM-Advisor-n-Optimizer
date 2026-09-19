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

Step 5 of the build plan: the fit estimator and the recommendation engine.
The daemon reads the machine, finds Ollama and what it has installed, keeps
the curated model list with each file's header metadata, and now answers the
product's central question: `GET /api/models/{id}/fit` says how much memory a
model needs here, where it would live and how fast it should run — a range,
labelled as an estimate — and `GET /api/recommend?purposes=…` returns up to
three models that fit, each with its reasons in plain words, what it costs to
download, what it changes against the model you have, and how confident the
advisor is. The Recommend screen shows it. Benchmarks that replace the
estimates with measurements are step 6.

## Run it

Needs Go (the version in `go.mod`) and Node 22+.

```sh
make dev      # daemon + Vite dev server; open the address Vite prints
make test     # everything CI runs
make build    # dist/advisor-<os>-<arch> for all four targets, UI embedded
```

`CLAUDE.md` has the rest.
