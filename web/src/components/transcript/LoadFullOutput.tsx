import { useEffect, useState } from "react";

import { RetryRow, textButton } from "~/components/RetryRow";
import { Spinner } from "~/components/ui/spinner";
import type { ToolContent } from "~/protocol";

const toolContentUrl = (threadId: string, itemId: string) =>
  `/api/threads/${encodeURIComponent(threadId)}/items/${encodeURIComponent(itemId)}/content`;

/** The whole of a tool call as the server holds it: output, and input. */
export interface FullToolBody {
  content: ToolContent[];
  /** Absent when the call recorded no input. */
  input?: unknown;
}

async function readToolContent(url: string, signal?: AbortSignal): Promise<FullToolBody> {
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
  const body = (await res.json()) as { content?: ToolContent[]; input?: unknown };
  return { content: body.content ?? [], input: body.input };
}

/**
 * The way from a truncated preview to the whole output and input, and back
 * after 4G drops it. Loaded lazily, since few cards are truncated and fewer
 * are asked for in full. The body goes to `onLoad` and is held by the card
 * alone: never written back into the thread, so it is dropped with the card
 * and a long session's output never accumulates in the tab.
 */
export default function LoadFullOutput({
  threadId,
  itemId,
  onLoad,
}: {
  threadId: string;
  itemId: string;
  onLoad: (body: FullToolBody) => void;
}) {
  // Zero until asked; each load or retry bumps it, re-running the effect.
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState("");

  // Race-safe the way useArtefactText is: the cleanup aborts the request, and
  // a read that lands after its effect is gone is dropped.
  // react-doctor-disable-next-line react-doctor/no-fetch-in-effect -- one fetch per request with AbortController cleanup and stale results ignored; the app has no data-fetching library
  useEffect(() => {
    if (attempt === 0) return;
    const ctl = new AbortController();
    readToolContent(toolContentUrl(threadId, itemId), ctl.signal)
      .then((body) => {
        if (!ctl.signal.aborted) onLoad(body);
      })
      .catch((e: unknown) => {
        if (!ctl.signal.aborted) setError(e instanceof Error ? e.message : String(e));
      });
    return () => ctl.abort();
  }, [threadId, itemId, onLoad, attempt]);

  const load = () => {
    setError("");
    setAttempt((n) => n + 1);
  };

  if (error) return <RetryRow message={error} onRetry={load} />;
  const loading = attempt > 0;
  return (
    <button type="button" onClick={load} disabled={loading} className={textButton}>
      {loading && <Spinner className="text-primary size-3" />}
      {loading ? "Loading full output…" : "Load full output"}
    </button>
  );
}
