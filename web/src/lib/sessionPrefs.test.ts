// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loadSessionPrefs, saveSessionPrefs } from "./sessionPrefs";

beforeEach(() => localStorage.clear());

describe("session preference storage", () => {
  it("round trips independent projects and harnesses, including an explicit default effort", () => {
    const codex = {
      instance: "codex",
      model: "astra",
      mode: "bypass",
      effort: "",
      want1m: false,
    };
    const claude = {
      ...codex,
      instance: "claude",
      model: "fable",
      effort: "high",
    };
    const choices = {
      one: { harness: "claude", byHarness: { codex, claude } },
      two: {
        harness: "codex",
        byHarness: { codex: { ...codex, mode: "ask" } },
      },
    };
    saveSessionPrefs(choices);
    expect(loadSessionPrefs()).toEqual(choices);
  });

  it("ignores corrupt storage", () => {
    const get = vi.spyOn(Storage.prototype, "getItem");
    for (const raw of [
      "invalid",
      "null",
      "[]",
      '{"p":{"harness":"codex","byHarness":{"codex":{"model":3}}}}',
    ]) {
      get.mockReturnValue(raw);
      const result = loadSessionPrefs();
      expect(result.p?.byHarness.codex).toBeUndefined();
    }
    get.mockRestore();
  });

  it("does not break the dialog when storage is denied", () => {
    const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(loadSessionPrefs()).toEqual({});
    get.mockRestore();
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() => saveSessionPrefs({})).not.toThrow();
    set.mockRestore();
  });
});
