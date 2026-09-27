import { useEffect, useState } from "react";

import type { PullRequest } from "~/protocol";

/**
 * How often omniplex re-asks whether the thread's branch has landed. A merge is not
 * urgent news — the offer it unlocks is a cleanup the user could do any time —
 * and `gh` is a network call per poll, so this is deliberately unhurried.
 */
export const PR_POLL_MS = 120_000;

/**
 * Watches for the thread's branch being merged.
 *
 * Only the active thread is ever polled, and only while it could plausibly
 * have a pull request: this is an affordance, so the cost of not knowing is
 * that the affordance stays hidden, and that is cheaper than asking the
 * network about every thread in the sidebar.
 *
 * Polling stops for good once a merge is seen. Nothing omniplex asks later can
 * un-merge it, so continuing to ask would be spending a subprocess every two
 * minutes to be told the same thing.
 */
export function useThreadPR(
  threadId: string | null,
  eligible: boolean,
  fetchPR: (threadId: string) => Promise<PullRequest | null>,
): PullRequest | null {
  const [pr, setPR] = useState<PullRequest | null>(null);

  useEffect(() => {
    // A new thread starts with no answer rather than the last one's: the
    // alternative shows one thread's merge under another's transcript.
    setPR(null);
    if (!threadId || !eligible) return;

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const poll = async () => {
      let found: PullRequest | null = null;
      // A failed lookup is an ordinary answer here — gh missing, no remote, no
      // pull request yet — and means only that there is nothing to show.
      try {
        found = await fetchPR(threadId);
      } catch {
        found = null;
      }
      if (cancelled) return;
      setPR(found);
      if (found?.merged) return;
      timer = setTimeout(poll, PR_POLL_MS);
    };
    void poll();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [threadId, eligible, fetchPR]);

  return pr;
}
