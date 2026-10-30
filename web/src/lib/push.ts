// Web push, from the settings side: whether this browser can take it, the
// subscription it holds, and the server's list of devices. Imported only by
// the Notifications settings pane, so none of it rides in the main bundle; the
// boot-time half lives in lib/threadLinks.ts (registering /sw.js, reading a
// tapped notification's link) and app/useThreadSelection.ts (opening it).

/** A browser the server pushes to, as GET /api/push lists it. */
export interface PushDevice {
  id: string;
  endpoint: string;
  label: string;
  deviceId: string;
  /** Milliseconds. */
  createdAt: number;
}

export interface PushState {
  /** The server's VAPID public key, base64url. */
  publicKey: string;
  subscriptions: PushDevice[];
}

export interface PushTestResult {
  sent: number;
  removed: number;
  failed: number;
}

/**
 * Why this page can or cannot subscribe. iOS comes first: Safari there has no
 * PushManager outside a home-screen app, and "install it" is the fix, not
 * "use another browser". Then HTTPS, since an insecure page also lacks the
 * APIs and the address is what to change.
 */
export type PushSupport = "ok" | "ios-install" | "insecure" | "unsupported";

export interface PushEnv {
  userAgent: string;
  maxTouchPoints: number;
  standalone: boolean;
  secure: boolean;
  /** serviceWorker, PushManager and Notification all present. */
  apis: boolean;
}

export function pushSupport(env: PushEnv): PushSupport {
  if (isIOS(env.userAgent, env.maxTouchPoints) && !env.standalone) return "ios-install";
  if (!env.secure) return "insecure";
  if (!env.apis) return "unsupported";
  return "ok";
}

export function readPushEnv(): PushEnv {
  return {
    userAgent: navigator.userAgent,
    maxTouchPoints: navigator.maxTouchPoints ?? 0,
    standalone:
      window.matchMedia?.("(display-mode: standalone)").matches === true ||
      (navigator as { standalone?: boolean }).standalone === true,
    secure: window.isSecureContext,
    apis: "serviceWorker" in navigator && "PushManager" in window && "Notification" in window,
  };
}

/** iPadOS asks for the desktop site, so an iPad says "Macintosh" and only its touch screen gives it away. */
export function isIOS(ua: string, maxTouchPoints: number): boolean {
  return /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && maxTouchPoints > 1);
}

/**
 * A short name for this browser in the device list: "iPhone · Safari",
 * "Android · Chrome", "Mac · Firefox". Order matters below, because every
 * Chromium browser also says "Chrome" and nearly everything says "Safari".
 * A home-screen app on iOS says neither, so it is just "iPhone".
 */
export function deviceLabel(ua: string, maxTouchPoints = 0): string {
  const os = /iPhone|iPod/.test(ua)
    ? "iPhone"
    : isIOS(ua, maxTouchPoints)
      ? "iPad"
      : /Android/.test(ua)
        ? "Android"
        : /CrOS/.test(ua)
          ? "ChromeOS"
          : /Macintosh|Mac OS X/.test(ua)
            ? "Mac"
            : /Windows/.test(ua)
              ? "Windows"
              : /Linux/.test(ua)
                ? "Linux"
                : "";
  const browser = /Edg(A|iOS)?\//.test(ua)
    ? "Edge"
    : /SamsungBrowser\//.test(ua)
      ? "Samsung Internet"
      : /Firefox\/|FxiOS\//.test(ua)
        ? "Firefox"
        : /OPR\//.test(ua)
          ? "Opera"
          : /Chrome\/|CriOS\//.test(ua)
            ? "Chrome"
            : /Version\/.*Safari\//.test(ua)
              ? "Safari"
              : "";
  return [os, browser].filter(Boolean).join(" · ") || "Browser";
}

/** The VAPID key as pushManager.subscribe wants it. Base64url, padding optional. */
export function base64UrlToBytes(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4));
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

/** "Sent to 2 devices", plus whatever didn't go. */
export function testSummary(r: PushTestResult): string {
  const total = r.sent + r.removed + r.failed;
  if (total === 0) return "No devices to send to.";
  const devices = (n: number) => `${n} ${n === 1 ? "device" : "devices"}`;
  const parts = [`Sent to ${devices(r.sent)}`];
  // A 404/410 from the push service: that browser dropped the subscription.
  if (r.removed) parts.push(`removed ${devices(r.removed)} that had expired`);
  if (r.failed) parts.push(`${r.failed} failed`);
  return parts.join(", ") + ".";
}

export class PushHTTPError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, { credentials: "same-origin", ...init });
  // Every push endpoint answers JSON. Anything else (a proxy's HTML page, the
  // dev server's SPA fallback) is a failed request, whatever its status.
  const body = (await res.json().catch(() => null)) as (T & { error?: string }) | null;
  if (!res.ok || body === null) {
    throw new PushHTTPError(body?.error || `Request failed (${res.status})`, res.status);
  }
  return body;
}

export async function fetchPush(): Promise<PushState> {
  const body = await call<Partial<PushState>>("/api/push");
  return { publicKey: body.publicKey ?? "", subscriptions: body.subscriptions ?? [] };
}

export function savePushDevice(sub: PushSubscription, label: string): Promise<{ id: string }> {
  return call("/api/push/subscriptions", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...sub.toJSON(), label }),
  });
}

/** Already gone counts as removed: the point was for it not to be there. */
export async function removePushDevice(id: string): Promise<void> {
  try {
    await call(`/api/push/subscriptions/${encodeURIComponent(id)}`, { method: "DELETE" });
  } catch (e) {
    if (!(e instanceof PushHTTPError && e.status === 404)) throw e;
  }
}

export function sendTestPush(): Promise<PushTestResult> {
  return call("/api/push/test", { method: "POST" });
}

/**
 * This browser's subscription, if it has one. getRegistration rather than
 * `ready`, which never settles when no worker was ever registered.
 */
export async function localSubscription(): Promise<PushSubscription | null> {
  const reg = await navigator.serviceWorker.getRegistration();
  return (await reg?.pushManager.getSubscription()) ?? null;
}

/**
 * Subscribe this browser with the server's key. A subscription it already
 * holds under the same key is kept (same endpoint, nothing for the server to
 * forget); one under another key, from before the server's keys changed,
 * would make subscribe throw, so it goes first.
 */
export async function subscribePush(publicKey: string): Promise<PushSubscription> {
  await navigator.serviceWorker.register("/sw.js");
  const reg = await navigator.serviceWorker.ready;
  const key = base64UrlToBytes(publicKey);
  const old = await reg.pushManager.getSubscription();
  if (old) {
    if (sameBytes(old.options?.applicationServerKey ?? null, key)) return old;
    await old.unsubscribe();
  }
  return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
}

function sameBytes(a: ArrayBuffer | null, b: Uint8Array): boolean {
  if (!a || a.byteLength !== b.length) return false;
  const view = new Uint8Array(a);
  return view.every((x, i) => x === b[i]);
}
