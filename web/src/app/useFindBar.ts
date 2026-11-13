import { useCallback, useState } from "react";

export type FindBar = ReturnType<typeof useFindBar>;

/**
 * Whether the open thread's find bar is showing. Every ask to open bumps the
 * nonce, so a repeat Ctrl/⌘+F refocuses a bar that is already up. A switch to
 * another thread closes it: the query is the old thread's, not the new one's.
 */
export function useFindBar(activeId: string | null) {
  const [open, setOpen] = useState(false);
  const [nonce, setNonce] = useState(0);
  // Closed during the render that switches, rather than in an effect after
  // it, so the new thread never paints with the old one's bar.
  const [openedOn, setOpenedOn] = useState(activeId);
  if (openedOn !== activeId) {
    setOpenedOn(activeId);
    setOpen(false);
  }
  const show = useCallback(() => {
    setOpen(true);
    setNonce((n) => n + 1);
  }, []);
  const close = useCallback(() => setOpen(false), []);
  return { open, nonce, show, close };
}
