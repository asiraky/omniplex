import type { Availability, HarnessMeta, ModelMeta } from "~/protocol";
import { LEGACY_GROUP } from "~/protocol";

/**
 * One provider instance, flattened out of the harness list.
 *
 * The picker is keyed by instance, never by harness: two Codex accounts are
 * two entries with their own health and their own model lists. The driver
 * still travels along, because that is what selects the mark and the accent —
 * two accounts of one product should look like that product.
 */
export interface PickerInstance {
  id: string;
  driver: string;
  name: string;
  accent?: string;
  enabled: boolean;
  canLogin: boolean;
  availability: Availability;
  models: ModelMeta[];
}

/**
 * Flattens harnesses into the instance list the picker renders. A harness that
 * reports no instances — an older server, or a build mid-upgrade — still
 * yields one entry, so the picker never comes up empty because of a protocol
 * gap.
 */
export function pickerInstances(harnesses: HarnessMeta[]): PickerInstance[] {
  const out: PickerInstance[] = [];
  for (const h of harnesses) {
    const instances = h.instances?.length
      ? h.instances
      : [
          {
            id: h.id,
            driver: h.id,
            displayName: h.name,
            enabled: true,
            availability: h.availability,
            models: h.models,
          },
        ];
    for (const inst of instances) {
      out.push({
        id: inst.id,
        driver: inst.driver || h.id,
        name: inst.displayName || h.name,
        accent: h.accent,
        enabled: inst.enabled !== false,
        canLogin: inst.canLogin === true,
        availability: inst.availability ?? h.availability,
        // An instance that has not reported its own catalogue yet shows the
        // harness's, which is the same list in the one-account case.
        models: inst.models?.length ? inst.models : h.models,
      });
    }
  }
  return out;
}

/** The model a harness would pick for itself, and what the picker preselects. */
export function defaultModel(instance: PickerInstance | undefined): ModelMeta | undefined {
  if (!instance) return undefined;
  return instance.models.find((m) => m.default) ?? instance.models[0];
}

/**
 * Resolves a stored selection against the live list.
 *
 * A recorded model can outlive the catalogue that offered it — a harness
 * upgrade drops a name, or the list is still the fallback while the live one
 * loads. Rather than silently swapping in something else, an unknown id is
 * returned as a model of its own so the trigger keeps saying what the session
 * is actually running.
 */
export function resolveModel(
  instance: PickerInstance | undefined,
  modelId: string,
): ModelMeta | undefined {
  if (!instance) return undefined;
  if (!modelId) return defaultModel(instance);
  const bare = stripContextTag(modelId);
  return (
    instance.models.find((m) => m.id === modelId) ??
    instance.models.find((m) => m.resolves === modelId) ??
    // The harness reports the concrete model it is running with the context
    // tag stripped ("claude-opus-5"), while a row resolves to the tagged form
    // ("claude-opus-5[1m]"). They are the same model, so match on the bare id
    // rather than falling through to a raw, unlabelled row.
    instance.models.find((m) => stripContextTag(m.resolves ?? "") === bare) ??
    instance.models.find((m) => stripContextTag(m.id) === bare) ?? { id: modelId, label: modelId }
  );
}

/** Drops a trailing context-window tag like "[1m]" from a model id. */
function stripContextTag(id: string): string {
  return id.replace(/\[[^\]]*\]$/, "");
}

/** Picks the instance a selection refers to, falling back to a usable one. */
export function resolveInstance(
  instances: PickerInstance[],
  instanceId: string,
  harnessId: string,
): PickerInstance | undefined {
  return (
    instances.find((i) => i.id === instanceId) ??
    // A session created before instances existed records only its harness; its
    // default instance is the one whose id matches the driver.
    instances.find((i) => i.driver === harnessId && i.id === harnessId) ??
    instances.find((i) => i.driver === harnessId) ??
    instances.find((i) => i.availability?.state === "ready") ??
    instances[0]
  );
}

export function isLegacy(model: ModelMeta): boolean {
  return model.group === LEGACY_GROUP;
}

/**
 * Formats a context-window token count as a compact label ("1M", "200k"). It
 * is a fact of the model rather than a knob — Claude Code fixes the window per
 * model — so it is stated next to the model, never offered as a choice.
 */
export function formatContextWindow(tokens: number | undefined): string {
  if (!tokens || tokens <= 0) return "";
  if (tokens >= 1_000_000) {
    const m = tokens / 1_000_000;
    return `${Number.isInteger(m) ? m : m.toFixed(1)}M`;
  }
  if (tokens >= 1_000) return `${Math.round(tokens / 1_000)}k`;
  return String(tokens);
}
