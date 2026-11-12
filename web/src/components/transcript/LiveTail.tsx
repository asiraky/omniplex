import { terminalLines } from "~/lib/terminalText";
import { cn } from "~/lib/utils";

/**
 * The last few lines a running call has printed, under its row, so a long
 * build or test run shows it is alive without anyone opening it.
 *
 * The fold keeps the last 20 lines, and the window shows up to 20 rows of
 * them, so a wrapped line costs one from the top. The window is a column
 * that packs to its end, so whatever overflows spills off the top and the
 * newest line always sits on the bottom edge, with no scroll position to keep
 * pinned as lines arrive. Lines wrap rather than scroll sideways: a phone has
 * no width to spare and a sideways scroll inside a scrolling transcript is a
 * trap for the thumb.
 *
 * Not a live region. It changes several times a second, and a screen reader
 * announcing each change would drown out everything else; it stays readable
 * on demand under its label.
 */
export function LiveTail({ tail, className }: { tail?: string; className?: string }) {
  const lines = tail ? terminalLines(tail) : [];
  if (lines.length === 0) return null;

  return (
    <div
      role="group"
      aria-label="Latest output"
      className={cn("bg-muted/60 text-muted-foreground rounded-md px-2 py-1.5", className)}
    >
      {/* The cap is in `lh` so it is twenty rows at whatever size the text is,
          and sits on the inner box: on the padded one, the clipped line would
          show through the padding above the first whole line. */}
      <div className="flex max-h-[20lh] min-w-0 flex-col justify-end overflow-hidden font-mono text-[11px] leading-relaxed">
        <pre className="min-w-0 break-words whitespace-pre-wrap">{lines.join("\n")}</pre>
      </div>
    </div>
  );
}
