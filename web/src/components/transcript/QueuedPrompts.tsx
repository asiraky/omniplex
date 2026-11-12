import { lazy, Suspense, useEffect, useRef } from "react";

import type { Artefact } from "~/lib/artefacts";
import type { QueuedPrompt } from "~/protocol";

import { Button } from "~/components/ui/button";

// Most threads never queue anything, so the card and its editor stay out of
// the initial bundle. A prompt only queues behind a running turn, so the turn
// starting fetches them, and the card is not a round trip late on 4G.
const loadQueuedCard = () => import("./QueuedCard");
const QueuedCard = lazy(() => loadQueuedCard().then((m) => ({ default: m.QueuedCard })));

// The tail of the transcript: what is waiting to run, with the way to let a
// held queue go above it. Rendered whether or not anything is queued, since
// it remembers the edits in progress across the queue emptying.
export function QueuedPrompts({
  queued,
  threadId,
  artefacts,
  heldFor,
  busy,
  steers,
  onDequeue,
  onEdit,
  onSend,
  onResume,
  onEditLost,
}: {
  queued: QueuedPrompt[];
  threadId: string;
  artefacts: Artefact[];
  /** What a usage limit holds each prompt for, if it does. */
  heldFor?: (q: QueuedPrompt) => "reset" | "send" | undefined;
  busy: boolean;
  steers: boolean;
  onDequeue?: (queueId: string) => void;
  onEdit?: (queueId: string, prompt: string) => Promise<boolean> | boolean;
  onSend?: (queueId: string) => void;
  onResume?: () => void;
  onEditLost?: (text: string) => void;
}) {
  const track = useUnsavedEdits(queued, onEditLost);
  useEffect(() => {
    if (busy) void loadQueuedCard();
  }, [busy]);
  return (
    <>
      {queued.some((q) => q.held) && <ResumeQueue onResume={() => onResume?.()} />}
      <Suspense fallback={null}>
        {queued.map((q) => (
          <QueuedCard
            key={q.queueId}
            queued={q}
            threadId={threadId}
            artefacts={artefacts}
            limitHeld={heldFor?.(q)}
            busy={busy}
            steers={steers}
            onDequeue={(id) => onDequeue?.(id)}
            onEdit={(id, prompt) => onEdit?.(id, prompt) ?? false}
            onSend={(id) => onSend?.(id)}
            onUnsaved={(text) => track(q.queueId, text)}
          />
        ))}
      </Suspense>
    </>
  );
}

// Above the queue when the server is holding it, after a restart or a start
// it refused: nothing in it runs until the person says go, since what was
// queued may no longer be wanted. A usage limit's hold is not this one: it
// lifts on its own, and each card says what it waits on.
function ResumeQueue({ onResume }: { onResume: () => void }) {
  return (
    <div className="text-muted-foreground flex items-center justify-end gap-2 text-[12px]">
      <span className="flex flex-col items-end text-right">
        Queue paused
        <span className="text-[11px]">Nothing here runs until you resume or start a new turn</span>
      </span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-9 text-[12px] pointer-coarse:min-h-11 md:h-7"
        onClick={onResume}
      >
        Resume queue
      </Button>
    </div>
  );
}

// A prompt can start while its editor is open: the turn ahead of it ends and
// the server runs it with the old words, taking it out of the queue and the
// editor with it. What was typed is handed on rather than lost with the card.
// The edits live here and not in the cards, since a card is gone by the time
// anyone could ask it; switching threads unmounts this too, and that drops
// them, which is what leaving a half-made edit means.
function useUnsavedEdits(queued: QueuedPrompt[], onLost: ((text: string) => void) | undefined) {
  const unsaved = useRef(new Map<string, string>());

  useEffect(() => {
    for (const [id, text] of unsaved.current) {
      // One the harness now holds has left the editor as surely as one that
      // started: it can no longer be edited.
      if (queued.some((q) => q.queueId === id && !q.sent)) continue;
      unsaved.current.delete(id);
      if (text) onLost?.(text);
    }
  }, [queued, onLost]);

  return (queueId: string, text: string | undefined) => {
    if (text === undefined) unsaved.current.delete(queueId);
    else unsaved.current.set(queueId, text);
  };
}
