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

## Status

Step 1 of the build plan: the architecture record, the repo skeleton and
three-OS CI. The daemon starts, answers `GET /api/health`, and serves a UI
shell whose screens are placeholders. No hardware detection, no Ollama calls,
no catalogue content yet — those are steps 2, 3 and 4.

## Run it

Needs Go (the version in `go.mod`) and Node 22+.

```sh
make dev      # daemon + Vite dev server; open the address Vite prints
make test     # everything CI runs
make build    # dist/advisor-<os>-<arch> for all four targets, UI embedded
```

`CLAUDE.md` has the rest.
