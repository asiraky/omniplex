import { SettingsIcon, TagIcon } from "lucide-react";

import { LabelDot } from "~/components/LabelMenu";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { UNLABELLED } from "~/labelFilter";
import { cn } from "~/lib/utils";
import type { Label } from "~/protocol";

/**
 * The sidebar's one label control: which labels are showing, and the way to
 * the manager that creates them.
 *
 * It is a filter rather than a grouping because the list is already long on a
 * phone: switching a label off removes those sessions outright, which is the
 * thing a collapsed group only pretended to do. Checking is per label and the
 * menu stays open while you work through it — filtering is usually two or
 * three toggles, not one.
 */
export function LabelFilter({
  labels,
  hidden,
  onToggle,
  onShowAll,
  onManage,
}: {
  labels: Label[];
  /** Filter keys currently switched off: label ids, and `UNLABELLED`. */
  hidden: Set<string>;
  onToggle: (key: string, show: boolean) => void;
  onShowAll: () => void;
  onManage: () => void;
}) {
  // Only what is both hidden and still real counts as filtering: a hidden id
  // left behind by a deleted label hides nothing, so it must not light the
  // trigger up either.
  const live = [UNLABELLED, ...labels.map((l) => l.id)];
  const offCount = live.filter((key) => hidden.has(key)).length;
  const filtering = offCount > 0;
  const name = filtering ? `Filter by label — ${offCount} hidden` : "Filter by label";

  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={name}
              // Same 44px-on-a-phone, 32px-on-a-pointer square as IconButton;
              // spelled out here because the trigger also has to show, in the
              // header, that a filter is on at all — with grouping gone, a
              // hidden session leaves no trace anywhere else.
              className={cn(
                "size-11 shrink-0 md:size-8",
                filtering ? "text-primary" : "text-muted-foreground hover:text-foreground",
              )}
            >
              <TagIcon />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{name}</TooltipContent>
      </Tooltip>

      <DropdownMenuContent align="end" className="min-w-44">
        {labels.map((l) => (
          <DropdownMenuCheckboxItem
            key={l.id}
            checked={!hidden.has(l.id)}
            // Radix closes the menu on select by default, which would make
            // hiding three labels three round trips through the trigger.
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={(show) => onToggle(l.id, show)}
            className="gap-2 text-[13px]"
          >
            <LabelDot color={l.color} />
            <span className="truncate">{l.name}</span>
          </DropdownMenuCheckboxItem>
        ))}
        {labels.length > 0 && (
          <DropdownMenuCheckboxItem
            checked={!hidden.has(UNLABELLED)}
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={(show) => onToggle(UNLABELLED, show)}
            className="text-muted-foreground gap-2 text-[13px]"
          >
            No label
          </DropdownMenuCheckboxItem>
        )}
        {labels.length === 0 && (
          <p className="text-muted-foreground px-2 py-1.5 text-[13px]">No labels yet.</p>
        )}

        <DropdownMenuSeparator />
        {filtering && (
          <DropdownMenuItem onSelect={onShowAll} className="text-[13px]">
            Show all
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={onManage} className="gap-2 text-[13px]">
          <SettingsIcon className="size-3.5" />
          {labels.length === 0 ? "New label…" : "Manage labels…"}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
