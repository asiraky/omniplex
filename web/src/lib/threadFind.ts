// The contract find-in-thread shares between the client, the hook that steps
// through matches, and the transcript that jumps to them.

/** One match: the item's id and its position in the thread's full timeline
    (the same index space as `itemsBefore`). */
export type ThreadMatch = { id: string; index: number };

/** `GET /api/threads/{id}/search?q=`: the newest matches (at most
    SEARCH_MATCH_CAP), oldest first, and the true count. */
export type ThreadSearchResult = { matches: ThreadMatch[]; total: number };

/** Server-side cap on matches returned; `total` still counts every one. */
export const SEARCH_MATCH_CAP = 500;

/** Shortest query the server runs. */
export const SEARCH_MIN_LENGTH = 2;

export type SearchThread = (q: string, signal: AbortSignal) => Promise<ThreadSearchResult>;

/** Pages the timeline in down to the turn holding `index`. Resolves true when
    the item at `index` is now loaded (or already was), false when the page was
    dropped as stale or failed. */
export type LoadThreadTo = (index: number) => Promise<boolean>;

/** What the transcript gets to run its find bar. */
export type ThreadFindControls = {
  open: boolean;
  /** Bumped on every ask to open, so a repeat Ctrl/Cmd+F refocuses the input. */
  nonce: number;
  close: () => void;
  search: SearchThread;
  loadTo: LoadThreadTo;
};
