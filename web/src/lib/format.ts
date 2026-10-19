// Shared number formatting for token counts and percentages, so the context
// meter and the transcript's compaction notice speak the same language.

/**
 * Token counts want one significant step, not exact digits:
 * `<1k` raw · `<10k` → `4.2k` · `<1M` → `84k` · else `1.2M`.
 */
export function fmtTokens(n: number): string {
  const v = Math.max(0, Math.round(n));
  if (v >= 1_000_000) {
    const m = v / 1_000_000;
    return `${m >= 10 ? Math.round(m) : Math.round(m * 10) / 10}M`;
  }
  if (v >= 10_000) return `${Math.round(v / 1_000)}k`;
  if (v >= 1_000) return `${Math.round(v / 100) / 10}k`;
  return String(v);
}

/** A percentage reads best with one decimal below 10% and whole numbers above. */
export function fmtPct(p: number): string {
  const v = Math.max(0, p);
  return v < 10 ? `${Math.round(v * 10) / 10}%` : `${Math.round(v)}%`;
}

/** Names as a sentence lists them: "A", "A and B", "A, B and C". */
export function joinAnd(names: string[]): string {
  if (names.length <= 1) return names[0] ?? "";
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}
