import { useMemo } from "react";

import type { ThreadMeta } from "~/protocol";
import { withPositions } from "~/threadOrder";

import type { Wire } from "./useWire";
import { useThreadOverlay, type OverlayCommand } from "./useThreadOverlay";

const MOVE: OverlayCommand<number> = {
  send: (client, threadId, position) =>
    client.command("set_thread_position", { threadId, position }),
  errorTitle: () => "Could not move that thread",
};

/**
 * The thread list in the user's order, with their moves applied before the
 * server has seen them (see `useThreadOverlay`).
 *
 * A dropped row has to land where the finger let go, not a round trip later:
 * on a phone that is seconds, and a row that snaps back and then jumps reads
 * as the drag having failed.
 *
 * `threads` is the list to order — App's, with pending renames and pins
 * already on it.
 */
export function useThreadOrder(wire: Wire, threads: ThreadMeta[]) {
  const { live, set } = useThreadOverlay(wire, MOVE);
  const shown = useMemo(() => withPositions(threads, live), [threads, live]);
  return { threads: shown, move: set };
}
