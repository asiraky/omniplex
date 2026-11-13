import type { DiffLine } from "~/lib/patch";

/**
 * One row of a side-by-side diff: a hunk header or note across both columns,
 * or a pair with the old line on the left and the new on the right. Either
 * side of a pair can be missing, when one version has a line the other lacks.
 */
export type SplitRow = { kind: "hunk" | "meta"; text: string } | { kind: "pair"; left?: DiffLine; right?: DiffLine };

// Git's note that the line above it ends the file without a newline. The only
// note that belongs to a line rather than marking a gap.
const NO_NEWLINE = "No newline at end of file";

/**
 * Lay unified diff lines out side by side. A context line sits on both
 * sides. A run of deletions followed by a run of additions is a change, so
 * they pair off in order — the first removed line beside the first added
 * one — and whichever run is longer runs on with nothing opposite.
 *
 * A "no newline" note inside a change waits until the change is laid out:
 * editing a file's last line puts one after the deletion and another after
 * the addition, and emitting the first where it stood would cut the change
 * in two, with the old line and the new one on different rows.
 */
export function splitRows(lines: DiffLine[]): SplitRow[] {
  const out: SplitRow[] = [];
  let dels: DiffLine[] = [];
  let adds: DiffLine[] = [];
  let notes: string[] = [];

  const flush = () => {
    const n = Math.max(dels.length, adds.length);
    for (let i = 0; i < n; i++) out.push({ kind: "pair", left: dels[i], right: adds[i] });
    for (const text of notes) out.push({ kind: "meta", text });
    dels = [];
    adds = [];
    notes = [];
  };

  for (const line of lines) {
    switch (line.kind) {
      case "del":
        // A deletion after additions starts a new change.
        if (adds.length > 0) flush();
        dels.push(line);
        break;
      case "add":
        adds.push(line);
        break;
      case "meta":
        if (line.text === NO_NEWLINE && dels.length + adds.length > 0) {
          notes.push(line.text);
          break;
        }
        flush();
        out.push({ kind: "meta", text: line.text });
        break;
      case "hunk":
        flush();
        out.push({ kind: "hunk", text: line.text });
        break;
      case "context":
        flush();
        out.push({ kind: "pair", left: line, right: line });
        break;
    }
  }
  flush();
  return out;
}
