import { FolderIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { cn } from "~/lib/utils";
import type { Project } from "~/protocol";

/**
 * Which projects the sidebar is showing.
 *
 * The same shape as the label filter beside it — a checkbox per entry, the
 * menu staying open while you work through it — because they do the same job
 * and switching between two different idioms in one header would be its own
 * small tax. What it narrows, though, is the list's structure rather than a
 * decoration on it: with more than one project left showing, the sidebar
 * groups under headers.
 *
 * "All projects" leads, because turning everything back on is the common way
 * out of a filter and doing it one project at a time is the annoying way.
 * Unchecking it switches everything off, which empties the list — recoverable
 * from the empty state's own button, and worth keeping symmetrical rather than
 * making the one control in the menu behave differently from the rest.
 */
export function ProjectFilter({
  projects,
  hidden,
  onToggle,
  onShowAll,
  onHideAll,
}: {
  projects: Project[];
  /** Project ids currently switched off. */
  hidden: Set<string>;
  onToggle: (id: string, show: boolean) => void;
  onShowAll: () => void;
  onHideAll: () => void;
}) {
  // Only a hidden id that still names a real project counts: one left behind
  // by a deleted project hides nothing, so it must not light the trigger
  // either. With one project there is nothing to filter between at all.
  const offCount = projects.filter((p) => hidden.has(p.id)).length;
  const filtering = offCount > 0;
  const name = filtering ? `Filter by project — ${offCount} hidden` : "Filter by project";

  if (projects.length < 2) return null;

  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={name}
              // Matches LabelFilter's square exactly: 44px for a thumb, 32px
              // for a pointer, and accent-coloured while a filter is on so the
              // header admits that sessions are missing.
              className={cn(
                "size-11 shrink-0 md:size-8",
                filtering ? "text-primary" : "text-muted-foreground hover:text-foreground",
              )}
            >
              <FolderIcon />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{name}</TooltipContent>
      </Tooltip>

      <DropdownMenuContent align="end" className="min-w-44">
        <DropdownMenuCheckboxItem
          checked={!filtering}
          // Radix closes the menu on select by default, which would make
          // narrowing to two projects a trip through the trigger for each.
          onSelect={(e) => e.preventDefault()}
          onCheckedChange={(show) => (show ? onShowAll() : onHideAll())}
          className="text-[13px] font-medium"
        >
          All projects
        </DropdownMenuCheckboxItem>
        <DropdownMenuSeparator />

        {projects.map((p) => (
          <DropdownMenuCheckboxItem
            key={p.id}
            checked={!hidden.has(p.id)}
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={(show) => onToggle(p.id, show)}
            className="gap-2 text-[13px]"
          >
            <span className="truncate">{p.config.name}</span>
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
