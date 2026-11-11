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
 * Turn a unified diff into lines that know their own numbering. Hunks-only
 * patches (no `---`/`+++` file header, as Codex sends) parse the same way.
 *
 * Inside a hunk, the counts in its `@@` header say how many lines are still
 * to come, and every one of them is content: a removed SQL comment `-- x` is
 * the line `--- x`, and must not be mistaken for a file header. Only once the
 * counts run out is a line read as a header again. A producer that miscounts
 * still renders: past the counts, `+` and `-` lines are taken as they come.
 */
export function parsePatch(patch: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldNo = 0;
  let newNo = 0;
  let oldLeft = 0;
  let newLeft = 0;

  for (const raw of patch.split("\n")) {
    const inHunk = oldLeft > 0 || newLeft > 0;
    if (raw.startsWith("@@")) {
      const m = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(raw);
      if (m) {
        oldNo = Number(m[1]);
        newNo = Number(m[3]);
        oldLeft = m[2] === undefined ? 1 : Number(m[2]);
        newLeft = m[4] === undefined ? 1 : Number(m[4]);
      }
      out.push({ kind: "hunk", text: raw });
      continue;
    }
    if (raw.startsWith("\\")) {
      out.push({ kind: "meta", text: raw.replace(/^\\ /, "") });
      continue;
    }
    if (!inHunk && HEADER.test(raw)) {
      // A binary file has no hunks at all, so its one header line is the only
      // thing there is to say about it.
      if (raw.startsWith("Binary files")) out.push({ kind: "meta", text: raw });
      continue;
    }
    if (raw === "" && out.length === 0) continue;
    if (raw.startsWith("+")) {
      out.push({ kind: "add", text: raw.slice(1), newNo: newNo++ });
      newLeft = Math.max(0, newLeft - 1);
      continue;
    }
    if (raw.startsWith("-")) {
      out.push({ kind: "del", text: raw.slice(1), oldNo: oldNo++ });
      oldLeft = Math.max(0, oldLeft - 1);
      continue;
    }
    // The last line of a patch is an empty string from the trailing newline,
    // and is not a line of the file.
    if (raw === "" && !inHunk) continue;
    out.push({ kind: "context", text: raw.slice(1), oldNo: oldNo++, newNo: newNo++ });
    oldLeft = Math.max(0, oldLeft - 1);
    newLeft = Math.max(0, newLeft - 1);
  }
  return out;
}
