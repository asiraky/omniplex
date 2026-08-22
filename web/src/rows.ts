// Folding transcript items into rows. The shape a transcript should read as:
// what was asked, what came back, and one quiet line standing in for the work
// between them — expandable for anyone who wants the detail. This module is
// pure so the folding rules can be tested without rendering anything.
//
// Two regimes, split by whether the item's turn is over:
//
// A finished turn folds completely. Everything between the prompt and the
// final answer — commentary, tool calls, even the failed ones — collapses
// behind one "Worked for 34s" row. A failure inside a finished turn is
// history, not an alarm: it is reachable behind the fold, not shouting from
// the transcript.
//
// A running turn stays visible, but never as a stack. Its text renders in
// full as it streams, and each unbroken run of tool calls is one row: the
// trailing run is live — a single line naming the call happening right now,
// updating in place — and a run the narration has moved past becomes a
// one-line summary. Nothing the reader is looking at is ever reclassified
// out from under them: rows accrete and morph in place, they do not vanish.

import type { Item, Turn } from "./protocol";

// A harness can open a message block it never fills — a thought whose text the
// model kept to itself. There is nothing to render and nothing to hide, so it
// is dropped rather than left to print a blank line.
function isEmptyMessage(item: Item) {
  // A prompt that is nothing but pictures has no text and is not empty: the
  // images are the message.
  return item.kind === "message" && (item.text ?? "").trim() === "" && !item.images?.length;
}

function isAgentText(item: Item) {
  return item.kind === "message" && item.role === "agent" && !isEmptyMessage(item);
}

export type Row =
  // A message, or a prompt: rendered as itself.
  | { kind: "item"; item: Item }
  // An unbroken run of tool calls in a turn still running. `live` marks the
  // trailing run — the one whose last call is the work happening now.
  | { kind: "run"; id: string; items: Item[]; live: boolean }
  // A finished turn's hidden middle: everything but its prompt and answer.
  | { kind: "fold"; id: string; turn: Turn; items: Item[] };

export function buildRows(items: Item[], turns: Turn[], phase: string): Row[] {
  const turnById = new Map(turns.map((t) => [t.id, t]));
  // A turn this projection has never heard of cannot be shown to be over, so
  // it is treated as still running rather than folded away mid-flight.
  const doneTurn = (turnId?: string) =>
    turnId !== undefined ? (turnById.get(turnId)?.done ?? false) : false;

  const visible = items.filter((it) => !isEmptyMessage(it));

  const rows: Row[] = [];
  let i = 0;
  while (i < visible.length) {
    const turnId = visible[i].turnId;

    if (turnId !== undefined && doneTurn(turnId)) {
      // The whole contiguous stretch of this finished turn, folded at once.
      let j = i;
      while (j < visible.length && visible[j].turnId === turnId) j++;
      const segment = visible.slice(i, j);

      // The answer is the turn's last piece of agent text. Tool calls that
      // trail it — work a steer cut short — fold with everything else.
      let answer: Item | undefined;
      for (let k = segment.length - 1; k >= 0; k--) {
        if (isAgentText(segment[k])) {
          answer = segment[k];
          break;
        }
      }

      const hidden: Item[] = [];
      for (const it of segment) {
        if (it === answer) continue;
        // The prompt stays where the reader can see what was asked.
        if (it.kind === "message" && it.role === "user") {
          rows.push({ kind: "item", item: it });
          continue;
        }
        hidden.push(it);
      }
      if (hidden.length > 0) {
        // Anchored to its first hidden item, not the turn id: a turn whose
        // items are somehow split by another's folds twice, and two rows must
        // not share a key.
        rows.push({ kind: "fold", id: `fold:${hidden[0].id}`, turn: turnById.get(turnId)!, items: hidden });
      }
      if (answer) rows.push({ kind: "item", item: answer });
      i = j;
      continue;
    }

    // A turn still running, or items outside any known turn.
    const item = visible[i];
    if (item.kind === "tool") {
      let j = i;
      while (j < visible.length && visible[j].kind === "tool" && !doneTurn(visible[j].turnId)) j++;
      const run = visible.slice(i, j);
      // The run is live when nothing follows it and a turn is running: its
      // last call is the work happening now. The row's id is its first call,
      // so the row keeps its identity while the run grows.
      const live = j === visible.length && phase === "turn";
      rows.push({ kind: "run", id: `run:${run[0].id}`, items: run, live });
      i = j;
      continue;
    }
    rows.push({ kind: "item", item });
    i++;
  }
  return rows;
}

// A row belongs to the turn its last item came from: that is the turn whose
// changed-files card, if it has one, comes next.
export function rowTurnID(row: Row): string | undefined {
  if (row.kind === "item") return row.item.turnId;
  if (row.kind === "fold") return row.turn.id;
  return row.items[row.items.length - 1]?.turnId;
}

// One phrase per tool kind, in the order they read best in a summary. These
// count calls, not files: one call can touch several paths and five calls can
// touch one, so "Edited 3 files" would be a claim this cannot make. The Changes
// panel is where the honest per-file count lives.
const SUMMARY: { kind: string; one: string; many: (n: number) => string }[] = [
  { kind: "read", one: "1 read", many: (n) => `${n} reads` },
  { kind: "edit", one: "1 edit", many: (n) => `${n} edits` },
  { kind: "delete", one: "1 delete", many: (n) => `${n} deletes` },
  { kind: "move", one: "1 move", many: (n) => `${n} moves` },
  { kind: "search", one: "1 search", many: (n) => `${n} searches` },
  { kind: "execute", one: "1 command", many: (n) => `${n} commands` },
  { kind: "fetch", one: "1 fetch", many: (n) => `${n} fetches` },
  { kind: "think", one: "1 thought", many: (n) => `${n} thoughts` },
  { kind: "other", one: "1 other call", many: (n) => `${n} other calls` },
];

export function summarise(items: Item[]): string {
  const counts = new Map<string, number>();
  for (const it of items) {
    if (it.kind !== "tool") continue;
    const kind = it.toolKind && SUMMARY.some((s) => s.kind === it.toolKind) ? it.toolKind : "other";
    counts.set(kind, (counts.get(kind) ?? 0) + 1);
  }
  return SUMMARY.filter((s) => counts.has(s.kind))
    .map((s) => {
      const n = counts.get(s.kind)!;
      return n === 1 ? s.one : s.many(n);
    })
    .join(" · ");
}

// "Worked for 34s", measured by the event log's own clock. A turn without
// timestamps — recorded before they existed — is still a fold, just unmeasured.
export function foldLabel(turn: Turn): string {
  const verb =
    turn.stopReason === "cancelled" ? "Stopped" : turn.stopReason === "error" ? "Failed" : "Worked";
  if (turn.startedAt === undefined || turn.finishedAt === undefined || turn.finishedAt < turn.startedAt)
    return verb;
  return `${verb} ${verb === "Worked" ? "for" : "after"} ${formatDuration(turn.finishedAt - turn.startedAt)}`;
}

export function formatDuration(ms: number): string {
  const s = Math.max(1, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}
