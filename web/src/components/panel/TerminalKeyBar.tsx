import type { PointerEvent, MouseEvent } from "react";

import type { BarKey, Mods } from "~/lib/terminalKeys";
import { cn } from "~/lib/utils";

type BarEntry = { key: BarKey; label: string; name?: string } | { mod: keyof Mods; label: string };

// Esc and Tab first, the modifiers next to them, then the arrows and the
// shell characters a phone keyboard buries a page or two deep.
const ENTRIES: BarEntry[] = [
  { key: "esc", label: "esc" },
  { key: "tab", label: "tab" },
  { mod: "ctrl", label: "ctrl" },
  { mod: "alt", label: "alt" },
  { key: "left", label: "←", name: "Left" },
  { key: "up", label: "↑", name: "Up" },
  { key: "down", label: "↓", name: "Down" },
  { key: "right", label: "→", name: "Right" },
  { key: "|", label: "|" },
  { key: "~", label: "~" },
  { key: "/", label: "/" },
  { key: "-", label: "-" },
];

// Not taking focus is what keeps the phone keyboard up: focus leaving the
// terminal's textarea is what puts it away.
const keepFocus = (e: PointerEvent | MouseEvent) => e.preventDefault();

/**
 * The keys a phone keyboard lacks, in a row under the terminal. Ctrl and Alt
 * are armed here and spent by whoever sends the next key, this bar or the
 * phone keyboard, so the caller holds them.
 */
export function TerminalKeyBar({
  mods,
  onMods,
  onKey,
}: {
  mods: Mods;
  onMods: (mods: Mods) => void;
  /** A key tapped, to send with the armed modifiers and then disarm them. */
  onKey: (key: BarKey) => void;
}) {
  return (
    <div
      role="toolbar"
      aria-label="Terminal keys"
      className="flex shrink-0 overflow-x-auto border-t border-white/10 bg-neutral-900 font-mono [scrollbar-width:none]"
    >
      {ENTRIES.map((entry) => {
        const pressed = "mod" in entry ? mods[entry.mod] : undefined;
        return (
          <button
            key={entry.label}
            type="button"
            aria-label={"name" in entry ? entry.name : undefined}
            aria-pressed={pressed}
            onPointerDown={keepFocus}
            onMouseDown={keepFocus}
            onClick={() =>
              "mod" in entry ? onMods({ ...mods, [entry.mod]: !mods[entry.mod] }) : onKey(entry.key)
            }
            className={cn(
              "h-11 min-w-11 shrink-0 px-3 text-[13px] text-white/80 transition-colors outline-none active:bg-white/15",
              pressed && "bg-white/20 text-white",
            )}
          >
            {entry.label}
          </button>
        );
      })}
    </div>
  );
}
