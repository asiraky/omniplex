import { useCallback, type RefObject } from "react";

import type { ComposerHandle } from "~/components/Composer";
import { useDocksPanel } from "~/useMediaQuery";

import { appendDraft } from "./draftStore";
import type { PanelControls } from "./usePanel";
import type { ComposerDrafts } from "./useComposerDrafts";

/**
 * Terminal output on its way to the thread's composer, quoted already. It goes
 * after whatever is typed there, since a quote is usually what a question is
 * about and the question is the next thing written.
 *
 * Then the composer has to be on screen. As a sheet the panel covers it, so it
 * closes; that also hands the phone back the transcript, and the keyboard
 * stays down until the composer is tapped. Docked, the composer is beside the
 * panel unless the panel was expanded over it, and it takes the cursor, at the
 * end of the quote, so typing the question is the next keystroke.
 */
export function useAddToChat({
  activeId,
  store,
  panel,
  composerRef,
}: {
  activeId: string | null;
  store: ComposerDrafts;
  panel: PanelControls;
  composerRef: RefObject<ComposerHandle | null>;
}) {
  const docked = useDocksPanel();
  const { setDrafts, getDraft } = store;
  const { close, expanded, toggleExpanded } = panel;

  return useCallback(
    (quoted: string) => {
      if (!activeId) return;
      setDrafts((d) => appendDraft(d, activeId, quoted));
      if (!docked) {
        close();
        return;
      }
      if (expanded) toggleExpanded();
      // The draft store is written synchronously, so this is the new length.
      // focusEnd waits a frame, by which time an unhidden composer is laid out.
      composerRef.current?.focusEnd(getDraft(activeId).length);
    },
    [activeId, setDrafts, getDraft, docked, close, expanded, toggleExpanded, composerRef],
  );
}
