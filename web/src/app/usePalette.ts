import { useCallback, useState } from "react";

import type { PalettePage } from "~/components/CommandPalette";

export type Palette = ReturnType<typeof usePalette>;

/**
 * Whether the command palette and the shortcuts overlay are up, and which
 * page the palette opens on.
 *
 * The palette starts over on its page only as it opens, so asking for another
 * page while it is already up (⌘N from inside it) bumps `nonce`, and the app
 * keys the palette on it to start it over in place.
 */
export function usePalette() {
  const [state, setState] = useState<{ open: boolean; page: PalettePage; nonce: number }>({
    open: false,
    page: "root",
    nonce: 0,
  });
  const [helpOpen, setHelpOpen] = useState(false);

  const show = useCallback((page: PalettePage) => {
    setHelpOpen(false);
    setState((s) => ({ open: true, page, nonce: s.open ? s.nonce + 1 : s.nonce }));
  }, []);
  const setOpen = useCallback((open: boolean) => setState((s) => ({ ...s, open })), []);
  // ⌘K over the palette puts it away, whichever page it is on.
  const toggle = useCallback(() => {
    setHelpOpen(false);
    setState((s) => ({ ...s, open: !s.open, page: "root" }));
  }, []);
  const toggleHelp = useCallback(() => setHelpOpen((v) => !v), []);

  return { ...state, show, setOpen, toggle, helpOpen, setHelpOpen, toggleHelp };
}
