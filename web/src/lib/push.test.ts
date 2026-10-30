import { afterEach, describe, expect, it, vi } from "vitest";

import { base64UrlToBytes, deviceLabel, fetchPush, pushSupport, testSummary, type PushEnv } from "./push";

const UA = {
  iphoneSafari:
    "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
  iphoneHomeScreen:
    "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148",
  iphoneChrome:
    "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/126.0.6478.54 Mobile/15E148 Safari/604.1",
  ipadDesktopMode:
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
  androidChrome:
    "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36",
  androidSamsung:
    "Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0.0.0 Mobile Safari/537.36",
  macChrome:
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
  macFirefox: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14.5; rv:127.0) Gecko/20100101 Firefox/127.0",
  windowsEdge:
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0",
  linuxChrome:
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
};

describe("deviceLabel", () => {
  it.each([
    [UA.iphoneSafari, 5, "iPhone · Safari"],
    [UA.iphoneHomeScreen, 5, "iPhone"],
    [UA.iphoneChrome, 5, "iPhone · Chrome"],
    [UA.ipadDesktopMode, 5, "iPad · Safari"],
    [UA.ipadDesktopMode, 0, "Mac · Safari"],
    [UA.androidChrome, 5, "Android · Chrome"],
    [UA.androidSamsung, 5, "Android · Samsung Internet"],
    [UA.macChrome, 0, "Mac · Chrome"],
    [UA.macFirefox, 0, "Mac · Firefox"],
    [UA.windowsEdge, 0, "Windows · Edge"],
    [UA.linuxChrome, 0, "Linux · Chrome"],
    ["curl/8.0", 0, "Browser"],
  ])("names %s (%i touch points) as %s", (ua, touch, want) => {
    expect(deviceLabel(ua, touch)).toBe(want);
  });
});

describe("pushSupport", () => {
  const env = (over: Partial<PushEnv>): PushEnv => ({
    userAgent: UA.macChrome,
    maxTouchPoints: 0,
    standalone: false,
    secure: true,
    apis: true,
    ...over,
  });

  it("asks iOS to install to the home screen, ahead of any other reason", () => {
    expect(pushSupport(env({ userAgent: UA.iphoneSafari, apis: false }))).toBe("ios-install");
    expect(pushSupport(env({ userAgent: UA.iphoneSafari, secure: false }))).toBe("ios-install");
    // An iPad in desktop mode is still iOS.
    expect(pushSupport(env({ userAgent: UA.ipadDesktopMode, maxTouchPoints: 5 }))).toBe(
      "ios-install",
    );
  });

  it("lets the home-screen app on iOS through", () => {
    expect(pushSupport(env({ userAgent: UA.iphoneHomeScreen, standalone: true }))).toBe("ok");
  });

  it("blames the address before the browser when the page is insecure", () => {
    expect(pushSupport(env({ secure: false, apis: false }))).toBe("insecure");
  });

  it("says the browser lacks push when the APIs are missing on a secure page", () => {
    expect(pushSupport(env({ apis: false }))).toBe("unsupported");
    expect(pushSupport(env({ userAgent: UA.iphoneHomeScreen, standalone: true, apis: false }))).toBe(
      "unsupported",
    );
  });

  it("is fine on a desktop browser over HTTPS", () => {
    expect(pushSupport(env({}))).toBe("ok");
  });
});

describe("base64UrlToBytes", () => {
  it("decodes the URL-safe alphabet, with or without padding", () => {
    // 0xfb 0xff 0xbf is "+/+/" in plain base64 and "-_-_" in base64url.
    expect([...base64UrlToBytes("-_-_")]).toEqual([0xfb, 0xff, 0xbf]);
    expect([...base64UrlToBytes("AQ")]).toEqual([1]);
    expect([...base64UrlToBytes("AQ==")]).toEqual([1]);
    expect([...base64UrlToBytes("AQI")]).toEqual([1, 2]);
  });

  it("round-trips a 65-byte P-256 public key", () => {
    const key = Uint8Array.from({ length: 65 }, (_, i) => (i * 37 + 4) & 0xff);
    const encoded = btoa(String.fromCharCode(...key))
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
    expect([...base64UrlToBytes(encoded)]).toEqual([...key]);
  });
});

describe("testSummary", () => {
  it("counts devices, singular and plural", () => {
    expect(testSummary({ sent: 1, removed: 0, failed: 0 })).toBe("Sent to 1 device.");
    expect(testSummary({ sent: 3, removed: 0, failed: 0 })).toBe("Sent to 3 devices.");
  });

  it("says what didn't go", () => {
    expect(testSummary({ sent: 1, removed: 2, failed: 1 })).toBe(
      "Sent to 1 device, removed 2 devices that had expired, 1 failed.",
    );
  });

  it("says when there was nobody to send to", () => {
    expect(testSummary({ sent: 0, removed: 0, failed: 0 })).toBe("No devices to send to.");
  });
});

describe("fetchPush", () => {
  afterEach(() => vi.unstubAllGlobals());
  const answer = (body: string, status = 200) =>
    vi.stubGlobal("fetch", vi.fn(async () => new Response(body, { status })));

  it("reads the key and devices", async () => {
    answer(JSON.stringify({ publicKey: "k", subscriptions: [] }));
    await expect(fetchPush()).resolves.toEqual({ publicKey: "k", subscriptions: [] });
  });

  it("fails cleanly when a 200 is not JSON, as from an HTML fallback", async () => {
    answer("<!doctype html><title>app</title>");
    await expect(fetchPush()).rejects.toThrow("Request failed (200)");
  });

  it("surfaces the server's error message", async () => {
    answer(JSON.stringify({ error: "push is off" }), 501);
    await expect(fetchPush()).rejects.toMatchObject({ message: "push is off", status: 501 });
  });
});
