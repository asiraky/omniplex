import { describe, expect, it } from "vitest";

import { defaultModel, describeSettings, pickerInstances, resolveInstance, resolveModel } from "./models";
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

  it("yields nothing for a driver with no accounts", () => {
    const got = pickerInstances([
      harness({ instances: [] }),
      harness({ id: "pi", name: "Pi", instances: undefined } as Partial<HarnessMeta>),
    ]);

    expect(got).toEqual([]);
  });

  it("lists only the drivers that have accounts", () => {
    const got = pickerInstances([harness(), harness({ id: "pi", name: "Pi", instances: [] })]);

    expect(got.map((i) => i.id)).toEqual(["codex"]);
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
        { id: "default", label: "Opus 5", resolves: "claude-opus-5[1m]", default: true },
        { id: "sonnet", label: "Sonnet" },
      ],
      // No catalogue of its own yet, so it shows the harness's.
      instances: [
        { id: "codex", driver: "codex", displayName: "Codex", enabled: true, availability: ready, models: [] },
      ],
    } as Partial<HarnessMeta>),
  ])[0];

  it("preselects the model the harness calls its default", () => {
    expect(resolveModel(instance, "")?.id).toBe("default");
    expect(defaultModel(instance)?.label).toBe("Opus 5");
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

  it("routes a thread that recorded only its harness to the account named after it", () => {
    expect(resolveInstance(instances, "", "codex")?.id).toBe("codex");
  });

  it("prefers the named instance over the one named after its driver", () => {
    expect(resolveInstance(instances, "codex_work", "codex")?.id).toBe("codex_work");
  });

  it("falls back to something usable when the named instance is gone", () => {
    expect(resolveInstance(instances, "codex_deleted", "nope")?.id).toBe("codex");
  });
});

describe("describeSettings", () => {
  const instance = pickerInstances([
    harness({
      instances: [
        {
          id: "codex",
          driver: "codex",
          displayName: "Codex",
          enabled: true,
          availability: ready,
          models: [
            { id: "gpt-5.6-sol", label: "GPT-5.6-Sol", efforts: ["low", "high", "xhigh"] },
            { id: "gpt-mini", label: "GPT Mini" },
          ],
        },
      ],
    } as Partial<HarnessMeta>),
  ])[0];

  it("names the model by its label and the effort by its display name", () => {
    expect(describeSettings(instance, "gpt-5.6-sol", "xhigh")).toBe("GPT-5.6-Sol · Extra high");
  });

  it("calls an unset effort Auto when the model has levels", () => {
    expect(describeSettings(instance, "gpt-5.6-sol", "")).toBe("GPT-5.6-Sol · Auto");
  });

  it("leaves the effort out for a model with no levels", () => {
    expect(describeSettings(instance, "gpt-mini", "")).toBe("GPT Mini");
  });

  it("falls back to the raw id for a model the catalogue has dropped", () => {
    expect(describeSettings(instance, "gpt-4", "high")).toBe("gpt-4 · High");
  });
});
