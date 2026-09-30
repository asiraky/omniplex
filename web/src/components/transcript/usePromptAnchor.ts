import { useLayoutEffect, useRef, type RefObject } from "react";

/**
 * Lifts a prompt the reader just sent to the top of the view.
 *
 * Only a prompt that arrived while this thread was already on screen is one
 * the reader just sent. Opening a thread, or switching between two, also
 * changes the newest prompt — the whole transcript arrives at once — and
 * yanking the view then would be moving a transcript nobody asked to move.
 * Which is why the thread is remembered separately from the prompt: a
 * thread first seen with no prompt in it at all still anchors the first one
 * it gets.
 */
export function usePromptAnchor(
  scrollerRef: RefObject<HTMLDivElement | null>,
  threadId: string,
  lastPromptID: string | undefined,
  anchorTo: (el: HTMLElement | null) => void,
) {
  const seenThread = useRef<string>(undefined);
  const seenPrompt = useRef<string>(undefined);
  useLayoutEffect(() => {
    const fresh = seenThread.current !== threadId;
    const prev = seenPrompt.current;
    seenThread.current = threadId;
    seenPrompt.current = lastPromptID;
    if (fresh || !lastPromptID || lastPromptID === prev) return;
    anchorTo(
      scrollerRef.current?.querySelector<HTMLElement>(`[data-msg-id="${lastPromptID}"]`) ?? null,
    );
  }, [threadId, lastPromptID, anchorTo, scrollerRef]);
}
