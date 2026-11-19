import { threadTitle } from "~/lib/threadTitle";
import type { ThreadMeta, ThreadState } from "~/protocol";
import { useDocumentTitle } from "~/useDocumentTitle";

import { useThread, type ThreadStore } from "./threadStore";

/**
 * The tab is named after whatever is attached, so a phone with several
 * threads open in several tabs can tell them apart without switching to
 * each one. An agent page names the page instead: the thread under it is
 * whatever was open before, not what is on screen. An agent's thread is
 * named after the agent, as its header is, which also covers the moment
 * before its list entry and snapshot land.
 */
export function useThreadTitle(
  activeId: string | null,
  threadStore: ThreadStore,
  meta: ThreadMeta | undefined,
  { agentName, pageTitle }: { agentName?: string; pageTitle?: string } = {},
) {
  // The list entry, not just the attached state: switching threads drops
  // `state` until the snapshot lands, and on a slow connection that would
  // leave every tab called "Omniplex" for exactly as long as it takes to
  // reconnect, which is when telling them apart matters most.
  const title = useThread(threadStore, titleOf);
  const needsAttention = useThread(threadStore, needsAttentionOf);
  useDocumentTitle(
    pageTitle !== undefined
      ? { title: pageTitle }
      : activeId
        ? { title: agentName || threadTitle(meta, title), needsAttention }
        : null,
  );
}

const titleOf = (state: ThreadState | null) => state?.title;
const needsAttentionOf = (state: ThreadState | null) =>
  Boolean(state?.pendingPermissions?.[0] || state?.pendingElicitations?.[0]);
