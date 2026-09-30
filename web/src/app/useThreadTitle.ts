import type { ThreadMeta, ThreadState } from "~/protocol";
import { useDocumentTitle } from "~/useDocumentTitle";

/**
 * The tab is named after whatever is attached, so a phone with several
 * threads open in several tabs can tell them apart without switching to
 * each one.
 */
export function useThreadTitle(
  activeId: string | null,
  state: ThreadState | null,
  meta: ThreadMeta | undefined,
) {
  // The list entry, not just the attached state: switching threads drops
  // `state` until the snapshot lands, and on a slow connection that would
  // leave every tab called "Omniplex" for exactly as long as it takes to
  // reconnect, which is when telling them apart matters most.
  const needsAttention = Boolean(state?.pendingPermissions?.[0] || state?.pendingElicitations?.[0]);
  useDocumentTitle(activeId ? { title: state?.title ?? meta?.title, needsAttention } : null);
}
