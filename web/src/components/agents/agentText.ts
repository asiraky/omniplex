import type { AgentTextKind, CompactionRow } from "~/protocol";
import { diffLines } from "~/lib/lineDiff";
import type { DiffLine } from "~/lib/patch";

// Plain helpers the agent page's Prompt, Compaction and Log tabs share. They
// live apart from the components so those files export only components.

/** r3 for a prompt revision, c3 for compaction instructions, so a log row can say "r7 · c3". */
export function revLabel(kind: AgentTextKind, n: number): string {
  return `${kind === "prompt" ? "r" : "c"}${n}`;
}

/** The harness as the page names it in "Not reported by …" and the per-harness notes. */
export function harnessName(harness: string): string {
  switch (harness) {
    case "claude":
      return "Claude Code";
    case "codex":
      return "Codex";
    case "pi":
      return "Pi";
    default:
      return harness;
  }
}

/** A character count the way the log shows sizes: 56, 0.7k, 14.8k, 120k. */
export function fmtChars(n: number): string {
  if (n < 100) return String(n);
  if (n < 100_000) return `${(n / 1000).toFixed(1)}k`;
  return `${Math.round(n / 1000)}k`;
}

export function fmtDay(ms: number): string {
  return new Date(ms).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

export function fmtDayTime(ms: number): string {
  const d = new Date(ms);
  return `${fmtDay(ms)}, ${d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hour12: false })}`;
}

/** Lines added and removed going from one body to the next. */
export function diffStat(before: string, after: string): { add: number; del: number } {
  let add = 0;
  let del = 0;
  for (const l of diffLines(before, after, 0)) {
    if (l.kind === "add") add++;
    else if (l.kind === "del") del++;
  }
  return { add, del };
}

function lineCount(text: string): number {
  if (text === "") return 0;
  return text.replace(/\n$/, "").split("\n").length;
}

/**
 * A prompt is prose, not code: "@@ -5,4 +5,7 @@" means nothing to someone
 * reading their brief. Each hunk header becomes the count of lines it skips,
 * and the lines after the last hunk get a row of their own, so the folded
 * parts read as "4 unchanged lines" the way the mockup has them.
 */
export function proseDiff(before: string, after: string): DiffLine[] {
  const lines = diffLines(before, after);
  const out: DiffLine[] = [];
  let lastOld = 0;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.kind === "hunk") {
      let next: DiffLine | undefined;
      for (let j = i + 1; j < lines.length && lines[j].kind !== "hunk"; j++) {
        if (lines[j].oldNo !== undefined) {
          next = lines[j];
          break;
        }
      }
      // A hunk of pure additions has no old line to measure from; its header
      // says how many old lines come before it.
      const start = next?.oldNo ?? Number(/^@@ -(\d+)/.exec(line.text)?.[1] ?? lastOld) + 1;
      const gap = start - lastOld - 1;
      if (gap > 0) out.push({ kind: "hunk", text: unchanged(gap) });
      continue;
    }
    if (line.oldNo !== undefined) lastOld = line.oldNo;
    out.push(line);
  }
  const rest = lineCount(before) - lastOld;
  if (out.length > 0 && rest > 0) out.push({ kind: "hunk", text: unchanged(rest) });
  return out;
}

function unchanged(n: number): string {
  return `${n} unchanged line${n === 1 ? "" : "s"}`;
}

/** Claude Code will not compact earlier than this, whatever it is told. */
export const CLAUDE_MIN_COMPACT_AT = 100_000;

/** The compact-at choices offered as one tap; anything else is Custom. */
export const COMPACT_PRESETS = [100_000, 120_000, 140_000] as const;

/**
 * A custom compact-at, typed in thousands of tokens ("150" is 150k). Empty is
 * not a value here: Default is its own choice. Claude's floor is enforced
 * rather than clamped, so nobody saves 80k believing that is what they got.
 */
export function parseCompactAt(text: string, harness: string): { tokens: number } | { error: string } {
  const t = text.trim().replace(/k$/i, "");
  if (!t) return { error: "Enter a number of thousands of tokens." };
  if (!/^\d+(\.\d+)?$/.test(t)) return { error: "Enter a number, like 150 for 150k tokens." };
  const tokens = Math.round(Number(t) * 1000);
  if (tokens <= 0) return { error: "Enter a number above zero." };
  if (harness === "claude" && tokens < CLAUDE_MIN_COMPACT_AT) {
    return { error: `Claude Code compacts at ${CLAUDE_MIN_COMPACT_AT / 1000}k at the earliest.` };
  }
  return { tokens };
}

/** How many compactions the log shows before "Show earlier". */
export const LOG_PAGE = 8;

/**
 * The log's rows in the order they are drawn: newest compactions first, cut
 * to a page unless everything is asked for, and epoch 0 held apart so it can
 * sit at the bottom whatever the paging.
 */
export function logRows(
  rows: CompactionRow[],
  showAll: boolean,
): { shown: CompactionRow[]; hidden: number; first?: CompactionRow } {
  const first = rows.find((r) => r.epoch === 0);
  const rest = rows.filter((r) => r.epoch !== 0).sort((a, b) => b.epoch - a.epoch);
  const shown = showAll ? rest : rest.slice(0, LOG_PAGE);
  return { shown, hidden: rest.length - shown.length, first };
}
