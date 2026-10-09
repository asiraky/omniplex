import { ArrowUpDownIcon, ListFilterIcon, SettingsIcon } from "lucide-react";

import { LabelDot } from "~/components/LabelMenu";
import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { UNLABELLED } from "~/labelFilter";
import { cn } from "~/lib/utils";
import type { Label, Project } from "~/protocol";

/**
 * The sidebar's one filter: which projects and which labels are showing.
 *
 * Projects lead, because they decide the shape of the list (more than one
 * showing groups it under headers) where a label only thins it. Every entry is
 * a checkbox and the menu stays open while you work through it: filtering is
 * usually two or three toggles, not one.
 *
 * "All projects" is there because turning everything back on is the common way
 * out, and unchecking it switches every project off, keeping it the same kind
 * of control as the rest. "Show all" clears both halves at once.
 *
 * "Group by project" leads the menu: it is not a filter, it decides how what
 * the filters leave is laid out. It only means anything with more than one
 * project to group, so it shows when the project choices do, and it never
 * lights the trigger — a flat list hides nothing.
 */
export function ThreadFilter({
  grouped,
  onToggleGrouped,
  projects,
  hiddenProjects,
  onToggleProject,
  onShowAllProjects,
  onHideAllProjects,
  labels,
  hiddenLabels,
  onToggleLabel,
  onShowAllLabels,
  onManageLabels,
  onReorder,
}: {
  /** The list is carved into project groups; off, it is one flat list. */
  grouped: boolean;
  onToggleGrouped: (on: boolean) => void;
  projects: Project[];
  /** Project ids switched off. */
  hiddenProjects: Set<string>;
  onToggleProject: (id: string, show: boolean) => void;
  onShowAllProjects: () => void;
  onHideAllProjects: () => void;
  labels: Label[];
  /** Label keys switched off: label ids, and `UNLABELLED`. */
  hiddenLabels: Set<string>;
  onToggleLabel: (key: string, show: boolean) => void;
  onShowAllLabels: () => void;
  onManageLabels: () => void;
  /** Puts the list into reorder mode. */
  onReorder: () => void;
}) {
  // Only what is both hidden and still real counts: an id left behind by a
  // deleted project or label hides nothing, so it must not light the trigger.
  // With one project there is nothing to choose between, and with no labels
  // "No label" is not in the menu either.
  const choosing = projects.length > 1;
  const projectsOff = choosing ? projects.filter((p) => hiddenProjects.has(p.id)).length : 0;
  const labelKeys = labels.length === 0 ? [] : [UNLABELLED, ...labels.map((l) => l.id)];
  const labelsOff = labelKeys.filter((key) => hiddenLabels.has(key)).length;
  const off = projectsOff + labelsOff;
  const name = off > 0 ? `Filter threads, ${off} hidden` : "Filter threads";
  // Radix closes the menu on select by default, which would make narrowing
  // to two projects a trip through the trigger for each.
  const stayOpen = (e: Event) => e.preventDefault();

  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={name}
              // IconButton's square: 44px for a thumb, 32px for a pointer. Lit
              // while anything is filtered out, since a hidden thread leaves no
              // other trace on screen.
              className={cn(
                "size-11 shrink-0 md:size-8",
                off > 0 ? "text-primary" : "text-muted-foreground hover:text-foreground",
              )}
            >
              <ListFilterIcon />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{name}</TooltipContent>
      </Tooltip>

      <DropdownMenuContent align="end" className="min-w-52">
        {/* Not a filter, but this is the list's one menu, and reordering is
            rare enough not to earn an icon of its own in the header. */}
        <DropdownMenuItem onSelect={onReorder} className="text-[13px]">
          <ArrowUpDownIcon className="text-muted-foreground" />
          Reorder threads
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        {choosing && (
          <>
            <DropdownMenuCheckboxItem
              checked={grouped}
              onSelect={stayOpen}
              onCheckedChange={(on) => onToggleGrouped(on === true)}
              className="text-[13px]"
            >
              Group by project
            </DropdownMenuCheckboxItem>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="text-muted-foreground text-[11px] font-medium">
              Projects
            </DropdownMenuLabel>
            <DropdownMenuCheckboxItem
              checked={projectsOff === 0}
              onSelect={stayOpen}
              onCheckedChange={(show) => (show ? onShowAllProjects() : onHideAllProjects())}
              className="text-[13px] font-medium"
            >
              All projects
            </DropdownMenuCheckboxItem>
            {projects.map((p) => (
              <DropdownMenuCheckboxItem
                key={p.id}
                checked={!hiddenProjects.has(p.id)}
                onSelect={stayOpen}
                onCheckedChange={(show) => onToggleProject(p.id, show)}
                className="text-[13px]"
              >
                <span className="truncate">{p.name}</span>
              </DropdownMenuCheckboxItem>
            ))}
            <DropdownMenuSeparator />
          </>
        )}

        <DropdownMenuLabel className="text-muted-foreground text-[11px] font-medium">
          Labels
        </DropdownMenuLabel>
        {labels.map((l) => (
          <DropdownMenuCheckboxItem
            key={l.id}
            checked={!hiddenLabels.has(l.id)}
            onSelect={stayOpen}
            onCheckedChange={(show) => onToggleLabel(l.id, show)}
            className="gap-2 text-[13px]"
          >
            <LabelDot color={l.color} />
            <span className="truncate">{l.name}</span>
          </DropdownMenuCheckboxItem>
        ))}
        {labels.length > 0 ? (
          <DropdownMenuCheckboxItem
            checked={!hiddenLabels.has(UNLABELLED)}
            onSelect={stayOpen}
            onCheckedChange={(show) => onToggleLabel(UNLABELLED, show)}
            className="text-muted-foreground text-[13px]"
          >
            No label
          </DropdownMenuCheckboxItem>
        ) : (
          <p className="text-muted-foreground px-2 py-1.5 text-[13px]">No labels yet.</p>
        )}

        <DropdownMenuSeparator />
        {off > 0 && (
          <DropdownMenuItem
            onSelect={() => {
              onShowAllProjects();
              onShowAllLabels();
            }}
            className="text-[13px]"
          >
            Show all
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={onManageLabels} className="gap-2 text-[13px]">
          <SettingsIcon className="size-3.5" />
          {labels.length === 0 ? "New label…" : "Manage labels…"}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
