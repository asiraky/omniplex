import { useEffect, useState } from "react";

import type { Item } from "~/protocol";

const childrenUrl = (threadId: string, itemId: string) =>
  `/api/threads/${encodeURIComponent(threadId)}/items/${encodeURIComponent(itemId)}/children`;

async function readChildren(url: string, signal: AbortSignal): Promise<Item[]> {
  const res = await fetch(url, { signal });
  // No such parent on the server is no narration, not a failure to report.
  if (res.status === 404) return [];
  if (!res.ok) {
    const message = await res
      .json()
      .then((b: { error?: string }) => b.error ?? "")
      .catch(() => "");
    throw new Error(message || `Could not load this agent's activity (${res.status})`);
  }
  const body = (await res.json()) as { items?: Item[] };
  return body.items ?? [];
}

export type AgentChildren =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; error: string }
  | { status: "ready"; items: Item[] };

/**
 * Every child item of an agent's spawning tool call, fetched while `wanted`:
 * the snapshot leaves a settled agent's narration out and keeps only the tail
 * of a running one's. Held here and nowhere else — never written into the
 * thread — so leaving the drill-down drops it.
 */
export function useAgentChildren(
  threadId: string,
  toolCallId: string | undefined,
  wanted: boolean,
): AgentChildren & { retry: () => void } {
  const url = toolCallId ? childrenUrl(threadId, toolCallId) : "";
  const [state, setState] = useState<{ url: string; value: AgentChildren }>({
    url: "",
    value: { status: "idle" },
  });
  const [attempt, setAttempt] = useState(0);

  // react-doctor-disable-next-line react-doctor/no-fetch-in-effect -- one fetch per request with AbortController cleanup and stale results ignored; the app has no data-fetching library
  useEffect(() => {
    if (!url || !wanted) return;
    const ctl = new AbortController();
    setState({ url, value: { status: "loading" } });
    readChildren(url, ctl.signal)
      .then((items) => {
        if (ctl.signal.aborted) return;
        setState({ url, value: { status: "ready", items } });
      })
      .catch((e: unknown) => {
        if (ctl.signal.aborted) return;
        setState({ url, value: { status: "error", error: e instanceof Error ? e.message : String(e) } });
      });
    return () => ctl.abort();
  }, [url, wanted, attempt]);

  const retry = () => setAttempt((n) => n + 1);
  // State left from another agent is not this one's.
  if (state.url !== url) return wanted && url ? { status: "loading", retry } : { status: "idle", retry };
  // A fetch abandoned because it stopped being wanted will never land.
  if (!wanted && state.value.status === "loading") return { status: "idle", retry };
  return { ...state.value, retry };
}

/**
 * The fetched children with the live ones from thread state laid over them:
 * the thread's copy of an item wins, since live events keep folding into it,
 * and children the fetch predates follow on in the thread's order.
 */
export function mergeChildren(fetched: Item[] | undefined, live: Item[]): Item[] {
  if (!fetched) return live;
  const byId = new Map(live.map((it) => [it.id, it]));
  const seen = new Set<string>();
  const out: Item[] = [];
  for (const it of fetched) {
    if (seen.has(it.id)) continue;
    seen.add(it.id);
    out.push(byId.get(it.id) ?? it);
  }
  for (const it of live) if (!seen.has(it.id)) out.push(it);
  return out;
}
