// One-line explainers for the technical terms the UI ever shows bare
// (CLAUDE.md: "No term from {VRAM, quantization, GGUF, KV cache, context
// window, tokens/sec, offload} without its explainer"). Written once here
// and reused everywhere through <Term> (components/Term.tsx) — a screen
// never writes its own version of one of these sentences.
//
// Not every term is used by every screen; a screen that never shows a
// concept in plain language does not need to import it. Advanced-only
// technical tables (Recommend, Benchmarks) carry their own row-by-row
// explain column instead of this component — the explanation is right
// next to the term either way, just laid out differently for a table.

export type GlossaryTermId = 'vram' | 'quantization' | 'gguf' | 'kv_cache' | 'context_window' | 'tokens_per_sec' | 'offload'

export interface GlossaryEntry {
  /** How the term reads inline, in the app's own usage. */
  term: string
  /** The one-line explainer, in plain words. */
  explain: string
}

export const glossary: Record<GlossaryTermId, GlossaryEntry> = {
  vram: {
    term: 'VRAM',
    explain: "A graphics card's own memory — the fastest place for a model to live, and usually the smallest.",
  },
  quantization: {
    term: 'quantization',
    explain: "How much a model's numbers were compressed to make the file smaller and faster to run, at some cost to precision.",
  },
  gguf: {
    term: 'GGUF',
    explain: 'The file format most local models ship in — the whole model in one file.',
  },
  kv_cache: {
    term: 'KV cache',
    explain: 'The memory that holds the conversation so far; it grows as the conversation gets longer.',
  },
  context_window: {
    term: 'context window',
    explain: 'How much text — yours and its replies together — a model can keep in mind at once.',
  },
  tokens_per_sec: {
    term: 'tok/s',
    explain: 'How fast a model reads or writes, in tokens per second; a token is about three quarters of a word.',
  },
  offload: {
    term: 'offload',
    explain: "Running part of a model on the processor instead of the graphics card, when it doesn't fully fit there — usually slower.",
  },
} as const
