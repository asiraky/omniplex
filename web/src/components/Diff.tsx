import { Fragment, type ReactNode, useMemo, useState } from "react";

import { Button } from "~/components/ui/button";
import { Textarea } from "~/components/ui/textarea";
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
 * Unwrapped, a long line makes the scroll container scroll sideways (see
 * `Unified`). Wrapped, rows are held to the container and the text breaks —
 * including mid-token, which is the only thing that helps with the minified
 * line that made someone tick the box.
 *
 * `highlight` marks every case-insensitive occurrence of a literal string,
 * for find-in-diff.
 *
 * With `onCommentLine`, a click or tap on a code line selects it and opens a
 * comment box under it; "Add to chat" hands the line, its side and the text
 * up. The whole row is the target, so a phone needs no horizontal precision,
 * and a mis-tap is fixed by tapping the right line: the box moves and keeps
 * what was typed. A click that ends a text selection is left alone, so code
 * can still be selected and copied. Without the prop the diff is read-only.
 */
export function Diff({
  patch,
  wrap = false,
  layout = "unified",
  highlight,
  onCommentLine,
  className,
}: {
  patch: string;
  wrap?: boolean;
  layout?: "unified" | "split";
  highlight?: string;
  onCommentLine?: (target: CommentTarget, comment: string) => void;
  className?: string;
}) {
  const lines = useMemo(() => parsePatch(patch), [patch]);
  return (
    <DiffLines
      lines={lines}
      wrap={wrap}
      layout={layout}
      highlight={highlight}
      onCommentLine={onCommentLine}
      className={className}
    />
  );
}

/** A commented line, and which version's numbering it belongs to. */
export interface CommentTarget {
  line: DiffLine;
  side: "old" | "new";
}

/** The same view for lines that never were a patch: two versions compared in the browser. */
export function DiffLines({
  lines,
  wrap = false,
  layout = "unified",
  highlight,
  onCommentLine,
  className,
}: {
  lines: DiffLine[];
  wrap?: boolean;
  layout?: "unified" | "split";
  highlight?: string;
  onCommentLine?: (target: CommentTarget, comment: string) => void;
  className?: string;
}) {
  const re = useMemo(() => matcher(highlight), [highlight]);
  // The selection is held by row key, not by line, so a re-read that drops
  // the row simply stops rendering the box.
  const [picked, setPicked] = useState<Picked | null>(null);
  const [draft, setDraft] = useState("");

  if (lines.length === 0) {
    return <p className="text-muted-foreground px-3 py-2 text-[12px]">No textual changes.</p>;
  }

  const close = () => {
    setPicked(null);
    setDraft("");
  };
  const comments: Comments | undefined = onCommentLine && {
    picked,
    pick: (next) => {
      // The click that finishes a drag-select is not a tap on the line.
      if (window.getSelection()?.toString()) return;
      setPicked(next);
    },
    box: (target, className) => (
      <CommentBox
        draft={draft}
        onDraft={setDraft}
        onAdd={() => {
          const comment = draft.trim();
          if (!comment) return;
          onCommentLine(target, comment);
          close();
        }}
        onCancel={close}
        className={className}
      />
    ),
  };

  return layout === "split" ? (
    <Split lines={lines} re={re} comments={comments} className={className} />
  ) : (
    <Unified lines={lines} wrap={wrap} re={re} comments={comments} className={className} />
  );
}

/** Which row is selected for a comment; in split view, which cell of it. */
interface Picked {
  key: string;
  side: "old" | "new";
}

interface Comments {
  picked: Picked | null;
  pick: (picked: Picked) => void;
  box: (target: CommentTarget, className?: string) => ReactNode;
}

interface BodyProps {
  lines: DiffLine[];
  wrap: boolean;
  re: RegExp | null;
  comments?: Comments;
  className?: string;
}

// An inset ring rather than a fill: the fill already says added or removed,
// and a find match is a fill too.
const SELECTED = "ring-primary ring-2 ring-inset";

function isCode(line: DiffLine): boolean {
  return line.kind === "add" || line.kind === "del" || line.kind === "context";
}

/**
 * Unwrapped, the rows sit in one `w-max min-w-full` block, so every row is as
 * wide as the longest line: a long line makes the scroll container actually
 * scroll, and every row's tint reaches the far edge once it has.
 *
 * The comment box is sticky at the left and `100cqw` wide, so it stays in view
 * however far the diff is scrolled. Sticky only moves within its containing
 * block, which is why the rows' block has to be the full scroll width; and the
 * one length that is the visible width rather than that block's is a container
 * unit, so the outer div is the container.
 */
function Unified({ lines, wrap, re, comments, className }: BodyProps) {
  const rows = useMemo(() => keyed(lines, lineBase), [lines]);
  return (
    <div className={cn("font-mono text-[11px] leading-[1.55]", comments && "@container", className)}>
      <div className={cn(!wrap && "w-max min-w-full")}>
        {rows.map(({ key, item: line }) => {
          const side = line.kind === "del" ? "old" : "new";
          const target = comments && isCode(line);
          const selected = target && comments?.picked?.key === key;
          return (
            <Fragment key={key}>
              <div
                data-diff-row
                data-selected={selected || undefined}
                onClick={target ? () => comments?.pick({ key, side }) : undefined}
                className={cn(
                  "flex",
                  wrap ? "whitespace-pre-wrap [overflow-wrap:anywhere]" : "whitespace-pre",
                  line.kind === "add" && "bg-success/10",
                  line.kind === "del" && "bg-destructive/10",
                  line.kind === "hunk" && "bg-muted/70 text-muted-foreground",
                  line.kind === "meta" && "text-muted-foreground italic",
                  target && "cursor-pointer",
                  selected && SELECTED,
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
              {selected && comments?.box({ line, side }, "sticky left-0 w-[100cqw]")}
            </Fragment>
          );
        })}
      </div>
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
function Split({ lines, re, comments, className }: Omit<BodyProps, "wrap">) {
  const rows = useMemo(() => keyed(splitRows(lines), splitBase), [lines]);
  return (
    <div
      className={cn(
        "grid w-full grid-cols-2 font-mono text-[11px] leading-[1.55] whitespace-pre-wrap [overflow-wrap:anywhere]",
        className,
      )}
    >
      {rows.map(({ key, item: row }) => {
        if (row.kind === "pair") {
          const picked = comments?.picked?.key === key ? comments.picked : null;
          const line = picked?.side === "old" ? row.left : picked?.side === "new" ? row.right : undefined;
          const cell = (side: "left" | "right", l?: DiffLine) => {
            const which = side === "left" ? "old" : "new";
            return (
              <Cell
                line={l}
                side={side}
                re={re}
                selected={picked?.side === which}
                onPick={comments && l ? () => comments.pick({ key, side: which }) : undefined}
              />
            );
          };
          return (
            <Fragment key={key}>
              <div data-diff-row className="contents">
                {cell("left", row.left)}
                {cell("right", row.right)}
              </div>
              {line && picked && comments?.box({ line, side: picked.side }, "col-span-2")}
            </Fragment>
          );
        }
        return (
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
        );
      })}
    </div>
  );
}

function Cell({
  line,
  side,
  re,
  selected = false,
  onPick,
}: {
  line?: DiffLine;
  side: "left" | "right";
  re: RegExp | null;
  selected?: boolean;
  onPick?: () => void;
}) {
  const edge = side === "left" && "border-border/60 border-r";
  // Nothing on this side: the line exists only in the other version.
  if (!line) return <div data-side={side} data-empty className={cn("bg-muted/40", edge)} />;
  return (
    <div
      data-side={side}
      data-selected={selected || undefined}
      onClick={onPick}
      className={cn(
        "flex min-w-0",
        edge,
        line.kind === "add" && "bg-success/10",
        line.kind === "del" && "bg-destructive/10",
        onPick && "cursor-pointer",
        selected && SELECTED,
      )}
    >
      <span className={NUMBER}>{gutter(side === "left" ? line.oldNo : line.newNo)}</span>
      <Sign kind={line.kind} />
      <span className="min-w-0 flex-1 pr-3">{lineText(line.text, re)}</span>
    </div>
  );
}

/**
 * The box under a selected line. It sets its own type: the diff around it is
 * 11px monospace, and a textarea under 16px makes iOS zoom the page on focus.
 */
function CommentBox({
  draft,
  onDraft,
  onAdd,
  onCancel,
  className,
}: {
  draft: string;
  onDraft: (draft: string) => void;
  onAdd: () => void;
  onCancel: () => void;
  className?: string;
}) {
  return (
    <div
      data-diff-comment
      className={cn("bg-card border-border flex flex-col gap-2 border-y p-2 font-sans whitespace-normal", className)}
    >
      <Textarea
        // Mounting is the tap that picked the line, so the keyboard comes up
        // with it; moving to another line remounts and focuses again.
        autoFocus
        aria-label="Comment on this line"
        placeholder="Comment on this line"
        value={draft}
        onChange={(e) => onDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            onAdd();
          } else if (e.key === "Escape") {
            // The docked panel closes on an Escape nobody else claimed.
            e.preventDefault();
            onCancel();
          }
        }}
        className="max-h-48 resize-none"
      />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="button" onClick={onAdd} disabled={!draft.trim()}>
          Add to chat
        </Button>
      </div>
    </div>
  );
}
