// Byte formatting the onboarding flow shares across steps: install and
// pull progress (plain facts, source: "n/a" on the Go side — never a
// <Figure>) and a recommendation's download size (also a fact: the
// catalogue's own listing, not an estimate). Decimal GB/MB, the way
// download pages count — matches Recommend.tsx's own formatDownload.
export function formatDownload(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '?'
  const gb = bytes / 1e9
  if (gb < 1) return `${Math.max(1, Math.round(gb * 1000))} MB`
  return `${gb.toFixed(1).replace(/\.0$/, '')} GB`
}
