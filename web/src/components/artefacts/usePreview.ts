import { useCallback, useEffect, useState } from "react";

import { requestPreview, type ExpiringUrl } from "~/lib/artefacts";

// Refresh a preview token this long before it lapses, so "open in a new tab"
// never hands out a dead link and a frame mounted later starts on a live one.
// A frame already showing is not reloaded for it.
const REFRESH_BEFORE_MS = 5 * 60_000;

interface PreviewState {
  preview?: ExpiringUrl;
  error?: string;
}

/** A preview URL for an HTML artefact, kept fresh while whatever shows it is
    mounted. Nothing is asked for until `enabled`. The URL serves the live
    files, so a revision needs no new one. */
export function usePreview(threadId: string, artefactId: string, enabled: boolean) {
  const [state, setState] = useState<PreviewState & { key: string }>({ key: "" });
  const [attempt, setAttempt] = useState(0);
  const key = `${threadId}/${artefactId}`;

  useEffect(() => {
    if (!enabled) return;
    let stale = false;
    requestPreview(threadId, artefactId)
      .then((preview) => !stale && setState({ key, preview }))
      .catch((e: unknown) => !stale && setState({ key, error: e instanceof Error ? e.message : String(e) }));
    return () => {
      stale = true;
    };
  }, [threadId, artefactId, enabled, attempt, key]);

  const refresh = useCallback(async () => {
    const preview = await requestPreview(threadId, artefactId);
    setState({ key, preview });
    return preview;
  }, [threadId, artefactId, key]);

  const current = state.key === key ? state : { key };
  useEffect(() => {
    if (!current.preview) return;
    const wait = Math.max(10_000, current.preview.expiresAt - Date.now() - REFRESH_BEFORE_MS);
    const t = setTimeout(() => void refresh().catch(() => {}), wait);
    return () => clearTimeout(t);
  }, [current.preview, refresh]);

  return { ...current, refresh, retry: () => setAttempt((n) => n + 1) };
}
