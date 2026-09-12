import { XIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "~/lib/utils";

/**
 * One thing riding along with the next message: a collapsed paste, or — on a
 * phone, where there is no inline pill — a referenced file.
 *
 * Two targets in one chip, so the body opens the quickview and the corner
 * takes it back. The remove button is always visible and 24px square rather
 * than appearing on hover: half of this is used on a touch screen, where there
 * is no hover to reveal anything and a 12px glyph is not a button.
 */
export function ComposerChip({
  icon,
  label,
  sub,
  onOpen,
  onRemove,
  removeLabel,
  className,
}: {
  icon: ReactNode;
  label: string;
  /** The sneak peek: enough to tell two log dumps apart without opening them. */
  sub?: string;
  onOpen?: () => void;
  onRemove: () => void;
  removeLabel: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "bg-muted/60 relative flex max-w-[min(15rem,60vw)] min-w-0 items-center gap-1.5 rounded-lg border py-1 pr-7 pl-2",
        className,
      )}
    >
      <span className="shrink-0">{icon}</span>
      <button
        type="button"
        onClick={onOpen}
        disabled={!onOpen}
        title={onOpen ? `${label} — tap to preview` : label}
        className="focus-visible:ring-ring min-w-0 flex-1 rounded text-left outline-none focus-visible:ring-2 enabled:cursor-pointer"
      >
        <span className="block truncate font-mono text-[11px] leading-tight">{label}</span>
        {sub && (
          <span className="text-muted-foreground block truncate font-mono text-[10px] leading-tight">
            {sub}
          </span>
        )}
      </button>
      <button
        type="button"
        onClick={onRemove}
        aria-label={removeLabel}
        className="text-muted-foreground hover:text-foreground focus-visible:ring-ring absolute top-1/2 right-0.5 grid size-6 -translate-y-1/2 place-items-center rounded-md outline-none focus-visible:ring-2"
      >
        <XIcon className="size-3.5" />
      </button>
    </span>
  );
}
