import { ArrowUpIcon, SparklesIcon } from "lucide-react";
import { useCallback, useState, useSyncExternalStore } from "react";

import type { DraftStore } from "~/app/draftStore";
import type { Outbox } from "~/app/outbox";
import { useThread, useThreadStore } from "~/app/threadStore";
import { suggestionShown, threadSuggestion } from "~/lib/suggestion";

import { IconButton } from "./IconButton";

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
  // A send spends the suggestion on screen. Its ack and the turn.started that
  // clears the suggestion travel separately, and the ack can win: without
  // this the chip would come back for a moment, one tap from sending twice.
  const offered = threadId && suggestion ? `${threadId}\n${suggestion}` : null;
  const [spent, setSpent] = useState<string | null>(null);
  if (sending && offered && spent !== offered) setSpent(offered);
  const text = offered === spent ? null : suggestionShown(suggestion, { empty, staged, sending });
  if (!text) return null;
  return (
    <div className="mx-auto max-w-3xl px-4 pb-2 md:px-5">
      <div
        data-suggestion
        className="bg-card/90 flex h-11 items-center rounded-full pl-1 ring-1 ring-border shadow-sm backdrop-blur md:h-9"
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
