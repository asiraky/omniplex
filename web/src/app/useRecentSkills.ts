import { useCallback, useEffect, useRef, useState } from "react";

import type { ComposerHandle } from "~/components/Composer";
import { loadRecentSkills, recordRecentSkill, resolveRecentSkills } from "~/lib/recentSkills";
import type { ComposerItem, ThreadState } from "~/protocol";

import { useThread, type ThreadStore } from "./threadStore";
import type { ComposerDrafts } from "./useComposerDrafts";

export type RecentSkills = ReturnType<typeof useRecentSkills>;

/**
 * The empty transcript's list of skills to reach for, and the composer it
 * writes into. Both live above the thread view for the same reason the drafts
 * do: the Transcript and the Composer are siblings remounted per thread, and
 * the app is the one place that can see the catalogue, the project, and the
 * input at once.
 */
export function useRecentSkills({
  activeId,
  projectId,
  threadStore,
  store,
  isDesktop,
  loadComposerItems,
}: {
  activeId: string | null;
  projectId: string | undefined;
  threadStore: ThreadStore;
  store: ComposerDrafts;
  isDesktop: boolean;
  loadComposerItems: () => Promise<ComposerItem[]>;
}) {
  const { getDraft, setDraft } = store;
  const composerRef = useRef<ComposerHandle>(null);
  const [recents, setRecents] = useState<{ items: ComposerItem[]; seeded: boolean }>({
    items: [],
    seeded: false,
  });
  // Only an empty transcript asks for this, so only an empty transcript pays
  // for the catalogue fetch. A newly provisioned thread publishes empty
  // snapshots before its harness is ready; asking during those snapshots can
  // produce an empty catalogue that would otherwise stick until a thread
  // switch. Wait for the harness-backed idle/turn phase instead. Selected as
  // the one boolean, so the frames that leave it as it was rerender nothing.
  const transcriptEmpty = useThread(threadStore, isTranscriptEmpty);
  useEffect(() => {
    if (!activeId || !transcriptEmpty) {
      setRecents((prev) => (prev.items.length === 0 ? prev : { items: [], seeded: false }));
      return;
    }
    let cancelled = false;
    loadComposerItems()
      .then((catalogue) => {
        if (cancelled) return;
        const history = loadRecentSkills(projectId);
        const items = resolveRecentSkills(history, catalogue);
        // Seeded means none of what is being shown was actually remembered:
        // a first run, or a project whose history no longer resolves.
        const remembered = new Set(history);
        const seeded = !items.some((item) => remembered.has(item.insertText));
        setRecents({ items, seeded });
      })
      .catch(() => {
        // No catalogue, no suggestions. The empty state still reads fine.
      });
    return () => {
      cancelled = true;
    };
  }, [activeId, transcriptEmpty, loadComposerItems, projectId]);

  // Clicking a suggestion writes the token and a space into the draft, and
  // nothing else: what happens next (an argument, or straight to submit) is
  // the user's to decide. Desktop takes the cursor with it, because the next
  // keystroke almost always belongs in the input. A phone deliberately does
  // not: focusing raises the keyboard over the very button just tapped, and
  // submit is one tap away without it. Desktop types the token in, so undo can
  // take it back out; the phone, never focusing, writes the draft directly.
  const pick = useCallback(
    (item: ComposerItem) => {
      if (!activeId) return;
      const current = getDraft(activeId);
      const gap = current && !/\s$/.test(current) ? " " : "";
      const text = `${gap}${item.insertText} `;
      if (isDesktop && composerRef.current?.typeAtEnd(text)) return;
      const next = `${current}${text}`;
      setDraft(activeId, next);
      if (isDesktop) composerRef.current?.focusEnd(next.length);
    },
    [activeId, getDraft, isDesktop, setDraft],
  );

  const noteUsed = useCallback(
    (insertText: string) => recordRecentSkill(projectId, insertText),
    [projectId],
  );

  return { composerRef, items: recents.items, seeded: recents.seeded, pick, noteUsed };
}

function isTranscriptEmpty(state: ThreadState | null) {
  return !!state && state.items.length === 0 && (state.phase === "idle" || state.phase === "turn");
}
