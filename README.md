# Local LLM Advisor & Optimizer

[![CI](https://github.com/itayp/-Local-LLM-Advisor-n-Optimizer/actions/workflows/ci.yml/badge.svg)](https://github.com/itayp/-Local-LLM-Advisor-n-Optimizer/actions/workflows/ci.yml)

*Point it at your computer. It tells you which local AI models will actually run well on it — and proves it, instead of guessing.*

---

## Why this exists

If you look on YouTube or X and search for AI, you'll find more and more people talking about local AI. It's also been coming up a lot in a tech group I'm part of. I agree it's an interesting direction — but there are real problems with it:

- **Hardware** — most people have no idea whether their computer is actually able to run local AI.
- **Setup** — even people who do know their way around tend to get lost among the programs and models on offer.
- **Understanding** — once it's set up, how do you know if your results are actually good? How many tokens/sec counts as good, or as better than a cloud service?

All of that (and more) is what led me to build this tool: to give everyday people who want to get started some clear guidance, and help them find what actually fits their needs.

Side note: I'm not a developer. I know a bit of Ubuntu, and I only started running models when I got a Mac Pro from work to play with. The entire codebase here is "vibe coded" with Claude (Fable, Opus, and Sonnet).

---

## Who this is for

This is built for someone who's heard "you can run AI models on your own computer" and wants to actually try it — not for someone who already knows what a GGUF is.

That's probably you if:

- You own a laptop or a gaming PC and want to know what it can actually handle, not what a spec sheet claims.
- You've installed Ollama, pulled a model, and had no real idea whether you picked the right one, or the right size.
- You keep hearing about new models and don't know which ones are worth your bandwidth.
- You don't have a graphics card at all, and would rather hear an honest "small models only, and here's roughly how fast" than a wall of jargon or a failure screen.

It's *not* built for someone who already hand-tunes GGUF quantizations, runs a fleet of inference servers, or wants to fine-tune a model — those are real workflows, just not this one.

(One honest disclosure: the machines this is built and tested on belong to the person writing it. They're the test fleet, not the audience. The product is aimed at people who will never open a terminal to use it.)

## What it does — and what it doesn't

**It does:**

- **Read your actual machine.** Your graphics card and how much memory it has, your CPU, your operating system, and which path Ollama really takes to use them — some AMD and Intel cards quietly fall back to the CPU without saying so; this says so.
- **Show you what's already installed**, and how it stacks up against what it would recommend instead.
- **Recommend models that will fit** — from a curated, vetted list of model families, not the entire internet — and say *why*, in plain language ("fits your graphics card with room for long documents"), and what it costs ("a 5 GB download").
- **Test its own advice.** It can run a short, real benchmark on your machine and replace its estimate with a measurement. The two are never shown as if they're the same thing.
- **Let you know when something new is worth trying**, instead of making you go looking for it yourself.
- **Explain itself.** Every technical term it shows has a plain-language explanation a tap away; the jargon lives behind an "Advanced" toggle that's off unless you ask for it.

**It doesn't:**

- **Chat with you.** You keep using whatever app you already chat in — Ollama's own app, Open WebUI, LM Studio, or anything else. This is the layer that helps you pick and tune what runs underneath it, not another chat window.
- **Replace Ollama, llama.cpp, or LM Studio.** It drives Ollama today (more runtimes are on the roadmap); it isn't trying to reinvent any of them.
- **Change anything on your machine without you clicking a button that says exactly what it's about to do** — "Download 5 GB," "Start Ollama," "Run a two-minute test." Nothing ever switches models automatically.
- **Send anything off your machine**, beyond looking up model information from Hugging Face when you ask it to check for models. No prompt, no file, and no usage data ever leaves your computer — the app only listens on `127.0.0.1`, never your network.
- **Treat weak hardware as a failure.** A laptop with no graphics card gets an honest, useful answer, not an error screen.

## What it's like to use

*(Screenshots coming soon. This section describes the full, intended experience; see [Status](#status) for exactly how much of it is built right now.)*

The first run is a short, guided walk: it reads your computer, checks whether Ollama is installed — and installs it for you with one click if it isn't — asks what you actually want local AI for, shows you what it'd recommend, gets the model, and lets you try it. No terminal, at any point.

> **Screenshot coming soon** — the welcome screen

> **Screenshot coming soon** — "Checking your computer," the hardware read-out

> **Screenshot coming soon** — "What will you use AI for?"

After that first run, three screens carry the ongoing relationship:

- **Recommend** — up to three model cards for whatever you're doing (coding, chat, long documents…), each with a plain-language reason, what it costs to download, and a speed range that's clearly marked as an estimate until it's actually been measured on your machine.

  > **Screenshot coming soon** — the Recommend screen, model cards

- **Benchmarks** — runs the advisor's own short test on a model of your choosing, timed for real, while your graphics memory, load, temperature and power are sampled as it runs, with a history so you can see what actually changed between runs.

  > **Screenshot coming soon** — a benchmark result

- **New models** — quietly watches for anything newly worth trying and tells you when it thinks something is. It never installs anything on its own and never switches you to it.

Wherever a number appears — a memory estimate, a speed range, a benchmark result — it carries a visible tag: *estimated* or *measured*. That distinction is never blurred. A measurement replaces an estimate the moment one exists; until then, the app says plainly that it's a guess.

## How it works, technically

It's a single Go binary — nothing separate to install — that starts a small local web server and opens your browser to it. The interface is an ordinary React + TypeScript app, built once and embedded directly into that binary, so there's exactly one file per operating system (Linux, macOS, Windows) and nothing else to download to run it.

Everything it remembers lives in one SQLite file on your machine (pure Go, no native dependencies to compile). It reads your hardware through per-OS probes, talks to Ollama over its local API to see what's installed and running, and matches that against a curated catalogue of model families whose metadata — including a lightweight read of each model file's own header — comes from Hugging Face.

From there:

- The **fit estimator** does the memory arithmetic (model weights + KV cache + overhead) that decides whether a model fits your machine, and how — pure calculation, tuned against real measurements, never a guess dressed up as one.
- The **recommendation engine** applies rules over that data to choose and explain up to three candidates. No model is ever asked to make that call — this app doesn't use AI to do its own job.
- The **benchmark harness** runs a fixed, versioned suite of prompts through an installed model, timing it with Ollama's own counters while sampling your machine's resources once a second, and stores everything needed to make one run genuinely comparable to the next.

The server binds to `127.0.0.1` only — that isn't a setting, it's compiled in, and every request is checked to make sure of it. The only traffic that ever leaves your machine is a lookup to Hugging Face for model metadata; nothing you type, and nothing about how you use it, goes anywhere else.

For the full set of decisions and why they were made, see `ARCHITECTURE.md`. For the product rules and repo conventions, see `CLAUDE.md`.

## Status

Currently through **step 7 of 14** in the build plan: hardware detection, Ollama integration, the curated model catalogue, the fit estimator and recommendation engine, the benchmark harness, and first-run onboarding are all in place and gated. Next up: filling out the remaining working screens.

## Documentation map

| Document | What it is |
|---|---|
| `PRD — Local LLM Advisor & Optimizer.md` | what and why |
| `BUILD_PLAN.md` | the build steps, the decisions, the product rules |
| `ARCHITECTURE.md` | the decisions restated with their consequences (an ADR) |
| `CLAUDE.md` | the product rules and repo conventions — read first if you're contributing |
| `scripts/probe0/README.md` | the estimator experiment and its result |
| `scripts/calibrate/README.md` | the dev-side tool that measures the speed model's constants on real hardware |

## Running it

Needs Go (the version in `go.mod`) and Node 22+.

```sh
make dev      # daemon + Vite dev server; open the address Vite prints
make test     # everything CI runs
make build    # dist/advisor-<os>-<arch> for all four targets, UI embedded
```

`CLAUDE.md` has the rest.
