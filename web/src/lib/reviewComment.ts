// A comment on one line of a diff, as a block for the composer. The composer
// draft is the pending review: blocks pile up there and go to the agent as one
// prompt when it is sent.

import { fence } from "./fence";
import type { DiffLine } from "./patch";

const SIGN: Partial<Record<DiffLine["kind"], string>> = { add: "+", del: "-", context: " " };

/**
 * The line cited as `path:n`, the line itself quoted in a diff fence, then the comment. The text
 * goes along so the agent can find the line even after the numbering has moved.
 *
 * `side` is the side the line was picked from. A new-side number is a line of the file as it is
 * now; an old-side one is not, so it says which it is: a removed line, or a context line picked
 * from the left of a split view. A line past a gap in the patch has no number, and is cited by
 * path alone.
 */
export function reviewComment(path: string, line: DiffLine, side: "old" | "new", comment: string): string {
  const no = side === "old" ? line.oldNo : line.newNo;
  const note = line.kind === "del" ? " (removed line)" : side === "old" ? " (old line)" : "";
  const cite = `\`${no === undefined ? path : `${path}:${no}`}\`${note}`;
  return `${cite}\n${fence(`${SIGN[line.kind] ?? " "}${line.text}`, "diff")}\n${comment.trim()}`;
}
