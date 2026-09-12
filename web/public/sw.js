/// <reference lib="webworker" />

// The service worker.
//
// Plain JavaScript in public/, not TypeScript in src/, on purpose. A service
// worker shares no code with the app, has to be served from the origin root to
// claim the whole scope, and must keep a stable URL across builds — three
// things that fight a bundler. Written this way it is copied verbatim by Vite,
// served identically by the dev server and the Go binary, and there is no
// build step that can be the reason notifications stopped working.
//
// It does two jobs that have nothing to do with each other:
//
//   1. Caching, so the app opens on a train with no signal.
//   2. Push, which is the only way a browser can be told something while its
//      tab is closed. A push event cannot be handled anywhere else.

const CACHE_PREFIX = "omniplex-";

// Requests that must never be served from, or written to, a cache. The API is
// live state and the socket is not a request that finishes; answering either
// from a cache would show a transcript from yesterday and call it current.
function isBypassed(url) {
  return (
    url.pathname.startsWith("/api/") ||
    url.pathname === "/ws" ||
    url.pathname === "/pair" ||
    url.pathname === "/sw.js"
  );
}

// The build the caches belong to. Assets carry a content hash in their names,
// so the manifest's contents change exactly when the bundle does.
async function readManifest() {
  // no-store, not no-cache: this is the file that decides whether we are
  // running a stale bundle, so it must never itself be answered from one.
  const res = await fetch("/asset-manifest.json", { cache: "no-store" });
  if (!res.ok) throw new Error(`asset manifest: ${res.status}`);
  return res.json();
}

/**
 * Brings the caches into line with the manifest: precache the current shell,
 * drop every older build.
 *
 * This is not only an install step. This file is static, so the browser only
 * ever sees one version of it and only reinstalls the worker when its bytes
 * change — which a normal rebuild does not do. If pruning happened solely at
 * activate, the one cache would accumulate every asset of every build ever
 * shipped and nothing would ever remove them. So the page asks for this
 * whenever it checks for an update, and the version in the manifest, not the
 * worker's lifecycle, is what decides which cache is current.
 */
async function syncCaches() {
  let manifest;
  try {
    manifest = await readManifest();
  } catch {
    // Offline, or the Vite dev server, which has no manifest and no hashed
    // bundle. Either way this is the wrong moment to throw caches away:
    // pruning here would delete the offline shell exactly when being offline
    // is the reason it is needed.
    return;
  }

  const keep = CACHE_PREFIX + manifest.version;
  const cache = await caches.open(keep);

  // Only fetch what is missing. Most of a rebuild's shell is byte-identical,
  // and this runs on a connection assumed to be metered and slow.
  const held = new Set((await cache.keys()).map((r) => new URL(r.url).pathname));
  const missing = manifest.precache.filter((url) => !held.has(url));

  // The document is always refetched: its URL never changes, so a hit here
  // means nothing, and a stale copy names asset hashes the last build deleted.
  // It is fetched alongside the shell so the offline case has something to
  // render at all.
  //
  // Only the shell is precached; lazy chunks are immutable and land in this
  // same cache the first time they are actually asked for, so a phone never
  // pays for a route it never opens.
  await cache.addAll([...missing, "/"]);

  const names = await caches.keys();
  await Promise.all(
    names
      .filter((name) => name.startsWith(CACHE_PREFIX) && name !== keep)
      .map((name) => caches.delete(name)),
  );
}

self.addEventListener("install", (event) => {
  event.waitUntil(
    (async () => {
      // Failing to precache must not fail the install: push has nothing to do
      // with caching, and refusing to install would take notifications down
      // with it during development.
      await syncCaches().catch(() => {});
      // Take over as soon as the new worker is ready rather than waiting for
      // every tab to close. The app already reloads itself when the server
      // reports a different build (see src/boot.ts), so waiting would only
      // mean the reload lands on the old worker.
      await self.skipWaiting();
    })(),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      await syncCaches().catch(() => {});
      // Control the pages that are already open, so the very first load after
      // an install is served by this worker rather than the next one.
      await self.clients.claim();
    })(),
  );
});

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin || isBypassed(url)) return;

  // A navigation is the document. Network first, because the document is what
  // names the current asset hashes and a stale one points at files the last
  // build deleted — the failure mode webassets.go exists to prevent. The
  // cache is only a fallback for having no network at all.
  if (request.mode === "navigate") {
    event.respondWith(
      (async () => {
        try {
          return await fetch(request);
        } catch {
          const cached = await caches.match("/", { ignoreSearch: true });
          return cached ?? Response.error();
        }
      })(),
    );
    return;
  }

  // Hashed assets are immutable: the name changes when the content does, so a
  // hit is always correct and is never revalidated.
  if (url.pathname.startsWith("/assets/")) {
    event.respondWith(
      (async () => {
        const cached = await caches.match(request);
        if (cached) return cached;
        const res = await fetch(request);
        if (res.ok) {
          const cache = await caches.open(await currentCacheName());
          cache.put(request, res.clone());
        }
        return res;
      })(),
    );
    return;
  }

  // Everything else — icons, the manifest, the favicon. Serve what we have
  // and refresh it behind the request, so a changed icon lands on the next
  // load without any of them costing a round trip on a slow connection.
  event.respondWith(
    (async () => {
      const cached = await caches.match(request);
      const network = fetch(request)
        .then(async (res) => {
          if (res.ok) {
            const cache = await caches.open(await currentCacheName());
            cache.put(request, res.clone());
          }
          return res;
        })
        .catch(() => cached ?? Response.error());
      return cached ?? network;
    })(),
  );
});

// The cache this build writes to. Cached rather than re-fetched per request:
// the manifest is small, but asking for it on every asset miss would be a
// request per request.
let cacheNamePromise = null;
function currentCacheName() {
  if (!cacheNamePromise) {
    cacheNamePromise = readManifest()
      .then((m) => CACHE_PREFIX + m.version)
      .catch(() => CACHE_PREFIX + "dev");
  }
  return cacheNamePromise;
}

// ---- Push ----

// How long a push is worth showing. A phone that was off overnight gets its
// backlog delivered the moment it comes back, and being told a turn finished
// eight hours ago is worse than not being told: it reads as current.
const MAX_AGE_MS = 6 * 60 * 60 * 1000;

self.addEventListener("push", (event) => {
  event.waitUntil(
    (async () => {
      let msg = {};
      try {
        msg = event.data ? event.data.json() : {};
      } catch {
        // A push with no payload, or one we cannot read. The spec requires a
        // notification to be shown for every push event or the browser shows
        // its own generic one and, on repeat, revokes the subscription — so
        // fall through to the defaults below rather than returning.
      }

      const stale = Boolean(msg.sentAt) && Date.now() - msg.sentAt > MAX_AGE_MS;

      // Someone may have walked to their desk between the send and the
      // delivery. A visible window means they are already looking, so this
      // becomes an in-app message instead of a banner over what they are doing.
      //
      // This is checked before staleness, not after: a push that arrives late
      // is still a push that must not banner over somebody who is looking at
      // the app — that is the case they would find most obviously wrong.
      const clients = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });
      const visible = clients.filter((c) => c.visibilityState === "visible");
      if (visible.length > 0) {
        // Stale news is not worth a toast either. The app in front of them is
        // live and already shows whatever this was going to announce.
        if (!stale) {
          for (const client of visible) {
            client.postMessage({ type: "omniplex:notification", notification: msg });
          }
        }
        // A notification is still required for every push event. Showing one
        // and closing it immediately is the accepted way to satisfy that
        // without putting a banner in front of someone who is looking at the
        // thing it is about.
        await self.registration.showNotification(msg.title || "Omniplex", {
          tag: msg.tag || "omniplex",
          silent: true,
        });
        const shown = await self.registration.getNotifications({
          tag: msg.tag || "omniplex",
        });
        shown.forEach((n) => n.close());
        return;
      }

      if (stale) {
        // Nothing is open and the news has aged out. Something still has to be
        // shown, so point at the app rather than announcing as current a turn
        // that finished before breakfast.
        await self.registration.showNotification("Omniplex", {
          body: "You have activity waiting.",
          tag: "omniplex-stale",
          icon: "/icon-192.png",
          badge: "/icon-192.png",
        });
        return;
      }

      await self.registration.showNotification(msg.title || "Omniplex", {
        body: msg.body || "",
        tag: msg.tag || "omniplex",
        renotify: Boolean(msg.renotify) && Boolean(msg.tag),
        icon: "/icon-192.png",
        badge: "/icon-192.png",
        timestamp: msg.sentAt || Date.now(),
        data: { sessionId: msg.sessionId || null, kind: msg.kind || null },
      });
    })(),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const sessionId = event.notification.data?.sessionId ?? null;

  event.waitUntil(
    (async () => {
      const clients = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });

      // Reuse a window if there is one. Opening a second copy of a
      // single-page app loses whatever was on screen in the first, and on a
      // phone leaves two identical entries in the app switcher.
      for (const client of clients) {
        if (new URL(client.url).origin !== self.location.origin) continue;
        client.postMessage({ type: "omniplex:open-session", sessionId });
        if ("focus" in client) return client.focus();
        return;
      }

      // Nothing open. The session travels in the URL because there is no
      // client yet to post a message to; the app reads it on boot and takes
      // it back out of the address bar.
      const target = sessionId ? `/?session=${encodeURIComponent(sessionId)}` : "/";
      await self.clients.openWindow(target);
    })(),
  );
});

// The page asks for this when it has decided a waiting worker should take
// over now — see src/lib/pwa.ts.
self.addEventListener("message", (event) => {
  if (event.data?.type === "omniplex:skip-waiting") self.skipWaiting();
  // A rebuild does not change this file, so nothing would otherwise tell the
  // worker its cache is a build behind. See syncCaches.
  if (event.data?.type === "omniplex:sync-cache") {
    event.waitUntil(syncCaches().catch(() => {}));
  }
});
