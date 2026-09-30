import { describe, expect, it } from "vitest";

import { defaultModel, pickerInstances, resolveInstance, resolveModel } from "./models";
import type { HarnessMeta } from "~/protocol";

const ready = { state: "ready" };

function harness(over: Partial<HarnessMeta> = {}): HarnessMeta {
  return {
    id: "codex",
    name: "Codex",
    accent: "oklch(0.76 0.13 165)",
    models: [{ id: "gpt-5.6-sol", label: "GPT-5.6-Sol", default: true }],
    permissionModes: [],
    availability: ready,
    instances: [
      {
        id: "codex",
        driver: "codex",
        displayName: "Codex",
        enabled: true,
        availability: ready,
        models: [{ id: "gpt-5.6-sol", label: "GPT-5.6-Sol", default: true }],
      },
    ],
    ...over,
  } as HarnessMeta;
}

describe("pickerInstances", () => {
  it("lists every account separately, with its own models", () => {
    const got = pickerInstances([
      harness({
        instances: [
          {
            id: "codex",
            driver: "codex",
            displayName: "Codex",
            enabled: true,
            availability: ready,
            models: [{ id: "gpt-5.6-sol", label: "Sol" }],
          },
          {
            id: "codex_work",
            driver: "codex",
            displayName: "Codex Work",
            enabled: true,
            availability: { state: "unavailable", reason: "Logged out." },
            models: [{ id: "gpt-5.5", label: "GPT-5.5" }],
          },
        ],
      } as Partial<HarnessMeta>),
    ]);

    expect(got.map((i) => i.id)).toEqual(["codex", "codex_work"]);
    expect(got[1].models.map((m) => m.id)).toEqual(["gpt-5.5"]);
    // One account being logged out must not speak for the other.
    expect(got[0].availability.state).toBe("ready");
    expect(got[1].availability.reason).toBe("Logged out.");
    // Both are the same product, so both carry the driver's mark.
    expect(got.map((i) => i.driver)).toEqual(["codex", "codex"]);
  });

  it("still yields an entry for a harness that reports no instances", () => {
    const got = pickerInstances([harness({ instances: undefined }) as HarnessMeta]);

    expect(got).toHaveLength(1);
    expect(got[0]).toMatchObject({ id: "codex", driver: "codex", name: "Codex" });
    expect(got[0].models.map((m) => m.id)).toEqual(["gpt-5.6-sol"]);
  });

  it("falls back to the harness catalogue for an instance that has none yet", () => {
    const got = pickerInstances([
      harness({
        instances: [
          {
            id: "codex",
            driver: "codex",
            displayName: "Codex",
            enabled: true,
            availability: ready,
            models: [],
          },
        ],
      } as Partial<HarnessMeta>),
    ]);

    expect(got[0].models.map((m) => m.id)).toEqual(["gpt-5.6-sol"]);
  });
});

describe("resolveModel", () => {
  const instance = pickerInstances([
    harness({
      models: [
        { id: "default", label: "Default (recommended)", resolves: "claude-opus-5[1m]", default: true },
        { id: "sonnet", label: "Sonnet" },
      ],
      instances: undefined,
    } as Partial<HarnessMeta>),
  ])[0];

  it("preselects the model the harness calls its default", () => {
    expect(resolveModel(instance, "")?.id).toBe("default");
    expect(defaultModel(instance)?.label).toBe("Default (recommended)");
  });

  it("matches a stored wire id against the alias that resolves to it", () => {
    expect(resolveModel(instance, "claude-opus-5[1m]")?.id).toBe("default");
  });

  it("matches the harness's bare concrete id against a context-tagged resolves", () => {
    // The harness reports "claude-opus-5" (no tag) even though the row resolves
    // to "claude-opus-5[1m]"; they are the same model, so it must not fall
    // through to a raw, unlabelled row.
    expect(resolveModel(instance, "claude-opus-5")?.id).toBe("default");
  });

  it("keeps naming a model the catalogue no longer lists", () => {
    // The thread is running it; swapping the label for something else would
    // say the thread is doing something it is not.
    expect(resolveModel(instance, "claude-opus-4-6")).toEqual({
      id: "claude-opus-4-6",
      label: "claude-opus-4-6",
    });
  });
});

describe("resolveInstance", () => {
  const instances = pickerInstances([
    harness({
      instances: [
        { id: "codex", driver: "codex", displayName: "Codex", enabled: true, availability: ready, models: [] },
        {
          id: "codex_work",
          driver: "codex",
          displayName: "Work",
          enabled: true,
          availability: ready,
          models: [],
        },
      ],
    } as Partial<HarnessMeta>),
  ]);

  it("routes a thread recorded before instances existed to the driver's default", () => {
    expect(resolveInstance(instances, "", "codex")?.id).toBe("codex");
  });

  it("prefers the named instance over its driver's default", () => {
    expect(resolveInstance(instances, "codex_work", "codex")?.id).toBe("codex_work");
  });

  it("falls back to something usable when the named instance is gone", () => {
    expect(resolveInstance(instances, "codex_deleted", "nope")?.id).toBe("codex");
  });
});
