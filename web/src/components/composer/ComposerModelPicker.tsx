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

/**
 * The one control for what runs the next turn: the model, the account it
 * bills to among this harness's own accounts, and reasoning effort, which
 * sits in the foot of the same menu rather than beside it as a second
 * dropdown.
 */
export function ComposerModelPicker({
  harnesses,
  harness,
  instance,
  model,
  effort,
  contextWindow,
  anyHarness,
  disabled,
  open,
  onOpenChange,
  focusRequest,
  onSwitchModel,
  onSwitchEffort,
  onSwitchAccount,
  onPickInstance,
}: {
  harnesses: HarnessMeta[];
  harness: string;
  instance: string;
  model: string;
  effort: string;
  contextWindow: number | undefined;
  anyHarness: boolean;
  disabled: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  focusRequest?: { target: PickerFocus } | null;
  onSwitchModel?: (id: string) => void | Promise<boolean>;
  onSwitchEffort?: (effort: string) => void | Promise<boolean>;
  onSwitchAccount?: (instance: string, model: string) => void;
  onPickInstance?: (instance: PickerInstance) => void;
}) {
  // Mid-thread a pick only lands when the server echoes it back, a round trip
  // that on a slow connection is long enough to see the open menu still
  // naming the old model and its levels. So the pick is shown until the props
  // agree with it. Closing the menu, or the thread's account changing under
  // it, drops it: the trigger goes back to saying what is actually running.
  // A draft's choices are local and agree at once, so this is a no-op there.
  const [pending, setPending] = useState<{ model?: string; effort?: string }>({});
  // The last model sent and not yet echoed back. Unlike the pending pick it
  // outlives the menu closing: the switch is still on its way regardless.
  const [inFlight, setInFlight] = useState<string>();
  const [synced, setSynced] = useState({ open, harness, instance, model, effort });
  if (
    synced.open !== open ||
    synced.harness !== harness ||
    synced.instance !== instance ||
    synced.model !== model ||
    synced.effort !== effort
  ) {
    setSynced({ open, harness, instance, model, effort });
    if (synced.model !== model || synced.instance !== instance) setInFlight(undefined);
    if (!open || synced.harness !== harness || synced.instance !== instance) setPending({});
    else
      setPending((p) => ({
        model: p.model === model ? undefined : p.model,
        effort: p.effort === effort ? undefined : p.effort,
      }));
  }
  // A switch the server refused is never echoed back: stop showing it.
  const settle = (field: "model" | "effort", value: string, sent: void | Promise<boolean>) => {
    if (!(sent instanceof Promise)) return;
    void sent.then((ok) => {
      if (!ok) setPending((p) => (p[field] === value ? { ...p, [field]: undefined } : p));
    });
  };
  const shownModel = pending.model ?? model;
  const shownEffort = pending.effort ?? effort;

  // The model's own reasoning levels, so the effort control offers what this
  // model accepts rather than a fixed set. Legacy picks report none, and the
  // control simply does not appear.
  const modelEfforts = useMemo(() => {
    const instances = pickerInstances(harnesses);
    const inst = resolveInstance(instances, instance, harness);
    return resolveModel(inst, shownModel)?.efforts ?? [];
  }, [harnesses, instance, harness, shownModel]);

  const switchEffort = (next: string) => {
    if (!onSwitchEffort) return;
    setPending((p) => ({ ...p, effort: next === effort ? undefined : next }));
    settle("effort", next, onSwitchEffort(next));
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
      value={{ harness, instance, model: shownModel }}
      onChange={(next) => {
        const current = resolveInstance(pickerInstances(harnesses), instance, harness);
        if (next.instance !== current?.id) {
          onSwitchAccount?.(next.instance, next.model);
          return;
        }
        // The thread records the id the harness reported ("claude-opus-5"),
        // the row its alias ("opus"): picking the row already running is no
        // switch, and sending it would mark one in the transcript. Unless a
        // pick away from it is still in flight: then this one undoes it.
        if (inFlight === undefined && resolveModel(current, model)?.id === next.model) return;
        if (onSwitchModel) {
          setPending((p) => ({ ...p, model: next.model === model ? undefined : next.model }));
          setInFlight(next.model);
          const sent = onSwitchModel(next.model);
          settle("model", next.model, sent);
          if (sent instanceof Promise)
            void sent.then((ok) => {
              if (!ok) setInFlight((f) => (f === next.model ? undefined : f));
            });
        }
        // Effort is per model: a level the old model allowed (Codex's
        // "ultra") may be one the new model rejects, which would break
        // its next turn. When the chosen model does not support the
        // current effort, drop to its strongest supported level —
        // closest to the intent, and a valid, displayable value.
        const nextEfforts =
          resolveModel(resolveInstance(pickerInstances(harnesses), instance, harness), next.model)
            ?.efforts ?? [];
        if (shownEffort && nextEfforts.length > 0 && !nextEfforts.includes(shownEffort)) {
          switchEffort(nextEfforts[nextEfforts.length - 1]);
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
