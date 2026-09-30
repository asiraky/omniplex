import { useMemo } from "react";

import { ModelPicker } from "~/components/ModelPicker";
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
 * opens out of the same menu rather than sitting beside it as a second
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
  onSwitchModel?: (id: string) => void;
  onSwitchEffort?: (effort: string) => void;
  onSwitchAccount?: (instance: string, model: string) => void;
  onPickInstance?: (instance: PickerInstance) => void;
}) {
  // The running model's own reasoning levels, so the effort control offers what
  // this model accepts rather than a fixed set. Legacy picks report none, and
  // the control simply does not appear.
  const modelEfforts = useMemo(() => {
    const instances = pickerInstances(harnesses);
    const inst = resolveInstance(instances, instance, harness);
    return resolveModel(inst, model)?.efforts ?? [];
  }, [harnesses, instance, harness, model]);

  return (
    <ModelPicker
      harnesses={harnesses}
      lockDriver={!anyHarness}
      onInstanceChange={onPickInstance}
      disabled={disabled}
      efforts={onSwitchEffort ? modelEfforts : []}
      effort={effort}
      contextLabel={formatContextWindow(contextWindow)}
      onEffortChange={onSwitchEffort}
      value={{ harness, instance, model }}
      onChange={(next) => {
        const current = resolveInstance(pickerInstances(harnesses), instance, harness)?.id;
        if (next.instance !== current) {
          onSwitchAccount?.(next.instance, next.model);
          return;
        }
        onSwitchModel?.(next.model);
        // Effort is per model: a level the old model allowed (Codex's
        // "ultra") may be one the new model rejects, which would break
        // its next turn. When the chosen model does not support the
        // current effort, drop to its strongest supported level —
        // closest to the intent, and a valid, displayable value.
        const nextEfforts =
          resolveModel(resolveInstance(pickerInstances(harnesses), instance, harness), next.model)
            ?.efforts ?? [];
        if (effort && nextEfforts.length > 0 && !nextEfforts.includes(effort)) {
          onSwitchEffort?.(nextEfforts[nextEfforts.length - 1]);
        }
      }}
      open={open}
      onOpenChange={onOpenChange}
      compact
      // shrink undoes Button's shrink-0: the picker is the one control
      // in this row that can give up width, so it must, or the send
      // button is what gets pushed off a narrow screen.
      className="text-muted-foreground hover:text-foreground hover:bg-accent dark:hover:bg-accent h-11 w-auto max-w-[55%] min-w-0 shrink border-0 bg-transparent px-2 shadow-none dark:bg-transparent md:h-8 md:min-h-8"
    />
  );
}
