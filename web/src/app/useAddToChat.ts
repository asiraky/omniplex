import { useCallback, useEffect, useRef, type RefObject } from "react";

import type { ComposerHandle } from "~/components/Composer";
import type { ThreadMeta } from "~/protocol";
import { useDocksPanel, useIsDesktop } from "~/useMediaQuery";

import { appendDraft } from "./draftStore";
import { LAST_THREAD } from "./threadKeys";
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

/**
 * A comment on a diff line on its way to the thread's composer, after whatever
 * is there. Unlike a terminal quote, the panel stays as it is and the composer
 * is left alone: the next thing done is usually commenting on another line, so
 * the composer draft fills up as a pending review and is sent once, at the end.
 */
export function useAppendToChat({ activeId, store }: { activeId: string | null; store: ComposerDrafts }) {
  const { setDrafts } = store;
  return useCallback(
    (text: string) => {
      if (!activeId) return;
      setDrafts((d) => appendDraft(d, activeId, text));
    },
    [activeId, setDrafts],
  );
}

/**
 * Box terminal output on its way to a thread's composer. The box belongs to no
 * thread, so it goes to the one open behind the terminal, else the one last
 * opened, as long as the list still has it open. Undefined when neither is,
 * and the terminal offers nothing to add to.
 *
 * The quote is there to be sent, so the terminal gives way to that thread's
 * composer: closed when the thread is the one behind it, the thread opened
 * when it is not. With a keyboard the cursor follows, once the composer is on
 * screen; a phone keeps its keyboard down, as the panel's quote does.
 */
export function useBoxAddToChat({
  activeId,
  attached,
  threads,
  store,
  composerRef,
  onClose,
  onOpenThread,
}: {
  activeId: string | null;
  /** The active thread's state has arrived and its composer is mounted. */
  attached: boolean;
  threads: ThreadMeta[];
  store: ComposerDrafts;
  composerRef: RefObject<ComposerHandle | null>;
  /** Closes the box terminal, back to the thread behind it. */
  onClose: () => void;
  /** Opens another thread, which leaves the terminal too. */
  onOpenThread: (id: string) => void;
}): ((quoted: string) => void) | undefined {
  const isDesktop = useIsDesktop();
  const { setDrafts, getDraft } = store;
  const target = [activeId, localStorage.getItem(LAST_THREAD)].find(
    (id): id is string => !!id && threads.some((t) => t.id === id && t.phase !== "closed"),
  );
  // A thread opened for the quote has no composer until it attaches, which on
  // a slow link is seconds later. The cursor waits for it here.
  const focusOnAttach = useRef<string | null>(null);
  useEffect(() => {
    if (!attached || !activeId || focusOnAttach.current !== activeId) return;
    focusOnAttach.current = null;
    composerRef.current?.focusEnd(getDraft(activeId).length);
  }, [attached, activeId, composerRef, getDraft]);

  const add = useCallback(
    (quoted: string) => {
      if (!target) return;
      setDrafts((d) => appendDraft(d, target, quoted));
      if (target !== activeId) {
        focusOnAttach.current = isDesktop ? target : null;
        onOpenThread(target);
        return;
      }
      onClose();
      // focusEnd waits a frame, by which time the thread view is shown again.
      if (isDesktop) composerRef.current?.focusEnd(getDraft(target).length);
    },
    [target, activeId, isDesktop, setDrafts, getDraft, onClose, onOpenThread, composerRef],
  );
  return target ? add : undefined;
}
