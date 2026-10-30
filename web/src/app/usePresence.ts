import { useEffect, useState, type RefObject } from "react";

import type { Client } from "~/client";

/**
 * Tells the server whether this page is on screen and which thread it shows,
 * so an agent's notify is skipped while you are already looking at its
 * thread. `threadId` is null when no thread is in view, which is more than
 * "none selected": a phone's open sidebar or a full-page screen covers it.
 * The client repeats the latest report on every reconnect.
 */
export function usePresence(clientRef: RefObject<Client | null>, threadId: string | null) {
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  useEffect(() => {
    const onChange = () => setVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", onChange);
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);

  useEffect(() => {
    clientRef.current?.setPresence(visible, threadId ?? "");
  }, [clientRef, visible, threadId]);
}
