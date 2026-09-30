import { describe, expect, it } from "vitest";
import { classifyNavigation, initialRoute, parseSetup } from "./routing";

describe("initialRoute", () => {
  it("shows setup on the first launch even when everything is installed", () => {
    expect(initialRoute({ firstRun: true, setup: { ready: true } })).toBe("/setup");
  });

  it("shows setup whenever something is missing", () => {
    expect(initialRoute({ firstRun: false, setup: { ready: false } })).toBe("/setup");
  });

  it("goes to the app once set up", () => {
    expect(initialRoute({ firstRun: false, setup: { ready: true } })).toBe("/");
  });

  it("goes to the app when the server has no setup endpoint", () => {
    expect(initialRoute({ firstRun: true, setup: null })).toBe("/");
  });
});

describe("parseSetup", () => {
  it("reads ready from the setup response", () => {
    expect(parseSetup({ platform: "darwin", ready: false, checks: [] })).toEqual({ ready: false });
  });

  it("rejects anything without a boolean ready", () => {
    expect(parseSetup(null)).toBeNull();
    expect(parseSetup({ error: "pair this device first" })).toBeNull();
    expect(parseSetup({ ready: "yes" })).toBeNull();
  });
});

describe("classifyNavigation", () => {
  const origin = "http://127.0.0.1:8787";

  it("keeps navigation within the server in the window", () => {
    expect(classifyNavigation(`${origin}/threads/1`, origin)).toEqual({ kind: "allow" });
  });

  it("sends other sites to the browser", () => {
    expect(classifyNavigation("https://github.com/asiraky/omniplex", origin)).toEqual({
      kind: "external",
      url: "https://github.com/asiraky/omniplex",
    });
    expect(classifyNavigation("http://127.0.0.1:9999/", origin).kind).toBe("external");
  });

  it("treats the server as external while the window is on a built-in page", () => {
    expect(classifyNavigation(`${origin}/`, null).kind).toBe("external");
  });

  it("turns the built-in pages' buttons into actions", () => {
    expect(classifyNavigation("omniplex-desktop://retry", null)).toEqual({ kind: "action", action: "retry" });
    expect(classifyNavigation("omniplex-desktop://show-log", origin)).toEqual({ kind: "action", action: "show-log" });
    expect(classifyNavigation("omniplex-desktop://format-disk", null)).toEqual({ kind: "block" });
  });

  it("blocks schemes that could run things", () => {
    expect(classifyNavigation("file:///etc/passwd", origin)).toEqual({ kind: "block" });
    expect(classifyNavigation("javascript:alert(1)", origin)).toEqual({ kind: "block" });
    expect(classifyNavigation("not a url", origin)).toEqual({ kind: "block" });
  });
});
