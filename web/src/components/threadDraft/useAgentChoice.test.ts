import { describe, expect, it } from "vitest";

import type { HarnessPrefs, ProjectPrefs } from "~/lib/threadPrefs";
import type { HarnessMeta, ModelMeta, Project, UserConfig } from "~/protocol";

import { compareAgent, type AgentSources } from "./useAgentChoice";

const ready = { state: "ready" as const };

const claudeModels: ModelMeta[] = [
  { id: "opus", label: "Opus 5", default: true, efforts: ["low", "high", "max"], supports1m: true },
  { id: "sonnet", label: "Sonnet 5", efforts: ["low", "high"] },
  { id: "unlabelled", label: "" },
];

const codexModels: ModelMeta[] = [
  { id: "gpt-sol", label: "GPT-Sol", default: true, efforts: ["low", "xhigh"], fastTier: "priority" },
  { id: "gpt-mini", label: "GPT-Mini", efforts: ["low"] },
];

const harnesses: HarnessMeta[] = [
  {
    id: "claude",
    name: "Claude",
    models: claudeModels,
    permissionModes: [
      { id: "default", label: "Ask", default: true, level: "ask" },
      { id: "acceptEdits", label: "Edits", level: "edits" },
      { id: "bypass", label: "All", level: "all" },
    ],
    availability: ready,
    instances: [
      { id: "claude", driver: "claude", displayName: "Claude", enabled: true, availability: ready },
      {
        id: "claude-work",
        driver: "claude",
        displayName: "Claude (work)",
        enabled: true,
        availability: ready,
      },
    ],
  },
  {
    id: "codex",
    name: "Codex",
    models: codexModels,
    permissionModes: [
      { id: "read-only", label: "Ask", default: true, level: "ask" },
      { id: "full", label: "All", level: "all" },
    ],
    availability: ready,
    instances: [
      { id: "codex", driver: "codex", displayName: "Codex", enabled: true, availability: ready },
    ],
  },
] as HarnessMeta[];

function prefs(over: Partial<HarnessPrefs> = {}): HarnessPrefs {
  return { instance: "", model: "", mode: "", effort: "", want1m: false, fast: false, ...over };
}

function project(seed: NonNullable<Project["defaults"]["harnesses"]> = {}): Project {
  return {
    id: "p",
    name: "P",
    defaults: { harnesses: seed },
    folders: [],
    createdAt: 0,
    updatedAt: 0,
  };
}

function sources(over: Partial<AgentSources> = {}): AgentSources {
  return { project: project(), remembered: undefined, harnesses, userConfig: null, ...over };
}

const remembered = (byHarness: Record<string, HarnessPrefs>, harness = "codex"): ProjectPrefs => ({
  harness,
  byHarness,
});

const claude = (model: string, instance = "claude") => ({ harness: "claude", instance, model });
const codex = (model: string) => ({ harness: "codex", instance: "codex", model });

describe("compareAgent", () => {
  it("resolves from the pick's harness, not the draft's current one", () => {
    const got = compareAgent(
      sources({
        remembered: remembered({
          codex: prefs({ instance: "codex", model: "gpt-sol", effort: "xhigh", mode: "full", fast: true }),
          claude: prefs({ instance: "claude", model: "sonnet", effort: "high", mode: "acceptEdits" }),
        }),
      }),
      claude("opus"),
    );
    expect(got).toEqual({
      harness: "claude",
      instance: "claude",
      model: "opus",
      mode: "acceptEdits",
      effort: "high",
      serviceTier: "",
      label: "Opus 5",
    });
  });

  it("sends the model picked over the one remembered for that harness", () => {
    const got = compareAgent(
      sources({ remembered: remembered({ claude: prefs({ model: "opus" }) }) }),
      claude("sonnet"),
    );
    expect(got.model).toBe("sonnet");
    expect(got.label).toBe("Sonnet 5");
  });

  describe("effort", () => {
    it("drops a remembered effort the model does not offer", () => {
      const got = compareAgent(
        sources({ remembered: remembered({ claude: prefs({ effort: "max" }) }) }),
        claude("sonnet"),
      );
      expect(got.effort).toBe("");
    });

    it("falls back to the project's seed when nothing is remembered", () => {
      const got = compareAgent(sources({ project: project({ claude: { effort: "max" } }) }), claude("opus"));
      expect(got.effort).toBe("max");
    });

    it("prefers the remembered effort over the seed", () => {
      const got = compareAgent(
        sources({
          project: project({ claude: { effort: "max" } }),
          remembered: remembered({ claude: prefs({ effort: "low" }) }),
        }),
        claude("opus"),
      );
      expect(got.effort).toBe("low");
    });

    it("is empty for a model with no effort levels", () => {
      const got = compareAgent(
        sources({ remembered: remembered({ claude: prefs({ effort: "high" }) }) }),
        claude("unlabelled"),
      );
      expect(got.effort).toBe("");
    });
  });

  describe("Fast", () => {
    it("becomes the model's fast tier when remembered on", () => {
      const got = compareAgent(
        sources({ remembered: remembered({ codex: prefs({ fast: true }) }) }),
        codex("gpt-sol"),
      );
      expect(got.serviceTier).toBe("priority");
    });

    it("is dropped for a model without a fast tier", () => {
      const got = compareAgent(
        sources({ remembered: remembered({ codex: prefs({ fast: true }) }) }),
        codex("gpt-mini"),
      );
      expect(got.serviceTier).toBe("");
    });

    it("stays off when not remembered, even where the model has a tier", () => {
      expect(compareAgent(sources(), codex("gpt-sol")).serviceTier).toBe("");
    });

    it("is not carried over from another harness", () => {
      const got = compareAgent(
        sources({ remembered: remembered({ claude: prefs({ fast: true }) }, "claude") }),
        codex("gpt-sol"),
      );
      expect(got.serviceTier).toBe("");
    });
  });

  describe("1M window", () => {
    it("tags the model when it supports 1M and the window is remembered on", () => {
      const got = compareAgent(
        sources({ remembered: remembered({ claude: prefs({ want1m: true }) }) }),
        claude("opus"),
      );
      expect(got.model).toBe("opus[1m]");
      expect(got.label).toBe("Opus 5");
    });

    it("leaves a model without 1M support untagged", () => {
      const got = compareAgent(
        sources({ remembered: remembered({ claude: prefs({ want1m: true }) }) }),
        claude("sonnet"),
      );
      expect(got.model).toBe("sonnet");
    });

    it("leaves the model untagged when the window is not remembered on", () => {
      expect(compareAgent(sources(), claude("opus")).model).toBe("opus");
    });
  });

  describe("mode", () => {
    it("uses the remembered mode over the project's seed", () => {
      const got = compareAgent(
        sources({
          project: project({ claude: { mode: "bypass" } }),
          remembered: remembered({ claude: prefs({ mode: "acceptEdits" }) }),
        }),
        claude("opus"),
      );
      expect(got.mode).toBe("acceptEdits");
    });

    it("uses the project's seed when nothing is remembered", () => {
      const got = compareAgent(sources({ project: project({ claude: { mode: "bypass" } }) }), claude("opus"));
      expect(got.mode).toBe("bypass");
    });

    it("uses the settings' level when neither is set", () => {
      const userConfig = { version: 1, defaultLevel: "all" } as UserConfig;
      expect(compareAgent(sources({ userConfig }), claude("opus")).mode).toBe("bypass");
      expect(compareAgent(sources({ userConfig }), codex("gpt-sol")).mode).toBe("full");
    });

    it("sends no mode for one the harness does not offer", () => {
      const got = compareAgent(
        sources({ remembered: remembered({ codex: prefs({ mode: "acceptEdits" }) }) }),
        codex("gpt-sol"),
      );
      expect(got.mode).toBe("");
    });

    it("sends no mode when nothing expresses one", () => {
      expect(compareAgent(sources({ project: undefined }), claude("opus")).mode).toBe("");
    });
  });

  describe("label", () => {
    it("is the catalogue's name for the model", () => {
      expect(compareAgent(sources(), codex("gpt-mini")).label).toBe("GPT-Mini");
    });

    it("falls back to the id when the catalogue's name is empty", () => {
      expect(compareAgent(sources(), claude("unlabelled")).label).toBe("unlabelled");
    });

    it("falls back to the id for an account no longer listed", () => {
      const got = compareAgent(sources(), { harness: "claude", instance: "gone", model: "opus-old" });
      expect(got.label).toBe("opus-old");
      expect(got.instance).toBe("gone");
      expect(got.harness).toBe("claude");
    });

    it("names the account's default for a model it no longer offers", () => {
      const got = compareAgent(sources(), claude("retired"));
      expect(got.model).toBe("opus");
      expect(got.label).toBe("Opus 5");
    });
  });

  describe("harness", () => {
    it("comes from the instance's driver, not the pick", () => {
      const got = compareAgent(
        sources({
          remembered: remembered({
            claude: prefs({ effort: "high" }),
            codex: prefs({ effort: "xhigh" }),
          }),
        }),
        { harness: "codex", instance: "claude-work", model: "opus" },
      );
      expect(got.harness).toBe("claude");
      expect(got.instance).toBe("claude-work");
      expect(got.effort).toBe("high");
    });

    it("keeps a second account's instance rather than the harness's first", () => {
      const got = compareAgent(sources(), claude("sonnet", "claude-work"));
      expect(got.instance).toBe("claude-work");
      expect(got.model).toBe("sonnet");
    });
  });
});
