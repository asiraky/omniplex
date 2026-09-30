import { useEffect, useState } from "react";

/** The most of a file the text viewers read. Past this a phone is paying for
    bytes nobody will scroll to, and the DOM for them costs more than the wire. */
export const TEXT_CAP = 1024 * 1024;

export interface TextRead {
  text: string;
  /** The file goes on past what was read. */
  truncated: boolean;
}

// A raw URL carries the revision it was made for, so its text is good for as
// long as the tab lives. Small and bounded: this only exists so that flipping
// between preview and source, or back to a file just looked at, is instant.
const cache = new Map<string, TextRead>();
const CACHE_ENTRIES = 8;

function remember(url: string, read: TextRead) {
  cache.delete(url);
  cache.set(url, read);
  while (cache.size > CACHE_ENTRIES) cache.delete(cache.keys().next().value!);
}

/** Only for tests, which each want to see the fetch happen. */
export function forgetTextCache() {
  cache.clear();
}

/**
 * Reads at most TEXT_CAP bytes. Asks for only that range, and stops reading if
 * a server that ignores Range sends the whole thing anyway.
 */
export async function readText(url: string, signal?: AbortSignal): Promise<TextRead> {
  const res = await fetch(url, { signal, headers: { Range: `bytes=0-${TEXT_CAP}` } });
  // An empty file has no byte 0 to start a range at.
  if (res.status === 416) return { text: "", truncated: false };
  if (!res.ok) {
    const message = await res
      .json()
      .then((b: { error?: string }) => b.error ?? "")
      .catch(() => "");
    throw new Error(message || `Could not read the file (${res.status})`);
  }
  const chunks: Uint8Array[] = [];
  let total = 0;
  const reader = res.body?.getReader();
  if (reader) {
    while (total <= TEXT_CAP) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
      total += value.byteLength;
    }
    if (total > TEXT_CAP) void reader.cancel().catch(() => {});
  } else {
    const all = new Uint8Array(await res.arrayBuffer());
    chunks.push(all);
    total = all.byteLength;
  }
  const bytes = new Uint8Array(Math.min(total, TEXT_CAP));
  let at = 0;
  for (const c of chunks) {
    if (at >= bytes.length) break;
    const part = c.subarray(0, bytes.length - at);
    bytes.set(part, at);
    at += part.byteLength;
  }
  const range = /\/(\d+)$/.exec(res.headers.get("Content-Range") ?? "");
  const truncated = total > TEXT_CAP || (range !== null && Number(range[1]) > TEXT_CAP);
  return { text: new TextDecoder().decode(bytes), truncated };
}

export type TextState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; error: string }
  | ({ status: "ready" } & TextRead);

/** The file's text, fetched when `enabled`, with a retry for when 4G drops it. */
export function useArtefactText(url: string, enabled: boolean): TextState & { retry: () => void } {
  const [state, setState] = useState<{ url: string; value: TextState }>({
    url: "",
    value: { status: "idle" },
  });
  const [attempt, setAttempt] = useState(0);
  const cached = cache.get(url);

  // Race-safe without a data library: the cleanup aborts the request, and a
  // read that lands after its effect is gone (a retry, a newer URL, unmount)
  // is dropped rather than written over the state that replaced it.
  // react-doctor-disable-next-line react-doctor/no-fetch-in-effect -- one fetch per URL with AbortController cleanup and stale results ignored; the app has no data-fetching library
  useEffect(() => {
    if (!enabled || cache.has(url)) return;
    const ctl = new AbortController();
    setState({ url, value: { status: "loading" } });
    readText(url, ctl.signal)
      .then((read) => {
        // The text is right for this URL whoever asked, so it is still worth
        // keeping even when the answer is too late to show.
        remember(url, read);
        if (ctl.signal.aborted) return;
        setState({ url, value: { status: "ready", ...read } });
      })
      .catch((e: unknown) => {
        if (ctl.signal.aborted) return;
        setState({
          url,
          value: { status: "error", error: e instanceof Error ? e.message : String(e) },
        });
      });
    return () => ctl.abort();
  }, [url, enabled, attempt]);

  const retry = () => setAttempt((n) => n + 1);
  if (!enabled) return { status: "idle", retry };
  if (cached) return { status: "ready", ...cached, retry };
  // State left from another URL is not this one's.
  if (state.url !== url) return { status: "loading", retry };
  return { ...state.value, retry };
}
