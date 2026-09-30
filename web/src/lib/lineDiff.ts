import type { DiffLine } from "~/components/Diff";

/** Past this many cells the table is not worth a phone's memory; see `changedLines`. */
const MAX_CELLS = 2_000_000;

function splitLines(text: string): string[] {
  if (text === "") return [];
  const lines = text.split("\n");
  // A trailing newline ends the last line; it is not one more, empty line.
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

type Op = { kind: "context" | "del" | "add"; text: string };

/**
 * The edit script between two runs of lines, by longest common subsequence.
 * Two runs too long to table are reported as one replaced by the other: still
 * true, just not minimal.
 */
function changedLines(a: string[], b: string[]): Op[] {
  const n = a.length;
  const m = b.length;
  const dels = a.map((text): Op => ({ kind: "del", text }));
  const adds = b.map((text): Op => ({ kind: "add", text }));
  if (n === 0 || m === 0 || n * m > MAX_CELLS) return [...dels, ...adds];

  // lcs[i][j] is the longest common subsequence of a[i..] and b[j..].
  const width = m + 1;
  const lcs = new Uint32Array((n + 1) * width);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i * width + j] =
        a[i] === b[j] ? lcs[(i + 1) * width + j + 1] + 1 : Math.max(lcs[(i + 1) * width + j], lcs[i * width + j + 1]);
    }
  }
  const out: Op[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ kind: "context", text: a[i] });
      i++;
      j++;
    } else if (lcs[(i + 1) * width + j] >= lcs[i * width + j + 1]) {
      out.push(dels[i++]);
    } else {
      out.push(adds[j++]);
    }
  }
  return [...out, ...dels.slice(i), ...adds.slice(j)];
}

/**
 * Two versions of a file as the lines `Diff` draws: hunks of changes with a
 * few lines around each. Empty when the two read the same. An update hands
 * over both versions whole rather than a patch, so the diff is made here.
 */
export function diffLines(before: string, after: string, context = 3): DiffLine[] {
  const a = splitLines(before);
  const b = splitLines(after);

  // Most of an updated file is untouched; only what sits between the common
  // start and the common end needs comparing.
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;

  const ops: Op[] = [
    ...a.slice(0, head).map((text): Op => ({ kind: "context", text })),
    ...changedLines(a.slice(head, a.length - tail), b.slice(head, b.length - tail)),
    ...a.slice(a.length - tail).map((text): Op => ({ kind: "context", text })),
  ];

  // Number every line, then keep the changes and what is within reach of one.
  const numbered: DiffLine[] = [];
  let oldNo = 1;
  let newNo = 1;
  for (const op of ops) {
    if (op.kind === "context") numbered.push({ kind: "context", text: op.text, oldNo: oldNo++, newNo: newNo++ });
    else if (op.kind === "del") numbered.push({ kind: "del", text: op.text, oldNo: oldNo++ });
    else numbered.push({ kind: "add", text: op.text, newNo: newNo++ });
  }
  const keep = new Array<boolean>(numbered.length).fill(false);
  numbered.forEach((line, at) => {
    if (line.kind === "context") return;
    for (let k = Math.max(0, at - context); k <= Math.min(numbered.length - 1, at + context); k++) keep[k] = true;
  });

  const out: DiffLine[] = [];
  for (let at = 0; at < numbered.length; at++) {
    if (!keep[at]) continue;
    if (at === 0 || !keep[at - 1]) {
      let end = at;
      while (end < numbered.length && keep[end]) end++;
      const hunk = numbered.slice(at, end);
      const olds = hunk.filter((l) => l.kind !== "add");
      const news = hunk.filter((l) => l.kind !== "del");
      // A side with no lines starts at the line before the hunk, as in git.
      const oldStart = olds[0]?.oldNo ?? countBefore(numbered, at, "add");
      const newStart = news[0]?.newNo ?? countBefore(numbered, at, "del");
      out.push({ kind: "hunk", text: `@@ -${oldStart},${olds.length} +${newStart},${news.length} @@` });
    }
    out.push(numbered[at]);
  }
  return out;
}

/** How many lines of one side come before index `at`, leaving out the other side's own. */
function countBefore(lines: DiffLine[], at: number, skip: "add" | "del"): number {
  let n = 0;
  for (let k = 0; k < at; k++) if (lines[k].kind !== skip) n++;
  return n;
}
