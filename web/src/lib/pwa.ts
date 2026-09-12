// Registering the service worker, and keeping it current.
//
// There are two update stories in this app and they have to agree. The
// document is served no-cache and carries the build id, so a running tab
// notices a rebuild over the WebSocket and reloads itself (src/boot.ts). The
// service worker is a second copy of the app that the browser updates on its
// own schedule — which, left alone, can be a day. This module makes the second
// follow the first: when we have reason to think the server changed, the
// worker is asked to check, and a new one is allowed to take over immediately.
//
// The alternative — a "new version available, reload?" prompt — was not worth
// building. There is one user, no unsaved state in a transcript, and the app
// already reloads itself on a build change without asking.

/** How often an open tab asks the browser to look for a new worker. */
const UPDATE_INTERVAL_MS = 30 * 60 * 1000;

let registration: ServiceWorkerRegistration | null = null;

/** Whether this browser can run a service worker at all. */
export function serviceWorkerSupported(): boolean {
  return typeof navigator !== "undefined" && "serviceWorker" in navigator;
}

/**
 * Registers the worker and keeps it up to date.
 *
 * Safe to call on every boot; the browser treats re-registering the same URL
 * as a no-op plus an update check.
 */
export async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!serviceWorkerSupported()) return null;

  // Captured before registering. A page that already had a controller and
  // then gets a new one has been updated underneath itself and must reload;
  // a page that had none is simply being claimed for the first time, and
  // reloading there would throw away whatever the user was doing on the very
  // first visit after install.
  const hadController = Boolean(navigator.serviceWorker.controller);

  try {
    registration = await navigator.serviceWorker.register("/sw.js", { scope: "/" });
  } catch {
    // A worker that will not register is not a reason to fail the app: every
    // one of its jobs is an enhancement. Notifications will report themselves
    // as unavailable, and the app runs exactly as it did before.
    return null;
  }

  // A worker sitting in "waiting" means a new build installed while this tab
  // was open. Nothing here needs the old one, so it is told to take over.
  registration.addEventListener("updatefound", () => {
    const installing = registration?.installing;
    if (!installing) return;
    installing.addEventListener("statechange", () => {
      if (installing.state === "installed" && navigator.serviceWorker.controller) {
        installing.postMessage({ type: "omniplex:skip-waiting" });
      }
    });
  });

  // A worker taking over mid-session leaves the page running code from the
  // build before it. Reload once — guarded, because a reload that lands on
  // another new worker would otherwise loop.
  let reloading = false;
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    if (reloading || !hadController) return;
    reloading = true;
    location.reload();
  });

  // Check on a timer, and whenever the app comes back to the foreground. The
  // second is what matters on a phone: a tab is suspended for hours and the
  // moment it is looked at again is exactly when a stale bundle would show.
  setInterval(() => void checkForUpdate(), UPDATE_INTERVAL_MS);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") void checkForUpdate();
  });

  return registration;
}

/**
 * Asks the browser to look for a new service worker.
 *
 * Called on a timer, on returning to the app, and by the build-mismatch check
 * — if the server says it is running a different bundle, the worker is by
 * definition stale and there is no reason to wait for the timer.
 */
export async function checkForUpdate(): Promise<void> {
  try {
    await registration?.update();
  } catch {
    // Offline, most likely. The next check will do.
  }
  // update() only re-fetches sw.js, and sw.js does not change when the bundle
  // does — it reads the hashed names from the manifest instead. So the worker
  // is asked separately to reconcile its cache with the current build; without
  // this, caches from every past build would pile up untouched.
  registration?.active?.postMessage({ type: "omniplex:sync-cache" });
}

/** The registration, once there is one. */
export function currentRegistration(): ServiceWorkerRegistration | null {
  return registration;
}

/** What the service worker sends the page. */
export interface WorkerNotification {
  kind?: string;
  title?: string;
  body?: string;
  sessionId?: string | null;
}

export interface WorkerHandlers {
  /** A push arrived while a window was visible — show it in the app instead. */
  onNotification(note: WorkerNotification): void;
  /** A notification was tapped; bring this session up. */
  onOpenSession(sessionId: string | null): void;
}

/** Routes messages from the service worker into the app. */
export function listenToWorker(handlers: WorkerHandlers): () => void {
  if (!serviceWorkerSupported()) return () => {};

  const onMessage = (event: MessageEvent) => {
    const data = event.data;
    if (!data || typeof data !== "object") return;
    if (data.type === "omniplex:notification") handlers.onNotification(data.notification ?? {});
    if (data.type === "omniplex:open-session") handlers.onOpenSession(data.sessionId ?? null);
  };

  navigator.serviceWorker.addEventListener("message", onMessage);
  return () => navigator.serviceWorker.removeEventListener("message", onMessage);
}

/**
 * The session a notification was tapped to open, when the app had to be
 * started to show it.
 *
 * Read once and removed from the address bar: a session id left in the URL
 * would be restored ahead of the user's actual last session on every
 * subsequent reload, and this app has no routing for it to belong to.
 */
export function sessionFromLaunchURL(): string | null {
  const params = new URLSearchParams(window.location.search);
  const id = params.get("session");
  if (!id) return null;
  params.delete("session");
  const query = params.toString();
  window.history.replaceState(
    null,
    "",
    window.location.pathname + (query ? `?${query}` : "") + window.location.hash,
  );
  return id;
}

/**
 * Whether the app is running as an installed PWA rather than in a browser tab.
 *
 * iOS only delivers push to an app that has been added to the home screen, so
 * the settings UI has to be able to say so rather than letting the user press
 * a button that silently cannot work.
 */
export function isInstalled(): boolean {
  return (
    window.matchMedia("(display-mode: standalone)").matches ||
    // Safari's own, non-standard, and the only signal on iOS.
    (window.navigator as { standalone?: boolean }).standalone === true
  );
}
