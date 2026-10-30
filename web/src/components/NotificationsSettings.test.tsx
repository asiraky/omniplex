// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { base64UrlToBytes, type PushDevice } from "~/lib/push";
import { render } from "~/test/harness";
import { NotificationsSettings } from "./NotificationsSettings";

const KEY = "BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8U";
const IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1";

const row = (id: string, endpoint: string, label: string): PushDevice => ({
  id,
  endpoint,
  label,
  deviceId: "",
  createdAt: Date.UTC(2026, 9, 1),
});

// The server, in memory: GET lists rows, POST adds one, DELETE drops one.
let rows: PushDevice[];
let failLoad: boolean;
const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
  const method = init?.method ?? "GET";
  const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
  if (url === "/api/push" && method === "GET") {
    if (failLoad) return json({ error: "push is off" }, 500);
    return json({ publicKey: KEY, subscriptions: rows });
  }
  if (url === "/api/push/subscriptions" && method === "POST") {
    const body = JSON.parse(init!.body as string);
    rows = [row("new", body.endpoint, body.label), ...rows];
    return json({ id: "new" });
  }
  const del = /^\/api\/push\/subscriptions\/(.+)$/.exec(url);
  if (del && method === "DELETE") {
    rows = rows.filter((r) => r.id !== del[1]);
    return json({ removed: del[1] });
  }
  if (url === "/api/push/test" && method === "POST") {
    return json({ sent: rows.length, removed: 0, failed: 0 });
  }
  return json({ error: "not found" }, 404);
});

const calls = (method: string, url: string | RegExp) =>
  fetchMock.mock.calls.filter(
    ([u, init]) => (init?.method ?? "GET") === method && (typeof url === "string" ? u === url : url.test(u)),
  );

interface FakeSub {
  endpoint: string;
  options: { applicationServerKey: ArrayBuffer };
  toJSON: () => unknown;
  unsubscribe: ReturnType<typeof vi.fn>;
}
let current: FakeSub | null;
const fakeSub = (endpoint: string, key: ArrayBuffer): FakeSub => ({
  endpoint,
  options: { applicationServerKey: key },
  toJSON: () => ({ endpoint, keys: { p256dh: "p256", auth: "auth" } }),
  unsubscribe: vi.fn(async () => {
    current = null;
    return true;
  }),
});
const pushManager = {
  getSubscription: vi.fn(async () => current),
  subscribe: vi.fn(async (opts: { applicationServerKey: Uint8Array }) => {
    current = fakeSub("https://push.example/new", opts.applicationServerKey.slice().buffer as ArrayBuffer);
    return current;
  }),
};
const requestPermission = vi.fn(async (): Promise<NotificationPermission> => "granted");

/** A browser that can take push: secure page, worker, PushManager, Notification. */
function capable(permission: NotificationPermission = "default") {
  const reg = { pushManager };
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      register: vi.fn(async () => reg),
      ready: Promise.resolve(reg),
      getRegistration: vi.fn(async () => reg),
    },
  });
  vi.stubGlobal("Notification", { permission, requestPermission });
  vi.stubGlobal("PushManager", class {});
  vi.stubGlobal("isSecureContext", true);
}

beforeEach(() => {
  rows = [];
  failLoad = false;
  current = null;
  fetchMock.mockClear();
  pushManager.getSubscription.mockClear();
  pushManager.subscribe.mockClear();
  requestPermission.mockReset().mockResolvedValue("granted");
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete (navigator as { serviceWorker?: unknown }).serviceWorker;
  delete (navigator as { userAgent?: unknown }).userAgent;
});

const toggle = () => screen.getByRole("switch", { name: "Notifications on this device" });
const loaded = () => waitFor(() => expect((toggle() as HTMLButtonElement).disabled).toBe(false));

describe("turning notifications on", () => {
  it("asks within the tap, subscribes with the server's key, and saves this device", async () => {
    capable();
    render(<NotificationsSettings />);
    await loaded();

    fireEvent.click(toggle());
    // Synchronously, inside the click: iOS only prompts from the gesture itself.
    expect(requestPermission).toHaveBeenCalledTimes(1);

    await waitFor(() => expect(toggle().getAttribute("aria-checked")).toBe("true"));
    const [opts] = pushManager.subscribe.mock.calls[0];
    expect(opts).toMatchObject({ userVisibleOnly: true });
    expect([...opts.applicationServerKey]).toEqual([...base64UrlToBytes(KEY)]);

    const [[, post]] = calls("POST", "/api/push/subscriptions");
    expect(JSON.parse(post!.body as string)).toEqual({
      endpoint: "https://push.example/new",
      keys: { p256dh: "p256", auth: "auth" },
      label: expect.any(String),
    });
    const mine = screen.getByRole("listitem");
    expect(within(mine).getByText("This device")).toBeTruthy();
  });

  it("keeps an existing subscription under the same key instead of making a new one", async () => {
    capable("granted");
    current = fakeSub("https://push.example/kept", base64UrlToBytes(KEY).buffer);
    render(<NotificationsSettings />);
    await loaded();
    // Held locally but the server has no row for it: off until saved.
    expect(toggle().getAttribute("aria-checked")).toBe("false");

    fireEvent.click(toggle());
    await waitFor(() => expect(toggle().getAttribute("aria-checked")).toBe("true"));
    expect(pushManager.subscribe).not.toHaveBeenCalled();
    expect(rows.map((r) => r.endpoint)).toEqual(["https://push.example/kept"]);
  });

  it("replaces a subscription made under an old key", async () => {
    capable("granted");
    const stale = fakeSub("https://push.example/stale", new Uint8Array([1, 2, 3]).buffer);
    current = stale;
    render(<NotificationsSettings />);
    await loaded();

    fireEvent.click(toggle());
    await waitFor(() => expect(toggle().getAttribute("aria-checked")).toBe("true"));
    expect(stale.unsubscribe).toHaveBeenCalled();
    expect(rows.map((r) => r.endpoint)).toEqual(["https://push.example/new"]);
  });

  it("subscribes nothing when permission is refused, and says how to undo it", async () => {
    capable();
    requestPermission.mockResolvedValue("denied");
    render(<NotificationsSettings />);
    await loaded();

    fireEvent.click(toggle());
    await waitFor(() => expect(screen.getByText(/Blocked for this site/)).toBeTruthy());
    expect(pushManager.subscribe).not.toHaveBeenCalled();
    expect(calls("POST", "/api/push/subscriptions")).toHaveLength(0);
    expect(toggle().getAttribute("aria-checked")).toBe("false");
  });

  it("shows why when subscribing fails, and stays off", async () => {
    capable();
    pushManager.subscribe.mockRejectedValueOnce(new Error("push service error"));
    render(<NotificationsSettings />);
    await loaded();

    fireEvent.click(toggle());
    expect((await screen.findByRole("alert")).textContent).toBe("push service error");
    expect(calls("POST", "/api/push/subscriptions")).toHaveLength(0);
    expect(toggle().getAttribute("aria-checked")).toBe("false");
  });
});

describe("devices", () => {
  it("turning off removes the server's row and unsubscribes this browser", async () => {
    capable("granted");
    const sub = fakeSub("https://push.example/me", base64UrlToBytes(KEY).buffer);
    current = sub;
    rows = [row("r1", "https://push.example/me", "Mac · Chrome"), row("r2", "https://push.example/phone", "iPhone")];
    render(<NotificationsSettings />);
    await waitFor(() => expect(toggle().getAttribute("aria-checked")).toBe("true"));

    fireEvent.click(toggle());
    await waitFor(() => expect(toggle().getAttribute("aria-checked")).toBe("false"));
    expect(calls("DELETE", "/api/push/subscriptions/r1")).toHaveLength(1);
    expect(sub.unsubscribe).toHaveBeenCalled();
    expect(screen.getAllByRole("listitem")).toHaveLength(1);
  });

  it("removes another device without touching this browser's subscription", async () => {
    capable("granted");
    const sub = fakeSub("https://push.example/me", base64UrlToBytes(KEY).buffer);
    current = sub;
    rows = [row("r1", "https://push.example/me", "Mac · Chrome"), row("r2", "https://push.example/phone", "iPhone")];
    render(<NotificationsSettings />);
    await screen.findByText("iPhone");

    fireEvent.click(screen.getByRole("button", { name: "Remove iPhone" }));
    await waitFor(() => expect(screen.queryByText("iPhone")).toBeNull());
    expect(calls("DELETE", "/api/push/subscriptions/r2")).toHaveLength(1);
    expect(sub.unsubscribe).not.toHaveBeenCalled();
    expect(toggle().getAttribute("aria-checked")).toBe("true");
  });

  it("marks only the row matching this browser as this device", async () => {
    capable("granted");
    current = fakeSub("https://push.example/me", base64UrlToBytes(KEY).buffer);
    rows = [row("r1", "https://push.example/phone", "iPhone"), row("r2", "https://push.example/me", "Mac · Chrome")];
    render(<NotificationsSettings />);
    await screen.findByText("This device");
    const [phone, mac] = screen.getAllByRole("listitem");
    expect(within(phone).queryByText("This device")).toBeNull();
    expect(within(mac).getByText("This device")).toBeTruthy();
  });

  it("sends a test and reports how many devices got it", async () => {
    capable();
    rows = [row("r1", "https://push.example/a", "iPhone"), row("r2", "https://push.example/b", "Mac · Chrome")];
    render(<NotificationsSettings />);
    fireEvent.click(await screen.findByRole("button", { name: "Send test notification" }));
    expect((await screen.findByRole("status")).textContent).toBe("Sent to 2 devices.");
    expect(calls("POST", "/api/push/test")).toHaveLength(1);
  });

  it("offers a retry when the list fails to load", async () => {
    capable();
    failLoad = true;
    render(<NotificationsSettings />);
    expect((await screen.findByRole("alert")).textContent).toBe("push is off");
    // Nothing to subscribe with until the key has arrived.
    expect((toggle() as HTMLButtonElement).disabled).toBe(true);

    failLoad = false;
    rows = [row("r1", "https://push.example/a", "iPhone")];
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Retry" })));
    expect(await screen.findByText("iPhone")).toBeTruthy();
    await loaded();
  });
});

describe("where push can't work", () => {
  it("on iOS outside the home-screen app, says to install it, and still lists devices", async () => {
    capable();
    Object.defineProperty(navigator, "userAgent", { value: IPHONE, configurable: true });
    rows = [row("r1", "https://push.example/a", "Mac · Chrome")];
    render(<NotificationsSettings />);

    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.getByText(/Add to Home Screen/)).toBeTruthy();
    expect(await screen.findByText("Mac · Chrome")).toBeTruthy();
  });

  it("on an insecure page, says HTTPS is needed", () => {
    // What an http:// page looks like: no worker, no PushManager.
    vi.stubGlobal("isSecureContext", false);
    render(<NotificationsSettings />);
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.getByText(/HTTPS/)).toBeTruthy();
  });

  it("in a browser without push, says so", () => {
    vi.stubGlobal("isSecureContext", true);
    render(<NotificationsSettings />);
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.getByText(/can't receive push/)).toBeTruthy();
  });
});
