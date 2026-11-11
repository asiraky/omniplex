import type { ThreadMeta } from "~/protocol";

/**
 * How well a query matches a row, or 0 for not at all. Every word of the
 * query has to appear somewhere; a word that starts the text beats one that
 * starts a word inside it, which beats one buried mid-word. Keywords count,
 * but for less than the text the row actually shows, so "copy" ranks "Copy
 * transcript" above a row that only lists it as a synonym.
 */
export function matchScore(query: string, text: string, keywords: string[] = []): number {
  const words = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return 1;
  const hay = text.toLowerCase();
  const extra = keywords.join(" ").toLowerCase();
  let total = 0;
  for (const w of words) {
    const at = hay.indexOf(w);
    if (at === 0) total += 3;
    else if (at > 0 && /[^\p{L}\p{N}]/u.test(hay[at - 1])) total += 2;
    else if (at > 0) total += 1;
    else if (extra.includes(w)) total += 0.5;
    else return 0;
  }
  return total;
}

/** Most recently active first: the order "recent threads" means. */
export function byRecency(a: ThreadMeta, b: ThreadMeta) {
  return b.updatedAt - a.updatedAt;
}

/**
 * The threads a query finds, best match first and the more recent of two
 * equal matches ahead. With no query it is just the most recent ones.
 */
export function searchThreads(
  threads: ThreadMeta[],
  query: string,
  limit: number,
  skipId?: string | null,
): ThreadMeta[] {
  const scored: { thread: ThreadMeta; score: number }[] = [];
  for (const thread of threads) {
    if (thread.id === skipId) continue;
    const score = matchScore(query, thread.title || "Untitled");
    if (score > 0) scored.push({ thread, score });
  }
  scored.sort((a, b) => b.score - a.score || byRecency(a.thread, b.thread));
  return scored.slice(0, limit).map((s) => s.thread);
}
