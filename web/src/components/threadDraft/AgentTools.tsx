import { ChevronDownIcon, ShieldIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { LEVELS } from "~/lib/permissions";
import { cn } from "~/lib/utils";
import type { HarnessMeta, PermissionModeMeta } from "~/protocol";
import { Chip, Described } from "./parts";
import { chipClass } from "./shared";

/** How much the agent may do without asking. */
function PermissionsMenu({
  harness,
  modes,
  modeId,
  onPick,
  className,
}: {
  harness: HarnessMeta | undefined;
  modes: PermissionModeMeta[];
  modeId: string;
  onPick: (mode: string) => void;
  className?: string;
}) {
  // The harness's own permission modes, folded away behind the levels.
  const [advanced, setAdvanced] = useState(false);
  const modeMeta = modes.find((m) => m.id === modeId);
  return (
    <DropdownMenu
      // Opens on the levels unless the current mode is not one of them.
      onOpenChange={(open) => open && setAdvanced(!!modeMeta && !modeMeta.level)}
    >
      <DropdownMenuTrigger asChild>
        {/* On a phone the shield stands alone, in the toolbar or the drawer;
            the level is still in its name and its menu. */}
        <Chip label="Permissions" icon={<ShieldIcon />} className={cn(className, "max-md:[&>span]:hidden")}>
          {LEVELS.find((l) => l.id === modeMeta?.level)?.short ?? modeMeta?.label}
        </Chip>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-w-[min(22rem,calc(100vw-2rem))]">
        {/* The three levels mean the same on every harness. Its own
            modes, Plan among them, wait under Advanced. */}
        <DropdownMenuRadioGroup value={modeId} onValueChange={onPick}>
          {LEVELS.map((l) => {
            const m = modes.find((x) => x.level === l.id);
            return (
              m && (
                <DropdownMenuRadioItem key={l.id} value={m.id}>
                  <Described title={l.label} hint={`${harness?.name}: ${m.label}`} />
                </DropdownMenuRadioItem>
              )
            );
          })}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          aria-expanded={advanced}
          onSelect={(e) => {
            e.preventDefault();
            setAdvanced(!advanced);
          }}
          className="text-muted-foreground text-[12px]"
        >
          <ChevronDownIcon className={cn("transition-transform", !advanced && "-rotate-90")} />
          Advanced
        </DropdownMenuItem>
        {advanced && (
          <DropdownMenuRadioGroup value={modeId} onValueChange={onPick}>
            {modes.map((m) => (
              <DropdownMenuRadioItem key={m.id} value={m.id}>
                <Described title={m.label} hint={m.description} />
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * The draft's agent settings: permissions, and the 1M window. In the composer's
 * toolbar, or its drawer when the toolbar is too narrow to hold them beside the
 * model's name; `className` sizes them to whichever.
 */
export function AgentTools({
  harness,
  modes,
  modeId,
  supports1m,
  want1m,
  onPickMode,
  onToggle1m,
  className,
}: {
  harness: HarnessMeta | undefined;
  modes: PermissionModeMeta[];
  modeId: string;
  supports1m: boolean;
  want1m: boolean;
  onPickMode: (mode: string) => void;
  onToggle1m: () => void;
  className?: string;
}) {
  return (
    <>
      {modes.length > 0 && (
        <PermissionsMenu
          harness={harness}
          modes={modes}
          modeId={modeId}
          onPick={onPickMode}
          className={className}
        />
      )}

      {supports1m && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          aria-label="1M context"
          aria-pressed={want1m}
          title={want1m ? "Larger window, higher cost" : "Standard window"}
          onClick={onToggle1m}
          className={cn(chipClass, className, want1m && "bg-accent text-foreground")}
        >
          1M
        </Button>
      )}
    </>
  );
}
