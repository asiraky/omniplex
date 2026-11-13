import { type ReactNode, useMemo } from "react";

import { type DiffLine, parsePatch } from "~/lib/patch";
import { type SplitRow, splitRows } from "~/lib/splitDiff";
import { cn } from "~/lib/utils";

function lineBase(line?: DiffLine): string {
  return line ? `${line.kind}:${line.oldNo ?? ""}:${line.newNo ?? ""}:${line.text}` : "";
}

/**
 * A key for each row that stays with it: what it shows, plus a count for the
 * rare exact repeat (a patch that touches two files can say "No newline at
 * end of file" twice).
 */
function keyed<T>(items: T[], base: (item: T) => string): { key: string; item: T }[] {
  const seen = new Map<string, number>();
  return items.map((item) => {
    const b = base(item);
    const n = seen.get(b) ?? 0;
    seen.set(b, n + 1);
    return { key: `${b}#${n}`, item };
  });
}

function splitBase(row: SplitRow): string {
  return row.kind === "pair" ? `pair:${lineBase(row.left)}|${lineBase(row.right)}` : `${row.kind}:${row.text}`;
}

function gutter(n?: number) {
  return n === undefined ? "" : String(n);
}

function escapeRegExp(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * The highlight as a pattern, or null when there is nothing to look for. A
 * regex rather than lowercasing both strings and comparing indices: a few
 * characters change length when lowercased, which would put the marks in the
 * wrong place.
 */
function matcher(highlight?: string): RegExp | null {
  if (!highlight?.trim()) return null;
  // The capture group makes split() keep the matches, at odd indices.
  return new RegExp(`(${escapeRegExp(highlight)})`, "gi");
}

/** A line's text, with every match marked. Lines are only split when a highlight is set. */
function lineText(text: string, re: RegExp | null): ReactNode {
  if (!text) return " ";
  if (!re) return text;
  const parts = text.split(re);
  if (parts.length === 1) return text;
  return parts.map((part, i) =>
    i % 2 === 1 ? (
      // Index keys are fine: the parts of one string never reorder.
      <mark key={i} data-diff-match className="bg-attention/40 text-foreground rounded-[2px]">
        {part}
      </mark>
    ) : (
      part
    ),
  );
}

const NUMBER = "text-muted-foreground/70 w-10 shrink-0 select-none px-1 text-right tabular-nums";

function Sign({ kind }: { kind: DiffLine["kind"] }) {
  return (
    <span
      className={cn(
        "w-4 shrink-0 select-none text-center",
        kind === "add" && "text-success",
        kind === "del" && "text-destructive",
      )}
    >
      {kind === "add" ? "+" : kind === "del" ? "-" : " "}
    </span>
  );
}

/**
 * A diff, with the gutters and colouring the eye expects: unified by default,
 * or old and new side by side. The panel only offers split at 768px and up;
 * a phone does not have the width for two columns of code.
 *
 * Unwrapped, each unified row is `w-max min-w-full`: the row is as wide as its
 * longest line, so a long line makes the scroll container actually scroll
 * instead of spilling out of a container-width box, and the add/delete tint
 * still covers the row once it is scrolled. Wrapped, rows are held to the
 * container and the text breaks — including mid-token, which is the only
 * thing that helps with the minified line that made someone tick the box.
 *
 * `highlight` marks every case-insensitive occurrence of a literal string,
 * for find-in-diff.
 */
export function Diff({
  patch,
  wrap = false,
  layout = "unified",
  highlight,
  className,
}: {
  patch: string;
  wrap?: boolean;
  layout?: "unified" | "split";
  highlight?: string;
  className?: string;
}) {
  const lines = useMemo(() => parsePatch(patch), [patch]);
  return <DiffLines lines={lines} wrap={wrap} layout={layout} highlight={highlight} className={className} />;
}

/** The same view for lines that never were a patch: two versions compared in the browser. */
export function DiffLines({
  lines,
  wrap = false,
  layout = "unified",
  highlight,
  className,
}: {
  lines: DiffLine[];
  wrap?: boolean;
  layout?: "unified" | "split";
  highlight?: string;
  className?: string;
}) {
  const re = useMemo(() => matcher(highlight), [highlight]);

  if (lines.length === 0) {
    return <p className="text-muted-foreground px-3 py-2 text-[12px]">No textual changes.</p>;
  }

  return layout === "split" ? (
    <Split lines={lines} re={re} className={className} />
  ) : (
    <Unified lines={lines} wrap={wrap} re={re} className={className} />
  );
}

interface BodyProps {
  lines: DiffLine[];
  wrap: boolean;
  re: RegExp | null;
  className?: string;
}

function Unified({ lines, wrap, re, className }: BodyProps) {
  const rows = useMemo(() => keyed(lines, lineBase), [lines]);
  return (
    <div className={cn("font-mono text-[11px] leading-[1.55]", className)}>
      {rows.map(({ key, item: line }) => (
        <div
          key={key}
          className={cn(
            "flex",
            wrap ? "w-full whitespace-pre-wrap [overflow-wrap:anywhere]" : "w-max min-w-full whitespace-pre",
            line.kind === "add" && "bg-success/10",
            line.kind === "del" && "bg-destructive/10",
            line.kind === "hunk" && "bg-muted/70 text-muted-foreground",
            line.kind === "meta" && "text-muted-foreground italic",
          )}
        >
          {line.kind === "hunk" || line.kind === "meta" ? (
            <span className="px-2 py-0.5">{lineText(line.text, re)}</span>
          ) : (
            <>
              <span className={NUMBER}>{gutter(line.oldNo)}</span>
              <span className={NUMBER}>{gutter(line.newNo)}</span>
              <Sign kind={line.kind} />
              <span className={cn("pr-3", wrap ? "min-w-0 flex-1" : "shrink-0")}>{lineText(line.text, re)}</span>
            </>
          )}
        </div>
      ))}
    </div>
  );
}

/**
 * Side by side. The rows are `display: contents`, so every cell sits in one
 * two-column grid: the columns line up from top to bottom, and a cell stretches
 * to its row's height, so the shorter side of a wrapped pair is still tinted
 * all the way down.
 *
 * Split always wraps, whatever `wrap` says. Unwrapped, the grid has to be as
 * wide as both sides' longest lines together, so in a panel docked beside the
 * transcript the new side sits off to the right and the view shows only the
 * old one — the opposite of what split is for. Two half-width columns that
 * break their text keep both sides on screen.
 */
function Split({ lines, re, className }: Omit<BodyProps, "wrap">) {
  const rows = useMemo(() => keyed(splitRows(lines), splitBase), [lines]);
  return (
    <div
      className={cn(
        "grid w-full grid-cols-2 font-mono text-[11px] leading-[1.55] whitespace-pre-wrap [overflow-wrap:anywhere]",
        className,
      )}
    >
      {rows.map(({ key, item: row }) =>
        row.kind === "pair" ? (
          <div key={key} data-diff-row className="contents">
            <Cell line={row.left} side="left" re={re} />
            <Cell line={row.right} side="right" re={re} />
          </div>
        ) : (
          <div
            key={key}
            data-diff-row
            className={cn(
              "col-span-2 px-2 py-0.5",
              row.kind === "hunk" ? "bg-muted/70 text-muted-foreground" : "text-muted-foreground italic",
            )}
          >
            {lineText(row.text, re)}
          </div>
        ),
      )}
    </div>
  );
}

function Cell({
  line,
  side,
  re,
}: {
  line?: DiffLine;
  side: "left" | "right";
  re: RegExp | null;
}) {
  const edge = side === "left" && "border-border/60 border-r";
  // Nothing on this side: the line exists only in the other version.
  if (!line) return <div data-side={side} data-empty className={cn("bg-muted/40", edge)} />;
  return (
    <div
      data-side={side}
      className={cn(
        "flex min-w-0",
        edge,
        line.kind === "add" && "bg-success/10",
        line.kind === "del" && "bg-destructive/10",
      )}
    >
      <span className={NUMBER}>{gutter(side === "left" ? line.oldNo : line.newNo)}</span>
      <Sign kind={line.kind} />
      <span className="min-w-0 flex-1 pr-3">{lineText(line.text, re)}</span>
    </div>
  );
}
