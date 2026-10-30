// Omniplex service worker: shows push notifications and opens the thread a
// tapped one belongs to. Nothing else. There is deliberately no fetch handler,
// so the page always talks to the network directly and the server's build
// check (src/boot.ts) stays the only thing deciding when a page is stale.
//
// Plain JS, served unhashed at /sw.js with scope "/". The browser checks it
// for changes on navigation, so an edit here ships like any other.

// A new version takes over at once rather than waiting for every tab to close:
// there is no cache to migrate, so nothing can break by switching mid-session.
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));

// Payload: {title, body, threadId, urgency}. See .omniplex/push-contract.md.
self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    // Not JSON: show the text rather than nothing. With userVisibleOnly every
    // push must show a notification, and browsers penalise ones that don't.
    data = { body: event.data ? event.data.text() : "" };
  }
  const threadId = typeof data.threadId === "string" ? data.threadId : "";
  event.waitUntil(
    self.registration.showNotification(data.title || "Omniplex", {
      body: data.body || "",
      // One notification per thread: a second from the same thread replaces
      // the first, and renotify makes the replacement buzz again.
      tag: threadId || undefined,
      renotify: !!threadId,
      data: { threadId },
      icon: "/icon-192.png",
      // Android draws the badge as a white silhouette in the status bar.
      badge: "/badge-96.png",
      requireInteraction: data.urgency === "high",
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const threadId = (event.notification.data && event.notification.data.threadId) || "";
  event.waitUntil(openThread(threadId));
});

// Prefer a window that is already open: focus it and tell the page which
// thread to show (src/app/useThreadSelection.ts listens). Otherwise open the
// app at /thread/<id>, which the page reads at load.
async function openThread(threadId) {
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  const win = windows.find((c) => c.focused) || windows.find((c) => c.visibilityState === "visible") || windows[0];
  if (win) {
    // Message first: focus can be refused (it needs the click's activation,
    // and some browsers decline anyway), and the thread should still switch.
    if (threadId) win.postMessage({ type: "open-thread", threadId });
    try {
      await win.focus();
    } catch {
      // Already in front, or not allowed; either way nothing more to do.
    }
    return;
  }
  await self.clients.openWindow(threadId ? "/thread/" + encodeURIComponent(threadId) : "/");
}
