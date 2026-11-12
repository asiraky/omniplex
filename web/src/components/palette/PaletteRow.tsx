import { ChevronRightIcon } from "lucide-react";
import { createContext, useContext, type ReactNode } from "react";

import { CommandItem } from "~/components/ui/command";
import { KbdChord } from "~/components/ui/kbd";
import type { CommandId } from "~/lib/keybindings";
import { cn } from "~/lib/utils";
import { useShortcut } from "~/useShortcuts";

/** True when the palette is the phone's sheet: rows grow to a 44px touch target. */
export const PaletteTouch = createContext(false);

/**
 * One row. The label truncates before anything to its right does: the detail
 * (a project, a path), then the chord, then a chevron on rows that open a
 * further step rather than doing something.
 */
export function PaletteRow({
  value,
  onSelect,
  label,
  detail,
  note,
  command,
  opensStep,
}: {
  /** cmdk's identity for the row: unique within the page. */
  value: string;
  onSelect: () => void;
  label: ReactNode;
  detail?: ReactNode;
  /** A warning beside the label: a worktree another thread is in. */
  note?: ReactNode;
  command?: CommandId;
  opensStep?: boolean;
}) {
  const touch = useContext(PaletteTouch);
  const chord = useShortcut(command);
  return (
    <CommandItem
      value={value}
      onSelect={onSelect}
      className={cn("gap-3", touch ? "min-h-11 px-3 text-[15px]" : "min-h-8")}
    >
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {note && <span className="text-attention-foreground shrink-0 text-xs">{note}</span>}
      {detail && (
        <span className="text-muted-foreground max-w-[45%] shrink truncate text-xs">{detail}</span>
      )}
      {chord && <KbdChord shortcut={chord.shortcut} platform={chord.platform} className="shrink-0" />}
      {opensStep && <ChevronRightIcon aria-hidden className="shrink-0" />}
    </CommandItem>
  );
}
