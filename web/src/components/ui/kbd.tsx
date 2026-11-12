import type * as React from "react";

import { shortcutParts, type Platform, type Shortcut } from "~/lib/keybindings";
import { cn } from "~/lib/utils";

/** One key, as a chip. */
function Kbd({ className, ...props }: React.ComponentProps<"kbd">) {
  return (
    <kbd
      data-slot="kbd"
      className={cn(
        "inline-flex h-5 min-w-5 items-center justify-center rounded border bg-muted px-1 font-sans text-[11px] leading-none font-medium text-muted-foreground",
        className,
      )}
      {...props}
    />
  );
}

/** A whole chord, one chip per key: ⇧ ⌘ M, or Ctrl Shift M. */
function KbdChord({
  shortcut,
  platform,
  className,
}: {
  shortcut: Shortcut;
  platform: Platform;
  className?: string;
}) {
  return (
    <span data-slot="kbd-chord" className={cn("inline-flex items-center gap-0.5", className)}>
      {shortcutParts(shortcut, platform).map((part, i) => (
        <Kbd key={i}>{part}</Kbd>
      ))}
    </span>
  );
}

export { Kbd, KbdChord };
