import type { ComponentProps, ReactNode } from "react";

import { Button } from "~/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import type { CommandId } from "~/lib/keybindings";
import { cn } from "~/lib/utils";
import { useShortcutLabel } from "~/useShortcuts";

/**
 * Every icon-only control in the app goes through here, which is what makes
 * "has an accessible name" and "is big enough for a thumb" properties of the
 * component rather than of whoever remembered them. The label is required, and
 * it is both the accessible name and the tooltip text.
 *
 * `shortcut` names the command the button also answers to; the tooltip
 * follows the label with its chord, where hints are shown at all. The
 * accessible name stays the label alone.
 */
export function IconButton({
  label,
  children,
  className,
  variant = "ghost",
  shortcut,
  ...props
}: Omit<ComponentProps<typeof Button>, "size" | "aria-label"> & {
  label: string;
  children: ReactNode;
  shortcut?: CommandId;
}) {
  const chord = useShortcutLabel(shortcut);
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          {...props}
          variant={variant}
          size="icon"
          aria-label={label}
          // 44px is the touch target on a phone; a pointer needs no such
          // slack, so on desktop the button hugs its 16px icon — the same
          // 32px square as the sidebar rows' delete X.
          className={cn("size-11 shrink-0 md:size-8", className)}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {label}
        {/* Quieter than the label: it is the aside, not the name. */}
        {chord && <span className="ml-1.5 opacity-60">{chord}</span>}
      </TooltipContent>
    </Tooltip>
  );
}
