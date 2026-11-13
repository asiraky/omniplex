// A thread's address, /thread/<id>: the URL the page shows while the thread is
// open, the one "Copy thread link" hands out, and the one a tapped
// notification opens. The service worker (public/sw.js) either opens the app
// there, or, when a window is already open, focuses it and posts
// {type: "open-thread", threadId}. This is the page's half: reading both, and
// registering the worker. It is in the main bundle, so it stays tiny; the rest
// of push lives in lib/push.ts.

const THREAD_PATH = /^\/thread\/([^/]+)\/?$/;

/** The path that opens a thread. */
export function threadPath(id: string): string {
  return `/thread/${encodeURIComponent(id)}`;
}

/** A thread's full link, on the address this page was reached at. */
export function threadUrl(id: string): string {
  return window.location.origin + threadPath(id);
}

/** The thread a /thread/<id> URL names, or null for any other path. */
export function threadIdFromPath(pathname: string): string | null {
  const m = THREAD_PATH.exec(pathname);
  if (!m) return null;
  try {
    return decodeURIComponent(m[1]) || null;
  } catch {
    return null;
  }
}

/** The thread an "open-thread" message from the service worker names, or null for anything else. */
export function openThreadMessage(data: unknown): string | null {
  if (typeof data !== "object" || data === null) return null;
  const { type, threadId } = data as { type?: unknown; threadId?: unknown };
  return type === "open-thread" && typeof threadId === "string" && threadId ? threadId : null;
}

/**
 * Register /sw.js, which shows pushes and routes taps on them. It has no fetch
 * handler, so it never stands between the page and the network. Failure only
 * costs notifications, so it is logged and nothing more.
 */
export function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) return;
  navigator.serviceWorker.register("/sw.js").catch((err) => {
    console.warn("service worker registration failed", err);
  });
}
