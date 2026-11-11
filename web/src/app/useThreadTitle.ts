import { threadTitle } from "~/lib/threadTitle";
import type { ThreadMeta, ThreadState } from "~/protocol";
import { useDocumentTitle } from "~/useDocumentTitle";

import { useThread, type ThreadStore } from "./threadStore";

/**
 * The tab is named after whatever is attached, so a phone with several
 * threads open in several tabs can tell them apart without switching to
 * each one.
 */
export function useThreadTitle(
  activeId: string | null,
  threadStore: ThreadStore,
  meta: ThreadMeta | undefined,
) {
  // The list entry, not just the attached state: switching threads drops
  // `state` until the snapshot lands, and on a slow connection that would
  // leave every tab called "Omniplex" for exactly as long as it takes to
  // reconnect, which is when telling them apart matters most.
  const title = useThread(threadStore, titleOf);
  const needsAttention = useThread(threadStore, needsAttentionOf);
  useDocumentTitle(activeId ? { title: threadTitle(meta, title), needsAttention } : null);
}

const titleOf = (state: ThreadState | null) => state?.title;
const needsAttentionOf = (state: ThreadState | null) =>
  Boolean(state?.pendingPermissions?.[0] || state?.pendingElicitations?.[0]);
