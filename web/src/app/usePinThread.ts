import { useMemo } from "react";

import type { ThreadMeta } from "~/protocol";

import type { Wire } from "./useWire";
import { useThreadOverlay, type OverlayCommand } from "./useThreadOverlay";

const PIN: OverlayCommand<boolean> = {
  send: (client, threadId, pinned) => client.command("set_thread_pinned", { threadId, pinned }),
  errorTitle: (pinned) => (pinned ? "Could not pin that thread" : "Could not unpin that thread"),
};

/**
 * Pinning a thread, and the thread list as the user should see it meanwhile
 * (see `useThreadOverlay`).
 *
 * The row jumps into (or out of) the Pinned section at once. On a phone the
 * round trip is seconds, and a tap that does nothing for that long reads as a
 * tap that missed.
 *
 * `threads` is the list to pin in — App's, with pending renames already on it.
 */
export function usePinThread(wire: Wire, threads: ThreadMeta[]) {
  const { live, set } = useThreadOverlay(wire, PIN);
  const shown = useMemo(() => {
    if (live.size === 0) return threads;
    return threads.map((s) => {
      const pinned = live.get(s.id);
      return pinned === undefined || !!s.pinned === pinned ? s : { ...s, pinned };
    });
  }, [threads, live]);
  return { threads: shown, setPinned: set };
}
