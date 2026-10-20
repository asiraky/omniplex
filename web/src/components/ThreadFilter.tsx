import { ArrowUpDownIcon, ListFilterIcon } from "lucide-react";

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
import { cn } from "~/lib/utils";
import type { Project } from "~/protocol";

/**
 * The sidebar's one menu: how the list is laid out, and which projects show.
 *
 * Every project entry is a checkbox and the menu stays open while you work
 * through it: filtering is usually two or three toggles, not one.
 *
 * "All projects" is there because turning everything back on is the common way
 * out, and unchecking it switches every project off, keeping it the same kind
 * of control as the rest. "Show all" says the same thing in words once
 * something is hidden.
 *
 * "Group by project" leads the project choices: it is not a filter, it decides
 * how what the filter leaves is laid out. It only means anything with more
 * than one project to group, so it shows when the project choices do, and it
 * never lights the trigger — a flat list hides nothing.
 */
export function ThreadFilter({
  grouped,
  onToggleGrouped,
  projects,
  hiddenProjects,
  onToggleProject,
  onShowAllProjects,
  onHideAllProjects,
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
  /** Puts the list into reorder mode. */
  onReorder: () => void;
}) {
  // Only what is both hidden and still real counts: an id left behind by a
  // deleted project hides nothing, so it must not light the trigger. With one
  // project there is nothing to choose between.
  const choosing = projects.length > 1;
  const off = choosing ? projects.filter((p) => hiddenProjects.has(p.id)).length : 0;
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
        {choosing && (
          <>
            <DropdownMenuSeparator />
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
              checked={off === 0}
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
          </>
        )}
        {off > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={onShowAllProjects} className="text-[13px]">
              Show all
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
