// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  harnessPrefs,
  loadSessionPrefs,
  projectPrefs,
  saveSessionPrefs,
} from "./sessionPrefs";

const KEY = "omniplex.sessionPrefs.v1";

describe("session prefs", () => {
  beforeEach(() => localStorage.clear());

  it("round-trips what a session was started with", () => {
    saveSessionPrefs("p1", {
      harness: "codex",
      workspace: "managed",
      model: "gpt-5.6-sol",
      effort: "xhigh",
      mode: "full-access",
    });

    expect(projectPrefs("p1")).toEqual({
      harness: "codex",
      workspace: "managed",
      byHarness: {
        codex: { model: "gpt-5.6-sol", effort: "xhigh", mode: "full-access" },
      },
    });
    expect(harnessPrefs("p1", "codex").model).toBe("gpt-5.6-sol");
  });

  // The whole reason it is keyed by harness: switching harness has to restore
  // that harness's own names, not overwrite them with the other's.
  it("keeps each harness's settings apart", () => {
    saveSessionPrefs("p1", { harness: "claude", mode: "bypassPermissions" });
    saveSessionPrefs("p1", { harness: "codex", mode: "full-access", effort: "high" });

    expect(harnessPrefs("p1", "claude")).toEqual({
      model: undefined,
      effort: undefined,
      mode: "bypassPermissions",
    });
    expect(harnessPrefs("p1", "codex").mode).toBe("full-access");
    // And the last harness used is the one to open on.
    expect(projectPrefs("p1").harness).toBe("codex");
  });

  it("keeps projects apart", () => {
    saveSessionPrefs("p1", { harness: "claude", workspace: "local" });
    saveSessionPrefs("p2", { harness: "codex", workspace: "managed" });

    expect(projectPrefs("p1").workspace).toBe("local");
    expect(projectPrefs("p2").workspace).toBe("managed");
    expect(projectPrefs("p3")).toEqual({ byHarness: {} });
  });

  // "" means "the harness's own default" in the dialog, so it must not come
  // back out as a value that overrides one.
  it("records an empty setting as no setting", () => {
    saveSessionPrefs("p1", { harness: "claude", model: "", effort: "", mode: "" });
    expect(harnessPrefs("p1", "claude")).toEqual({
      model: undefined,
      effort: undefined,
      mode: undefined,
    });
  });

  // An attach names one checkout; that is not a standing preference, so the
  // last real workspace kind survives it.
  it("leaves the workspace kind alone when the session attached", () => {
    saveSessionPrefs("p1", { harness: "claude", workspace: "managed" });
    saveSessionPrefs("p1", { harness: "claude", workspace: "" });
    expect(projectPrefs("p1").workspace).toBe("managed");
  });

  it("ignores malformed storage rather than throwing", () => {
    localStorage.setItem(KEY, "{not json");
    expect(loadSessionPrefs()).toEqual({});

    localStorage.setItem(KEY, JSON.stringify(["nope"]));
    expect(loadSessionPrefs()).toEqual({});

    // A project whose entry is not an object is dropped; a harness entry that
    // is not an object is dropped from an otherwise usable project.
    localStorage.setItem(KEY, JSON.stringify({ p1: 7, p2: { harness: "codex", byHarness: { c: 3 } } }));
    const loaded = loadSessionPrefs();
    expect(Object.keys(loaded)).toEqual(["p2"]);
    expect(loaded.p2.harness).toBe("codex");
    expect(loaded.p2.byHarness).toEqual({});
  });

  // Safari private mode denies storage outright. Forgetting is fine; throwing
  // on the way into the dialog is not.
  it("survives storage being unavailable", () => {
    const denied = () => {
      throw new Error("denied");
    };
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(denied);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(denied);

    expect(loadSessionPrefs()).toEqual({});
    expect(() => saveSessionPrefs("p1", { harness: "claude", model: "opus" })).not.toThrow();

    vi.restoreAllMocks();
  });
});
