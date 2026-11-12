// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loadThreadPrefs, saveThreadPrefs } from "./threadPrefs";

beforeEach(() => localStorage.clear());

describe("thread preference storage", () => {
  it("round trips independent projects and harnesses, including an explicit default effort", () => {
    const codex = {
      instance: "codex",
      model: "astra",
      mode: "bypass",
      effort: "",
      want1m: false,
      fast: true,
    };
    const claude = {
      ...codex,
      instance: "claude",
      model: "fable",
      effort: "high",
      fast: false,
    };
    const choices = {
      one: { harness: "claude", byHarness: { codex, claude } },
      two: {
        harness: "codex",
        byHarness: { codex: { ...codex, mode: "ask" } },
      },
    };
    saveThreadPrefs(choices);
    expect(loadThreadPrefs()).toEqual(choices);
  });

  it("loads a choice saved before Fast existed, as off", () => {
    localStorage.setItem(
      "omniplex.threadChoices.v1",
      JSON.stringify({
        p: {
          harness: "codex",
          byHarness: { codex: { instance: "codex", model: "astra", mode: "", effort: "high" } },
        },
      }),
    );
    expect(loadThreadPrefs().p?.byHarness.codex).toMatchObject({ model: "astra", fast: false });
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
      const result = loadThreadPrefs();
      expect(result.p?.byHarness.codex).toBeUndefined();
    }
    get.mockRestore();
  });

  it("does not break the dialog when storage is denied", () => {
    const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(loadThreadPrefs()).toEqual({});
    get.mockRestore();
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() => saveThreadPrefs({})).not.toThrow();
    set.mockRestore();
  });
});
