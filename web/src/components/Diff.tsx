import { useMemo } from "react";

import { cn } from "~/lib/utils";

export type DiffLineKind = "add" | "del" | "context" | "hunk" | "meta";

export interface DiffLine {
  kind: DiffLineKind;
  text: string;
  oldNo?: number;
  newNo?: number;
}

// Header lines carry nothing a reader wants: the path is already the row they
// clicked, and the blob hashes are noise.
const HEADER = /^(diff --git |index |--- |\+\+\+ |old mode |new mode |new file mode |deleted file mode |similarity index |rename from |rename to |copy from |copy to |Binary files )/;

/**
 * Turn a unified diff into lines that know their own numbering. Git is the one
 * producing this text, so the parser only has to be right about git's output,
 * not about every patch a human might paste.
 */
export function parsePatch(patch: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldNo = 0;
  let newNo = 0;

  for (const raw of patch.split("\n")) {
    if (raw.startsWith("@@")) {
      const m = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(raw);
      if (m) {
        oldNo = Number(m[1]);
        newNo = Number(m[2]);
      }
      out.push({ kind: "hunk", text: raw });
      continue;
    }
    if (raw.startsWith("\\")) {
      out.push({ kind: "meta", text: raw.replace(/^\\ /, "") });
      continue;
    }
    if (HEADER.test(raw)) {
      // A binary file has no hunks at all, so its one header line is the only
      // thing there is to say about it.
      if (raw.startsWith("Binary files")) out.push({ kind: "meta", text: raw });
      continue;
    }
    if (raw === "" && out.length === 0) continue;
    if (raw.startsWith("+")) {
      out.push({ kind: "add", text: raw.slice(1), newNo: newNo++ });
      continue;
    }
    if (raw.startsWith("-")) {
      out.push({ kind: "del", text: raw.slice(1), oldNo: oldNo++ });
      continue;
    }
    // The last line of a patch is an empty string from the trailing newline,
    // and is not a line of the file.
    if (raw === "") continue;
    out.push({ kind: "context", text: raw.slice(1), oldNo: oldNo++, newNo: newNo++ });
  }
  return out;
}

function gutter(n?: number) {
  return n === undefined ? "" : String(n);
}

/**
 * A unified diff, with the gutters and colouring the eye expects. Split view
 * would need horizontal room this panel does not have on a phone.
 *
 * Unwrapped, each row is `w-max min-w-full`: the row is as wide as its longest
 * line, so a long line makes the scroll container actually scroll instead of
 * spilling out of a container-width box, and the add/delete tint still covers
 * the row once it is scrolled. Wrapped, rows are held to the container and the
 * text breaks — including mid-token, which is the only thing that helps with
 * the minified line that made someone tick the box.
 */
export function Diff({
  patch,
  wrap = false,
  className,
}: {
  patch: string;
  wrap?: boolean;
  className?: string;
}) {
  const lines = useMemo(() => parsePatch(patch), [patch]);

  if (lines.length === 0) {
    return <p className="text-muted-foreground px-3 py-2 text-[12px]">No textual changes.</p>;
  }

  return (
    <div className={cn("font-mono text-[11px] leading-[1.55]", className)}>
      {lines.map((line, i) => (
        <div
          key={i}
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
            <span className="px-2 py-0.5">{line.text}</span>
          ) : (
            <>
              <span className="text-muted-foreground/70 w-10 shrink-0 select-none px-1 text-right tabular-nums">
                {gutter(line.oldNo)}
              </span>
              <span className="text-muted-foreground/70 w-10 shrink-0 select-none px-1 text-right tabular-nums">
                {gutter(line.newNo)}
              </span>
              <span
                className={cn(
                  "w-4 shrink-0 select-none text-center",
                  line.kind === "add" && "text-success",
                  line.kind === "del" && "text-destructive",
                )}
              >
                {line.kind === "add" ? "+" : line.kind === "del" ? "-" : " "}
              </span>
              <span className={cn("pr-3", wrap ? "min-w-0 flex-1" : "shrink-0")}>{line.text || " "}</span>
            </>
          )}
        </div>
      ))}
    </div>
  );
}
