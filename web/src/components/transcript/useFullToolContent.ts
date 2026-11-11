import { useEffect, useState } from "react";

import type { ToolContent } from "~/protocol";

const toolContentUrl = (threadId: string, itemId: string) =>
  `/api/threads/${encodeURIComponent(threadId)}/items/${encodeURIComponent(itemId)}/content`;

async function readToolContent(url: string, signal?: AbortSignal): Promise<ToolContent[]> {
  const res = await fetch(url, { signal });
  if (!res.ok) {
    const message = await res
      .json()
      .then((b: { error?: string }) => b.error ?? "")
      .catch(() => "");
    if (message) throw new Error(message);
    throw new Error(
      res.status === 404 ? "The full output is not available" : `Could not load the output (${res.status})`,
    );
  }
  const body = (await res.json()) as { content?: ToolContent[] };
  return body.content ?? [];
}

export type FullContent =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; error: string }
  | { status: "ready"; content: ToolContent[] };

/**
 * A truncated tool call's whole output, fetched once `load` is called. It lives
 * in this hook's state and nowhere else: never written back into the thread, so
 * it is dropped with the card and a long session's output never accumulates in
 * the tab.
 */
export function useFullToolContent(
  threadId: string,
  itemId: string,
): FullContent & { load: () => void } {
  const url = toolContentUrl(threadId, itemId);
  const [state, setState] = useState<{ url: string; value: FullContent }>({
    url: "",
    value: { status: "idle" },
  });
  // Zero until asked; each load or retry bumps it, re-running the effect.
  const [attempt, setAttempt] = useState(0);

  // Race-safe the way useArtefactText is: the cleanup aborts the request, and
  // a read that lands after its effect is gone is dropped.
  // react-doctor-disable-next-line react-doctor/no-fetch-in-effect -- one fetch per request with AbortController cleanup and stale results ignored; the app has no data-fetching library
  useEffect(() => {
    if (attempt === 0) return;
    const ctl = new AbortController();
    setState({ url, value: { status: "loading" } });
    readToolContent(url, ctl.signal)
      .then((content) => {
        if (ctl.signal.aborted) return;
        setState({ url, value: { status: "ready", content } });
      })
      .catch((e: unknown) => {
        if (ctl.signal.aborted) return;
        setState({ url, value: { status: "error", error: e instanceof Error ? e.message : String(e) } });
      });
    return () => ctl.abort();
  }, [url, attempt]);

  const load = () => setAttempt((n) => n + 1);
  // State left from another item is not this one's.
  if (state.url !== url) return attempt === 0 ? { status: "idle", load } : { status: "loading", load };
  return { ...state.value, load };
}
