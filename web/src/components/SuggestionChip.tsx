import { ArrowUpIcon, SparklesIcon } from "lucide-react";
import { useCallback, useSyncExternalStore } from "react";

import type { DraftStore } from "~/app/draftStore";
import type { Outbox } from "~/app/outbox";
import { useThread, useThreadStore } from "~/app/threadStore";
import type { ThreadState } from "~/protocol";

import { IconButton } from "./IconButton";

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
 * idle, and nothing (a permission, a question, a card, a queued prompt) is
 * waiting on the human. Null otherwise.
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

/**
 * The harness's guess at the next prompt, one line above the composer. The
 * text loads it into the composer to edit; the arrow sends it as it stands.
 */
export function SuggestionChip({
  threadId,
  draftStore,
  outbox,
  staged,
  onEdit,
  onSend,
}: {
  threadId: string | null;
  draftStore: DraftStore;
  outbox: Outbox;
  staged: boolean;
  onEdit: (text: string) => void;
  onSend: (text: string) => void;
}) {
  const suggestion = useThread(useThreadStore(), threadSuggestion);
  // Booleans, not the draft: a keystroke rerenders this only when the
  // composer goes from empty to not or back.
  const emptyDraft = useCallback(
    () => !threadId || draftStore.get(threadId) === "",
    [draftStore, threadId],
  );
  const empty = useSyncExternalStore(draftStore.subscribe, emptyDraft, emptyDraft);
  const inFlight = useCallback(
    () => !!threadId && outbox.get().some((e) => e.threadId === threadId && e.status !== "failed"),
    [outbox, threadId],
  );
  const sending = useSyncExternalStore(outbox.subscribe, inFlight, inFlight);
  const text = suggestionShown(suggestion, { empty, staged, sending });
  if (!text) return null;
  return (
    <div className="mx-auto max-w-3xl px-4 pb-2 md:px-5">
      <div
        data-suggestion
        className="bg-card/90 flex h-11 items-center rounded-full border pl-1 shadow-sm backdrop-blur md:h-9"
      >
        <button
          type="button"
          onClick={() => onEdit(text)}
          title={text}
          aria-label={`Edit suggestion: ${text}`}
          className="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex h-full min-w-0 flex-1 items-center gap-2 rounded-full pl-3 text-left text-sm outline-none focus-visible:ring-2 md:text-[13px]"
        >
          <SparklesIcon className="size-3.5 shrink-0" aria-hidden />
          <span className="truncate">{text}</span>
        </button>
        <IconButton
          label="Send suggestion"
          onClick={() => onSend(text)}
          className="rounded-full md:size-8"
        >
          <ArrowUpIcon />
        </IconButton>
      </div>
    </div>
  );
}
