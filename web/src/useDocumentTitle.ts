import { useEffect } from "react";

export const APP_NAME = "Omniplex";

/** What the tab should say for a given session. Exported for the test, and so
 *  the rule lives in one place rather than inside an effect. */
export function documentTitle(
  session: { title?: string; needsAttention?: boolean } | null,
): string {
  if (!session) return APP_NAME;
  const name = session.title?.trim() || "Untitled session";
  // A backgrounded tab is the normal case here — the work happens elsewhere
  // and you come back to it — so a session that is waiting on an answer says
  // so in the one place a background tab still shows: its title.
  return `${session.needsAttention ? "● " : ""}${name} — ${APP_NAME}`;
}

/** Keeps document.title in step with the attached session. */
export function useDocumentTitle(session: { title?: string; needsAttention?: boolean } | null) {
  const title = documentTitle(session);
  useEffect(() => {
    document.title = title;
  }, [title]);
}
