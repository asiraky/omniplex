import type { ThreadState } from "~/protocol";

/** What the composer holds, as far as the chip cares. */
export interface ComposerEmptiness {
  /** The draft is the empty string: whitespace is something typed. */
  empty: boolean;
  /** Files are staged for the next message. */
  staged: boolean;
  /** A send from this thread has not been acked yet, or waits on uploads. */
  sending: boolean;
}

/**
 * The suggestion the thread itself allows, ignoring the composer: the harness
 * proposed one for the latest turn, that turn ended normally, the thread is
 * idle, and nothing (a permission, a question, a queued prompt) is waiting on
 * the human. Null otherwise.
 *
 * A string or null, so a selector built on it rerenders only when the answer
 * changes, not on every frame of the thread.
 */
export function threadSuggestion(state: ThreadState | null): string | null {
  if (!state?.suggestion || state.closed || state.phase !== "idle") return null;
  const latest = state.turns.at(-1);
  if (!latest || latest.id !== state.suggestion.turnId) return null;
  if (!latest.done || latest.stopReason !== "end_turn") return null;
  if (state.pendingPermissions?.length || state.pendingElicitations?.length) return null;
  if (state.queuedPrompts?.length) return null;
  return state.suggestion.text || null;
}

/**
 * The text the chip shows, or null for no chip: the thread's suggestion
 * (threadSuggestion), while the composer is empty. Typing anything hides it;
 * emptying the composer brings it back. A send in flight counts as not empty:
 * the draft clears the moment it goes, and until its turn starts the chip
 * would offer a next prompt for a turn that has already been answered.
 */
export function suggestionShown(suggestion: string | null, composer: ComposerEmptiness): string | null {
  if (!suggestion || !composer.empty || composer.staged || composer.sending) return null;
  return suggestion;
}

/** The turn the thread's suggestion follows, or null. */
export function suggestionTurn(state: ThreadState | null): string | null {
  return state?.suggestion?.turnId ?? null;
}
