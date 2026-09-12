import { useMemo, useRef, useEffect } from "react";

import { cn } from "~/lib/utils";

/**
 * Numbered, wrapped plain text.
 *
 * Shared by the panel's file viewer and the composer's quickview so a chunk of
 * a file looks the same wherever it is read, and so the line numbers on a
 * quoted range are the file's own rather than restarting at one.
 */
export function CodeLines({
  content,
  /** The line number the first row carries. A quoted range starts where it
      was cut from, not at 1. */
  startLine = 1,
  /** Highlighted and scrolled to, in the same numbering as `startLine`. */
  highlight,
  className,
}: {
  content: string;
  startLine?: number;
  highlight?: number;
  className?: string;
}) {
  const rowRefs = useRef(new Map<number, HTMLTableRowElement>());

  const lines = useMemo(() => {
    const split = content.split("\n");
    // A trailing newline yields one phantom empty line nobody wrote.
    if (split[split.length - 1] === "") split.pop();
    return split;
  }, [content]);

  useEffect(() => {
    if (highlight === undefined) return;
    rowRefs.current.get(highlight)?.scrollIntoView({ block: "center" });
  }, [content, highlight]);

  return (
    <table className={cn("w-full border-collapse font-mono text-[11.5px] leading-relaxed", className)}>
      <tbody>
        {lines.map((text, i) => {
          const line = startLine + i;
          return (
            <tr
              key={i}
              // Read back by the copy-provenance watcher to work out which
              // lines a selection covered.
              data-line={line}
              ref={(el) => {
                if (el) rowRefs.current.set(line, el);
                else rowRefs.current.delete(line);
              }}
              className={cn(line === highlight && "bg-attention/40")}
            >
              <td className="text-muted-foreground/50 w-[1%] min-w-10 pr-3 pl-2 text-right align-top select-none">
                {line}
              </td>
              <td className="pr-3 break-words whitespace-pre-wrap">{text}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

/**
 * The line range a selection covers inside a `CodeLines` table, if it is
 * inside one at all. Used to label a copied chunk with where it came from.
 */
export function selectedLineRange(selection: Selection | null): { from: number; to: number } | null {
  if (!selection || selection.rangeCount === 0) return null;
  const from = lineOf(selection.anchorNode);
  const to = lineOf(selection.focusNode);
  if (from === null || to === null) return null;
  return { from: Math.min(from, to), to: Math.max(from, to) };
}

function lineOf(node: Node | null): number | null {
  const el = node instanceof Element ? node : node?.parentElement;
  const row = el?.closest?.("tr[data-line]");
  const value = row?.getAttribute("data-line");
  return value ? Number(value) : null;
}
