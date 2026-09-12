// Subscribing this browser to Web Push.
//
// Three separate pieces of state have to agree before a notification can
// arrive, and each can change without the others knowing:
//
//   1. The browser's notification permission for this origin.
//   2. A PushSubscription held by the service worker registration.
//   3. A row on the server saying where to send to.
//
// Any one of them can be true while the others are false — permission survives
// the subscription being dropped, a subscription survives the server database
// being replaced, and the server's row survives the browser's data being
// cleared. So `status()` asks all three and the UI reports the conjunction,
// rather than showing a toggle based on permission and quietly lying.

import { currentRegistration, isInstalled, serviceWorkerSupported } from "./pwa";

export type PushState =
  | "unsupported" // no service worker or no Push API in this browser
  | "needs-install" // iOS: works, but only from the home screen
  | "denied" // the user said no; only they can undo it, in browser settings
  | "off" // available, not subscribed
  | "on"; // subscribed here and known to the server

export interface PushStatus {
  state: PushState;
  /** Why it cannot be turned on, when that needs explaining. */
  reason?: string;
}

interface KeyResponse {
  available: boolean;
  publicKey?: string;
  endpoints?: string[];
}

/**
 * The VAPID public key travels as base64url and has to reach the browser as
 * bytes. This conversion is the single most common reason a subscription is
 * rejected with an opaque error, so it is one function used by one caller.
 */
function decodeKey(base64url: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (base64url.length % 4)) % 4);
  const base64 = (base64url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  // Backed by an explicit ArrayBuffer: applicationServerKey will not take a
  // view that might sit on a SharedArrayBuffer, which is what a bare
  // Uint8Array is typed as.
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function fetchKey(): Promise<KeyResponse> {
  const res = await fetch("/api/push/key", { credentials: "same-origin" });
  if (!res.ok) throw new Error(`push key: ${res.status}`);
  return res.json();
}

/** What this browser supports, before asking the server anything. */
function capability(): PushState | null {
  if (!serviceWorkerSupported() || typeof PushManager === "undefined") {
    return "unsupported";
  }
  if (typeof Notification === "undefined") return "unsupported";
  // iOS grants neither permission nor a subscription to a page in a Safari
  // tab, whatever the API surface suggests. Saying "add to home screen" is far
  // better than a button that fails with a DOMException.
  if (isIOS() && !isInstalled()) return "needs-install";
  if (Notification.permission === "denied") return "denied";
  return null;
}

function isIOS(): boolean {
  const ua = navigator.userAgent;
  // iPadOS reports itself as a Mac; the touch-point count is what separates
  // it from a desktop Safari that does not need the home-screen step.
  return /iPad|iPhone|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1);
}

/**
 * Whether this device is worth asking, unprompted, to turn notifications on.
 *
 * Only when the browser could actually grant it and the user has not already
 * answered. A device that said no is not asked again by the app: the browser
 * will not re-prompt after a denial anyway, so the invitation would be a
 * button that silently does nothing.
 */
export function invitable(): boolean {
  if (capability() !== null) return false;
  return Notification.permission === "default";
}

/** Reports whether notifications are actually live on this device. */
export async function status(): Promise<PushStatus> {
  const blocked = capability();
  if (blocked === "unsupported") {
    return { state: "unsupported", reason: "This browser cannot receive push notifications." };
  }
  if (blocked === "needs-install") {
    return {
      state: "needs-install",
      reason: "On iOS, add Omniplex to your home screen first — Safari tabs cannot receive push.",
    };
  }
  if (blocked === "denied") {
    return {
      state: "denied",
      reason: "Notifications are blocked for this site. Allow them in your browser settings.",
    };
  }

  const registration = currentRegistration() ?? (await navigator.serviceWorker.ready);
  const subscription = await registration.pushManager.getSubscription();
  if (!subscription || Notification.permission !== "granted") return { state: "off" };

  // The browser holding a subscription is not enough. If the server has never
  // heard of this endpoint — a restored database, a revoked device — nothing
  // will ever be sent to it, and reporting "on" would be a lie the user only
  // discovers by not being notified.
  const key = await fetchKey();
  if (!key.available) {
    return { state: "off", reason: "The server has no push keys." };
  }
  if (!key.endpoints?.includes(subscription.endpoint)) return { state: "off" };

  return { state: "on" };
}

/**
 * Turns notifications on for this device.
 *
 * Asks for permission, subscribes, and registers the result. Throws with
 * something worth showing the user: every failure here is one they can act on.
 */
export async function enable(): Promise<PushStatus> {
  const blocked = capability();
  if (blocked) return status();

  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    return {
      state: permission === "denied" ? "denied" : "off",
      reason: "Permission was not granted.",
    };
  }

  const key = await fetchKey();
  if (!key.available || !key.publicKey) throw new Error("The server has no push keys.");

  const registration = currentRegistration() ?? (await navigator.serviceWorker.ready);

  let subscription = await registration.pushManager.getSubscription();
  // A subscription minted against a different VAPID key can never be
  // decrypted by its owner, and the browser will not silently re-key it. If
  // the server's keys changed, the old one has to go first.
  if (subscription && !(await matchesKey(subscription, key.publicKey))) {
    await subscription.unsubscribe();
    subscription = null;
  }
  if (!subscription) {
    subscription = await registration.pushManager.subscribe({
      // Required to be true, and enforced: a browser will revoke a
      // subscription whose pushes do not result in a visible notification.
      // The service worker's push handler is written to always show one.
      userVisibleOnly: true,
      applicationServerKey: decodeKey(key.publicKey),
    });
  }

  const json = subscription.toJSON();
  const res = await fetch("/api/push/subscribe", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ endpoint: subscription.endpoint, keys: json.keys }),
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error ?? `Could not register: ${res.status}`);
  }

  return { state: "on" };
}

/** Whether a subscription was minted against the key the server holds now. */
async function matchesKey(subscription: PushSubscription, publicKey: string): Promise<boolean> {
  const held = subscription.options?.applicationServerKey;
  if (!held) return true; // Nothing to compare; assume it is fine.
  const wanted = decodeKey(publicKey);
  const bytes = new Uint8Array(held as ArrayBuffer);
  if (bytes.length !== wanted.length) return false;
  return bytes.every((b, i) => b === wanted[i]);
}

/**
 * Turns notifications off for this device.
 *
 * The server is told first. Unsubscribing in the browser destroys the endpoint
 * we would name, so doing it the other way round leaves a row the server keeps
 * pushing to until the push service reports it gone.
 */
export async function disable(): Promise<PushStatus> {
  const registration = currentRegistration() ?? (await navigator.serviceWorker.ready);
  const subscription = await registration.pushManager.getSubscription();

  await fetch("/api/push/unsubscribe", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ endpoint: subscription?.endpoint ?? "" }),
  }).catch(() => {
    // Offline. The local subscription still goes, because the user asked for
    // it to; the server prunes the endpoint the first time a send fails.
  });

  await subscription?.unsubscribe();
  return { state: "off" };
}

/** Sends this device a test notification, end to end through the push service. */
export async function sendTest(): Promise<void> {
  const res = await fetch("/api/push/test", { method: "POST", credentials: "same-origin" });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error ?? `Test failed: ${res.status}`);
  }
}
