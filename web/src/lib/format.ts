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

/** How long ago, in the one unit a glance needs: `now` · `5m` · `3h` · `2d`. */
export function fmtAgo(ms: number, now: number): string {
  const s = Math.max(0, Math.floor((now - ms) / 1000));
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}
