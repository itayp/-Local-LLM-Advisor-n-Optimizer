import type { PublicEntry } from '../api/types'

/** A public value as the daemon serves it (Go: catalog.PublicEntry), for tests. */
export function publicEntry(over: Partial<PublicEntry> = {}): PublicEntry {
  return {
    source_id: 'arena',
    source_name: 'Arena leaderboard dataset',
    metric: 'arena:text/overall',
    tests: "people's votes comparing answers to everyday questions",
    purposes: ['chat'],
    position: 'Among the strongest for everyday chat of the 9 models here that Arena has rated.',
    provenance_words: 'rated by people comparing answers on Arena',
    rated: 9,
    rank: 2,
    scored: true,
    value: {
      value: 1391,
      scale: 'rating',
      origin: {
        publisher: 'Arena (arena.ai)',
        url: 'https://huggingface.co/datasets/lmarena-ai/leaderboard-dataset',
        date: '2026-09-15',
        licence: 'CC-BY-4.0',
        attribution: 'Arena leaderboard dataset (arena.ai), 2026-09-15, CC BY 4.0 — position computed by the advisor',
        provenance: 'crowd',
      },
    },
    detail: [
      { label: 'Value as published', text: '1391 (a relative rating: only its position among other models means anything)' },
      { label: 'Votes', text: '8410' },
    ],
    fetched_at: '2026-09-24T10:00:00Z',
    ...over,
  }
}
