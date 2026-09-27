import { useEffect } from "react";

export const APP_NAME = "Omniplex";

/** What the tab should say for a given thread. Exported for the test, and so
 *  the rule lives in one place rather than inside an effect. */
export function documentTitle(
  thread: { title?: string; needsAttention?: boolean } | null,
): string {
  if (!thread) return APP_NAME;
  const name = thread.title?.trim() || "Untitled thread";
  // A backgrounded tab is the normal case here — the work happens elsewhere
  // and you come back to it — so a thread that is waiting on an answer says
  // so in the one place a background tab still shows: its title.
  return `${thread.needsAttention ? "● " : ""}${name} — ${APP_NAME}`;
}

/** Keeps document.title in step with the attached thread. */
export function useDocumentTitle(thread: { title?: string; needsAttention?: boolean } | null) {
  const title = documentTitle(thread);
  useEffect(() => {
    document.title = title;
  }, [title]);
}
