import { useCallback, useEffect, useRef } from "react";

/**
 * The draft as of the latest keystroke, for async work that must not clobber
 * what was typed while it ran. `changeDraft` updates it before the parent's
 * render catches up, so a check straight after a change already sees it.
 */
export function useDraftRef(draft: string, onDraftChange: (text: string) => void) {
  const draftRef = useRef(draft);

  useEffect(() => {
    draftRef.current = draft;
  }, [draft]);

  const changeDraft = useCallback(
    (next: string) => {
      draftRef.current = next;
      onDraftChange(next);
    },
    [onDraftChange],
  );

  return { draftRef, changeDraft };
}
