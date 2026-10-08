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
  // The list entry first: it carries a rename, which the attached state never
  // hears about, and switching threads drops `state` until the snapshot
  // lands, which on a slow connection would leave every tab called "Omniplex"
  // for exactly as long as it takes to reconnect. The state's title covers a
  // brand-new thread whose list entry has not caught up with its first prompt.
  const needsAttention = Boolean(state?.pendingPermissions?.[0] || state?.pendingElicitations?.[0]);
  useDocumentTitle(activeId ? { title: meta?.title || state?.title, needsAttention } : null);
}
