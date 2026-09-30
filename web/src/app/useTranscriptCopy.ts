import { useCallback } from "react";

import { useCopy } from "~/lib/clipboard";
import { toast } from "~/lib/toast";
import { transcriptMarkdown } from "~/lib/transcript";

import type { Wire } from "./useWire";

export type TranscriptCopy = ReturnType<typeof useTranscriptCopy>;

/**
 * Copying the attached thread's whole transcript as markdown.
 *
 * Copying wants the whole timeline, and a windowed state only holds the tail,
 * so pull the rest in first. What arrives stays loaded, which is exactly what
 * a reader who just copied everything would expect. A failed fetch aborts the
 * copy loudly: a truncated transcript that says "Copied" is a lie pasted
 * somewhere the truncation won't be noticed.
 */
export function useTranscriptCopy(wire: Wire) {
  const { clientRef, stateRef } = wire;
  const { copied, copy } = useCopy();
  const copyAll = useCallback(async () => {
    let s = stateRef.current;
    if (!s) return;
    if ((s.itemsBefore ?? 0) > 0) {
      const full = await clientRef.current?.loadAll();
      if (!full) {
        toast.error("Could not load the full transcript to copy");
        return;
      }
      s = full;
    }
    await copy(transcriptMarkdown(s.items, s.turns));
  }, [clientRef, copy, stateRef]);
  return { copied, copyAll };
}
