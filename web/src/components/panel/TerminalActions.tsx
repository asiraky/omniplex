import { MessageSquarePlusIcon, TextQuoteIcon } from "lucide-react";
import { useRef } from "react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { cn } from "~/lib/utils";
import { useCanHover } from "~/useMediaQuery";

/** The choices of "send the last N lines": a command's tail, a screenful, a long log. */
const LAST_LINES = [20, 50, 200];

// Opaque, not see-through: these sit over the output, and text showing
// through a pill makes both unreadable. On a touch screen they are a fingertip's
// 44px, the status one too so the row lines up.
const PILL =
  "flex h-6 items-center gap-1.5 rounded-full bg-neutral-900 px-2 py-0.5 text-[11px] text-white/80 ring-1 ring-white/15 pointer-coarse:h-11";
const PILL_BUTTON = cn(
  PILL,
  "pointer-events-auto transition-colors outline-none hover:bg-neutral-800 hover:text-white focus-visible:ring-white/60 pointer-coarse:px-3 pointer-coarse:text-[12px]",
);

/**
 * The pills in a terminal's top corner: that it is reconnecting, and, given
 * `quote`, the ways to put its output in the chat.
 */
export function TerminalActions({
  reconnecting,
  selected,
  quote,
  onRefocus,
}: {
  reconnecting: boolean;
  /** Whether the terminal has a selection to add. */
  selected: boolean;
  quote?: {
    addSelection: () => void;
    /** Sends the last `n` lines; false when there was nothing to send. */
    sendLast: (n: number) => boolean;
  };
  /** Puts focus back in the terminal. */
  onRefocus: () => void;
}) {
  const canHover = useCanHover();
  // Whether the last-lines menu closed on a send, for where focus goes next.
  const sentRef = useRef(false);

  return (
    // One row, so the pills sit side by side rather than on top of each other.
    // Only the buttons take pointer events; the rest lets clicks and drags
    // through to the terminal.
    <div className="pointer-events-none absolute top-2 right-2 flex items-center gap-1.5">
      {/* The output stays up while the socket is away: the shell is still
          running, and what it printed meanwhile replays on reconnect. */}
      {reconnecting && (
        <div role="status" className={PILL}>
          <span className="size-1.5 animate-pulse rounded-full bg-amber-400" />
          Reconnecting…
        </div>
      )}
      {quote && selected && (
        <button
          type="button"
          // The terminal keeps its selection and its focus through the click.
          onMouseDown={(e) => e.preventDefault()}
          onClick={quote.addSelection}
          className={PILL_BUTTON}
        >
          <MessageSquarePlusIcon className="size-3.5" />
          Add to chat
        </button>
      )}
      {/* Selecting on a touch screen is hard going in xterm, so this is the
          phone's way to quote output; with a mouse it waits for the pointer
          to come over the terminal rather than sit on top of the output. */}
      {quote && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              type="button"
              aria-label="Send recent output to chat"
              onMouseDown={(e) => e.preventDefault()}
              className={cn(
                PILL_BUTTON,
                canHover &&
                  "opacity-0 group-hover/terminal:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100",
              )}
            >
              <TextQuoteIcon className="size-3.5" />
              <span className="hidden pointer-coarse:inline">Send output</span>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            align="end"
            // Not back to the trigger: after a send the caller puts focus
            // where the quote went, and dismissed with a mouse it goes back
            // to the shell. A touch screen gets neither, since focusing the
            // terminal raises the keyboard.
            onCloseAutoFocus={(e) => {
              e.preventDefault();
              if (canHover && !sentRef.current) onRefocus();
              sentRef.current = false;
            }}
          >
            <DropdownMenuLabel>Send to chat</DropdownMenuLabel>
            {LAST_LINES.map((n) => (
              <DropdownMenuItem
                key={n}
                className="pointer-coarse:min-h-11"
                onSelect={() => {
                  sentRef.current = quote.sendLast(n);
                }}
              >
                Last {n} lines
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}
