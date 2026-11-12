import { useMemo, useState } from "react";

import { ModelPicker, type PickerFocus } from "~/components/ModelPicker";
import {
  formatContextWindow,
  pickerInstances,
  resolveInstance,
  resolveModel,
  type PickerInstance,
} from "~/lib/models";
import type { HarnessMeta } from "~/protocol";

const NO_EFFORTS: string[] = [];

type Pickable = "model" | "effort" | "serviceTier";

/**
 * The one control for what runs the next turn: the model, the account it
 * bills to among this harness's own accounts, and reasoning effort, which
 * sits in the foot of the same menu rather than beside it as a second
 * dropdown. Fast joins it the same way.
 */
export function ComposerModelPicker({
  harnesses,
  harness,
  instance,
  model,
  effort,
  serviceTier,
  contextWindow,
  anyHarness,
  disabled,
  open,
  onOpenChange,
  focusRequest,
  onSwitchModel,
  onSwitchEffort,
  onSwitchServiceTier,
  onSwitchAccount,
  onPickInstance,
}: {
  harnesses: HarnessMeta[];
  harness: string;
  instance: string;
  model: string;
  effort: string;
  serviceTier: string;
  contextWindow: number | undefined;
  anyHarness: boolean;
  disabled: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  focusRequest?: { target: PickerFocus } | null;
  onSwitchModel?: (id: string) => void | Promise<boolean>;
  onSwitchEffort?: (effort: string) => void | Promise<boolean>;
  onSwitchServiceTier?: (serviceTier: string) => void | Promise<boolean>;
  onSwitchAccount?: (instance: string, model: string) => void;
  onPickInstance?: (instance: PickerInstance) => void;
}) {
  const current = useMemo(
    () => resolveInstance(pickerInstances(harnesses), instance, harness),
    [harnesses, instance, harness],
  );
  const {
    shown: { model: shownModel, effort: shownEffort, serviceTier: shownTier },
    inFlight,
    pick,
  } = usePendingPicks(
    { open, harness, instance, model, effort, serviceTier },
    (id) => resolveModel(current, id)?.id,
  );

  // The model's own reasoning levels, so the effort control offers what this
  // model accepts rather than a fixed set. Legacy picks report none, and the
  // control simply does not appear.
  const shownMeta = useMemo(() => resolveModel(current, shownModel), [current, shownModel]);
  const modelEfforts = shownMeta?.efforts ?? NO_EFFORTS;

  const switchEffort = (next: string) => {
    if (onSwitchEffort) pick("effort", next, onSwitchEffort(next));
  };
  const switchServiceTier = (next: string) => {
    if (onSwitchServiceTier) pick("serviceTier", next, onSwitchServiceTier(next));
  };

  return (
    <ModelPicker
      harnesses={harnesses}
      lockDriver={!anyHarness}
      onInstanceChange={onPickInstance}
      disabled={disabled}
      efforts={onSwitchEffort ? modelEfforts : []}
      effort={shownEffort}
      contextLabel={formatContextWindow(contextWindow)}
      onEffortChange={onSwitchEffort && switchEffort}
      fastTier={shownMeta?.fastTier ?? ""}
      serviceTier={shownTier}
      onServiceTierChange={onSwitchServiceTier && switchServiceTier}
      value={{ harness, instance, model: shownModel }}
      onChange={(next) => {
        if (next.instance !== current?.id) {
          onSwitchAccount?.(next.instance, next.model);
          return;
        }
        // Picking the row already running is no switch, and sending it would
        // mark one in the transcript. Unless a pick away from it is still in
        // flight: then this one undoes it.
        if (inFlight === undefined && resolveModel(current, model)?.id === next.model) return;
        if (onSwitchModel) pick("model", next.model, onSwitchModel(next.model));
        // Effort is per model: a level the old model allowed (Codex's
        // "ultra") may be one the new model rejects, which would break
        // its next turn. When the chosen model does not support the
        // current effort, drop to its strongest supported level —
        // closest to the intent, and a valid, displayable value.
        const nextModel = resolveModel(current, next.model);
        const nextEfforts = nextModel?.efforts ?? NO_EFFORTS;
        if (shownEffort && nextEfforts.length > 0 && !nextEfforts.includes(shownEffort)) {
          switchEffort(nextEfforts[nextEfforts.length - 1]);
        }
        // Fast is per model too, and a tier is only an id the harness
        // understands for the model that offered it. Staying Fast means the
        // new model's own tier; a model with none runs at the default. A
        // draft is left alone: it keeps Fast as a wish and resolves it
        // against whichever model it starts with.
        const nextFast = nextModel?.fastTier ?? "";
        if (!anyHarness && shownTier && nextFast !== shownTier) {
          switchServiceTier(nextFast);
        }
      }}
      open={open}
      onOpenChange={onOpenChange}
      focusRequest={focusRequest}
      compact
      // shrink undoes Button's shrink-0: the picker is the one control
      // in this row that can give up width, so it must, or the send
      // button is what gets pushed off a narrow screen.
      className="text-muted-foreground hover:text-foreground hover:bg-accent dark:hover:bg-accent h-11 w-auto max-w-[55%] min-w-0 shrink border-0 bg-transparent px-2 shadow-none dark:bg-transparent md:h-8 md:min-h-8"
    />
  );
}

type Picks = Record<Pickable, string>;

/**
 * What the picker shows for each pick: the one just made, until the props
 * agree with it.
 *
 * Mid-thread a pick only lands when the server echoes it back, a round trip
 * that on a slow connection is long enough to see the open menu still naming
 * the old model and its levels. Closing the menu, or the thread's account
 * changing under it, drops every pending pick: the trigger goes back to saying
 * what is actually running. A draft's choices are local and agree at once, so
 * this is a no-op there.
 */
function usePendingPicks(
  actual: Picks & { open: boolean; harness: string; instance: string },
  /** The catalogue row a model id names: the thread records the id the
      harness reported ("claude-opus-5"), the row its alias ("opus"). */
  rowFor: (id: string) => string | undefined,
) {
  const { open, harness, instance, model, effort, serviceTier } = actual;
  const [pending, setPending] = useState<Partial<Picks>>({});
  // The last model sent and not yet echoed back. Unlike the pending pick it
  // outlives the menu closing: the switch is still on its way regardless.
  const [inFlight, setInFlight] = useState<string>();
  const [synced, setSynced] = useState(actual);
  if (
    synced.open !== open ||
    synced.harness !== harness ||
    synced.instance !== instance ||
    synced.model !== model ||
    synced.effort !== effort ||
    synced.serviceTier !== serviceTier
  ) {
    setSynced(actual);
    // Cleared by the echo of the model sent, not by any echo: an earlier
    // pick, or the harness naming the id for an alias, can land before it.
    if (synced.instance !== instance) setInFlight(undefined);
    else if (inFlight !== undefined && (model === inFlight || rowFor(model) === inFlight)) setInFlight(undefined);
    if (!open || synced.harness !== harness || synced.instance !== instance) setPending({});
    else
      setPending((p) => ({
        model: p.model === model ? undefined : p.model,
        effort: p.effort === effort ? undefined : p.effort,
        serviceTier: p.serviceTier === serviceTier ? undefined : p.serviceTier,
      }));
  }

  // Shows `value` until the server echoes it, or until `sent` resolves false:
  // a switch the server refused is never echoed back, so stop showing it, and
  // stop waiting for it.
  const pick = (field: Pickable, value: string, sent: void | Promise<boolean>) => {
    setPending((p) => ({ ...p, [field]: value === actual[field] ? undefined : value }));
    if (field === "model") setInFlight(value);
    if (!(sent instanceof Promise)) return;
    void sent.then((ok) => {
      if (ok) return;
      setPending((p) => (p[field] === value ? { ...p, [field]: undefined } : p));
      if (field === "model") setInFlight((f) => (f === value ? undefined : f));
    });
  };

  return {
    shown: {
      model: pending.model ?? model,
      effort: pending.effort ?? effort,
      serviceTier: pending.serviceTier ?? serviceTier,
    },
    inFlight,
    pick,
  };
}
