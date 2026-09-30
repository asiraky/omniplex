import { useCallback } from "react";

import type { PullRequest, ThreadMeta } from "~/protocol";
import { useThreadPR } from "~/useThreadPR";

import type { Wire } from "./useWire";

/** The pull request for the attached thread's branch, if it has one. */
export function useActivePR(wire: Wire, activeId: string | null, meta: ThreadMeta | undefined) {
  const { clientRef } = wire;
  const fetchPR = useCallback(
    async (threadId: string): Promise<PullRequest | null> => {
      const res = await clientRef.current!.command("thread_pr", { threadId });
      return (res.pr ?? null) as PullRequest | null;
    },
    [clientRef],
  );
  // The server checks this too and is the authority; asking here only spares
  // a subprocess for the threads that plainly have nothing to report.
  const prEligible =
    (meta?.workspaceMode === "managed" || meta?.workspaceMode === "borrowed") && !!meta?.branch;
  return useThreadPR(activeId, prEligible, fetchPR);
}
