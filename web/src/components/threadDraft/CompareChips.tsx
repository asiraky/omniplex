import { GitCompareArrowsIcon, XIcon } from "lucide-react";

import { ModelPicker, type ModelSelection } from "~/components/ModelPicker";
import { ProviderLogo } from "~/components/ProviderLogo";
import { Button } from "~/components/ui/button";
import { cn } from "~/lib/utils";
import type { HarnessMeta } from "~/protocol";
import { MAX_COMPARE } from "./compare";
import { Chip } from "./parts";
import { chipClass } from "./shared";
import type { CompareState } from "./useCompare";

/**
 * The models the prompt also goes to, each a chip that takes it off again,
 * and the chip that adds one until the set is full. Only the model is picked
 * here: its effort and the rest are what the project last used with it.
 */
export function CompareChips({
  harnesses,
  primary,
  compare,
}: {
  harnesses: HarnessMeta[];
  /** The draft's own model, which the picker opens on. */
  primary: ModelSelection;
  compare: CompareState;
}) {
  return (
    <>
      {compare.extras.map(({ pick, agent }) => (
        <Button
          key={`${pick.instance}:${pick.model}`}
          type="button"
          variant="ghost"
          size="sm"
          aria-label={`Stop comparing with ${agent.label}`}
          onClick={() => compare.remove(pick)}
          className={cn(chipClass, "text-foreground")}
        >
          <ProviderLogo provider={agent.harness} />
          <span className="truncate">{agent.label}</span>
          <XIcon className="text-muted-foreground" />
        </Button>
      ))}
      {compare.extras.length < MAX_COMPARE && (
        <ModelPicker
          harnesses={harnesses}
          value={primary}
          onChange={compare.add}
          trigger={
            <Chip
              label="Compare"
              icon={<GitCompareArrowsIcon />}
              aria-label="Compare with another model"
            >
              Compare
            </Chip>
          }
        />
      )}
    </>
  );
}
