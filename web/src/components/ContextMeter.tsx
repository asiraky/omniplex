import { type PointerEvent as ReactPointerEvent, useRef, useState } from "react";

import { Popover, PopoverContent, PopoverTrigger } from "~/components/ui/popover";
import { fmtPct, fmtTokens } from "~/lib/format";
import { cn } from "~/lib/utils";
import type { Usage } from "~/protocol";

// The one threshold, shared by the ring and the bar: below it the meter is
// quiet, at or above it the meter turns to the destructive colour because the
// next compaction is close. A single line, deliberately — a traffic-light
// gradient reads as three states when there are only two that matter.
const WARN_AT = 90;

// Brand-neutral segment palette for the by-category bar. Plain Tailwind shades
// so they are legible in both themes without depending on theme tokens.
const SEGMENT = [
  "bg-sky-400",
  "bg-violet-400",
  "bg-emerald-400",
  "bg-amber-400",
  "bg-rose-400",
  "bg-teal-400",
  "bg-fuchsia-400",
  "bg-slate-400",
];

/**
 * The context gauge: a donut ring in the composer footer that fills as the
 * window does, and — on hover, focus, or tap — a popover that says exactly
 * where the conversation stands against auto-compaction. The ring alone never
 * shows a number; the popover carries the detail, kept strictly separate from
 * the session's cost accounting so the two can never be confused (the mistake
 * that let a summed-per-turn total read as a full window in the first place).
 */
export function ContextMeter({ usage, model }: { usage: Usage; model?: string }) {
  const [open, setOpen] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const openSoon = () => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setOpen(true), 150);
  };
  // A grace period, not an immediate close: the pointer has to cross the gap
  // between the ring and the popover, and moving onto the popover cancels it.
  const closeSoon = () => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setOpen(false), 120);
  };
  const cancelClose = () => clearTimeout(timer.current);
  // Touch has no hover: after a tap the pointer immediately leaves, and a
  // finger cannot move onto the portalled popover to cancel a scheduled close.
  // So hover-open/close is mouse-only; touch falls through to the trigger's
  // click, which Radix toggles, and an outside tap closes it.
  const hoverOnly = (fn: () => void) => (e: ReactPointerEvent) => {
    if (e.pointerType !== "touch") fn();
  };

  const used = usage.contextUsed ?? 0;
  const win = usage.contextWindow ?? 0;
  const hasWindow = win > 0;
  const hasPct = hasWindow || usage.contextPct !== undefined;
  // Measured against the compaction window, computed from the raw tokens rather
  // than a supplied percentage so an adapter that clamps its own pct (Codex)
  // still reads as over-limit here. It answers "how close am I to compaction".
  const pct = hasWindow ? (used / win) * 100 : (usage.contextPct ?? 0);
  const over = hasWindow ? used > win : pct > 100;
  const near = hasPct && pct >= WARN_AT;

  // Whether the harness will compact on its own. Undefined (an adapter that
  // does not report it) is treated as "unknown", not "off".
  const autoCompactOff = usage.autoCompact === false;

  // The bar is drawn against the model's full window when that is larger than
  // the compaction window, so the compaction point can sit as an interior
  // marker rather than pinned to the right edge.
  const limit = usage.contextLimit && usage.contextLimit > win ? usage.contextLimit : win;
  // The threshold marker: the reported auto-compaction trigger if there is one,
  // otherwise the compaction window when it sits below the model's full window.
  const threshold =
    usage.autoCompactThreshold && usage.autoCompactThreshold > 0
      ? usage.autoCompactThreshold
      : limit > win && win > 0
        ? win
        : undefined;
  const barMax = Math.max(limit, used, threshold ?? 0, 1);
  const compactionMarker =
    !autoCompactOff && threshold !== undefined ? (threshold / barMax) * 100 : undefined;

  const categories = (usage.contextCategories ?? []).filter((c) => c.tokens > 0);
  const ringTone = near ? "text-destructive" : "text-muted-foreground";

  const r = 6;
  const c = 2 * Math.PI * r;
  const ringPct = hasPct ? Math.min(100, Math.max(0, pct)) : 0;
  const fillPct = hasWindow ? (used / barMax) * 100 : ringPct;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        onPointerEnter={hoverOnly(openSoon)}
        onPointerLeave={hoverOnly(closeSoon)}
        onFocus={() => setOpen(true)}
        onBlur={closeSoon}
        aria-label={hasPct ? `Context ${fmtPct(pct)} used` : "Context usage unavailable"}
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring",
          ringTone,
        )}
      >
        <svg aria-hidden viewBox="0 0 16 16" className="size-4 -rotate-90">
          <circle cx="8" cy="8" r={r} fill="none" strokeWidth="2.5" className="stroke-border" />
          <circle
            cx="8"
            cy="8"
            r={r}
            fill="none"
            strokeWidth="2.5"
            strokeLinecap="round"
            stroke="currentColor"
            strokeDasharray={c}
            strokeDashoffset={c * (1 - ringPct / 100)}
          />
        </svg>
      </PopoverTrigger>

      <PopoverContent
        side="top"
        align="end"
        sideOffset={8}
        className="w-64 p-3"
        onOpenAutoFocus={(e) => e.preventDefault()}
        onPointerEnter={hoverOnly(cancelClose)}
        onPointerLeave={hoverOnly(closeSoon)}
      >
        <div className="flex items-baseline justify-between gap-2">
          <span className={cn("text-sm font-medium tabular-nums", near && "text-destructive")}>
            {hasPct ? fmtPct(pct) : "—"}
          </span>
          <span className="text-muted-foreground text-xs tabular-nums">
            {fmtTokens(used)}{hasWindow && ` / ${fmtTokens(win)}`}
          </span>
        </div>

        {/* The occupancy bar. Segmented by category when the harness reports
            the breakdown, otherwise a single fill. The compaction threshold,
            when it sits below the model's full window, is a marker on the bar
            rather than a sentence. */}
        <div
          role="progressbar"
          aria-valuenow={hasPct ? Math.round(pct) : undefined}
          aria-valuemin={0}
          aria-valuemax={100}
          className="bg-muted relative mt-2 h-1.5 w-full overflow-hidden rounded-full"
        >
          {hasWindow && categories.length > 0 ? (
            // inset-0, not inset-y-0 left-0: an absolutely positioned box with
            // no right edge shrinks to fit, and its children's percentage
            // widths would then resolve against an indefinite width and
            // collapse. Spanning the full track gives them something to size to.
            <div className="absolute inset-0 flex">
              {categories.map((cat, i) => (
                <div
                  key={cat.name}
                  className={SEGMENT[i % SEGMENT.length]}
                  style={{ width: `${(cat.tokens / barMax) * 100}%` }}
                />
              ))}
            </div>
          ) : (
            <div
              className={cn("absolute inset-y-0 left-0", near ? "bg-destructive" : "bg-primary")}
              style={{ width: `${Math.min(100, fillPct)}%` }}
            />
          )}
          {compactionMarker !== undefined && (
            <div
              className="bg-foreground/70 absolute inset-y-0 w-px"
              style={{ left: `${compactionMarker}%` }}
              aria-hidden
            />
          )}
        </div>

        {categories.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1">
            {categories.map((cat, i) => (
              <span
                key={cat.name}
                className="text-muted-foreground flex items-center gap-1 text-[11px]"
              >
                <span className={cn("size-2 rounded-[2px]", SEGMENT[i % SEGMENT.length])} />
                {cat.name}
                <span className="tabular-nums">{fmtTokens(cat.tokens)}</span>
              </span>
            ))}
          </div>
        )}

        <div className="mt-2.5 border-t pt-2.5 text-[11px] leading-relaxed">
          <p className={cn(over ? "text-destructive" : "text-muted-foreground")}>
            {!hasWindow
              ? "Context window unavailable."
              : autoCompactOff
              ? over
                ? `Over the ${fmtTokens(win)} context limit by ${fmtTokens(used - win)}.`
                : `Auto-compaction off — hard limit at ${fmtTokens(win)}.`
              : over
                ? `Past the compaction point by ${fmtTokens(used - win)} — compaction is imminent.`
                : compactionMarker !== undefined
                  ? `Auto-compaction near ${fmtTokens(threshold ?? win)}, before the model's ${fmtTokens(limit)} window.`
                  : `Auto-compaction near ${fmtTokens(threshold ?? win)}.`}
          </p>
          {model && <p className="text-muted-foreground/70 mt-1 truncate">{model}</p>}
        </div>

        {/* Cost accounting, walled off from the occupancy above: these are the
            last turn's totals, not what is in the window right now. */}
        {(usage.input > 0 || usage.output > 0 || usage.cacheRead > 0) && (
          <div className="text-muted-foreground/70 mt-2 flex flex-wrap gap-x-3 border-t pt-2 text-[10px] tabular-nums">
            <span>Last turn: {fmtTokens(usage.input + usage.output)} processed</span>
            {usage.cacheRead > 0 && <span>{fmtTokens(usage.cacheRead)} cached</span>}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
