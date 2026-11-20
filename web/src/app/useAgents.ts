import { useEffect, useRef } from "react";

import type { Agent, ThreadMeta } from "~/protocol";
import { useLatest } from "~/useLatest";

/**
 * The agent the attached thread runs for. Its live thread first; failing
 * that, the agent the thread names, so an archived thread of an agent still
 * reads as that agent's.
 */
export function currentAgent(agents: Agent[], activeId: string | null, meta: ThreadMeta | undefined): Agent | undefined {
  if (!activeId) return undefined;
  return (
    agents.find((a) => a.threadId === activeId) ??
    (meta?.agentId ? agents.find((a) => a.id === meta.agentId) : undefined)
  );
}

/**
 * The thread to move to when an agent the user was watching moved on: its
 * live thread was the attached one and it now has another. New thread and a
 * server-side replacement both look like this; archiving the agent (no
 * thread after) does not, so the user stays on the archived transcript.
 */
export function followedThread(prev: Agent[], next: Agent[], activeId: string | null): string | null {
  if (!activeId) return null;
  const was = prev.find((a) => a.threadId === activeId);
  if (!was) return null;
  const now = next.find((a) => a.id === was.id);
  if (!now?.threadId || now.threadId === activeId) return null;
  return now.threadId;
}

/** Calls onFollow with the new thread whenever followedThread finds one. */
export function useFollowAgentThread(
  agents: Agent[],
  activeId: string | null,
  onFollow: (threadId: string) => void,
): void {
  const prev = useRef(agents);
  const follow = useLatest(onFollow);
  useEffect(() => {
    const before = prev.current;
    prev.current = agents;
    const to = followedThread(before, agents, activeId);
    if (to) follow.current(to);
    // Only a new agents list can move a thread; a change of the attached
    // thread alone is the user's own doing and must not be compared against
    // an old list.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents]);
}
