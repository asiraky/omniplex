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
export function isEmptyMessage(item: Item) {
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
  // `continued` marks a piece after the first, when a plan or a spawn batch
  // split the middle: the turn's duration belongs to the first piece alone.
  | { kind: "fold"; id: string; turn: Turn; items: Item[]; continued?: boolean }
  // A batch of subagent spawns, pinned where the first one happened. Never
  // folded: the work is still running beside the conversation after the turn
  // that started it is over, so the card has to stay where it can be seen.
  | { kind: "jobs"; id: string; items: Item[] }
  // Artefacts the agent showed. Never folded: they are what the turn was
  // for. A finished turn shows them under its answer, one card per artefact
  // however many times it was shown.
  | { kind: "artefacts"; id: string; items: Item[] };

/** A row's identity: its React key, and the id the scroll memory finds it by. */
export const rowKey = (row: Row) => (row.kind === "item" ? row.item.id : row.id);

function latestPerArtefact(items: Item[]): Item[] {
  const last = new Map<string, Item>();
  for (const it of items) last.set(it.artefactId ?? it.id, it);
  return [...last.values()];
}

export function isSpawn(item: Item) {
  return item.kind === "tool" && item.toolKind === "agent";
}

// Consecutive spawns starting at `i` become one card, anchored to the first.
function spawnBatch(items: Item[], i: number): Item[] {
  let j = i;
  while (j < items.length && isSpawn(items[j])) j++;
  return items.slice(i, j);
}

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

      // Anchored to its first hidden item, not the turn id: a turn whose
      // items are somehow split by another's folds twice, and two rows must
      // not share a key.
      let hidden: Item[] = [];
      let folds = 0;
      const produced: Item[] = [];
      const decided: Item[] = [];
      const flush = () => {
        if (hidden.length > 0)
          rows.push({
            kind: "fold",
            id: `fold:${hidden[0].id}`,
            turn: turnById.get(turnId)!,
            items: hidden,
            continued: folds++ > 0 || undefined,
          });
        hidden = [];
      };
      for (let k = 0; k < segment.length; k++) {
        const it = segment[k];
        if (it === answer) continue;
        if (it.kind === "artefact") {
          produced.push(it);
          continue;
        }
        // A change the agent proposed was the user's decision: what they
        // decided stays in view, under the answer.
        if (it.kind === "card") {
          decided.push(it);
          continue;
        }
        // The prompt stays where the reader can see what was asked.
        if (it.kind === "message" && it.role === "user") {
          rows.push({ kind: "item", item: it });
          continue;
        }
        // The agent's task list stays where it wrote it, splitting the fold:
        // it is the turn's outline, not part of the work it outlines.
        if (it.kind === "plan") {
          flush();
          rows.push({ kind: "item", item: it });
          continue;
        }
        // A spawn batch stays out of the fold, splitting it in two if it must.
        if (isSpawn(it)) {
          flush();
          const batch = spawnBatch(segment, k);
          rows.push({ kind: "jobs", id: `jobs:${batch[0].id}`, items: batch });
          k += batch.length - 1;
          continue;
        }
        hidden.push(it);
      }
      flush();
      if (answer) rows.push({ kind: "item", item: answer });
      for (const it of decided) rows.push({ kind: "item", item: it });
      if (produced.length > 0)
        rows.push({ kind: "artefacts", id: `artefacts:${produced[0].id}`, items: latestPerArtefact(produced) });
      i = j;
      continue;
    }

    // A turn still running, or items outside any known turn.
    const item = visible[i];
    if (isSpawn(item)) {
      const batch = spawnBatch(visible, i);
      rows.push({ kind: "jobs", id: `jobs:${batch[0].id}`, items: batch });
      i += batch.length;
      continue;
    }
    if (item.kind === "artefact") {
      let j = i;
      while (j < visible.length && visible[j].kind === "artefact" && !doneTurn(visible[j].turnId)) j++;
      rows.push({ kind: "artefacts", id: `artefacts:${item.id}`, items: visible.slice(i, j) });
      i = j;
      continue;
    }
    if (item.kind === "tool") {
      let j = i;
      while (
        j < visible.length &&
        visible[j].kind === "tool" &&
        !isSpawn(visible[j]) &&
        !doneTurn(visible[j].turnId)
      )
        j++;
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

function sameItems(a: Item[], b: Item[]) {
  return a.length === b.length && a.every((it, i) => it === b[i]);
}

// Whether two rows under one key would draw the same thing. Items are compared
// by reference: apply.ts copies only the item an event touched, so an item
// that is still the same object is one nothing has happened to.
function sameRow(a: Row, b: Row): boolean {
  switch (a.kind) {
    case "item":
      return b.kind === "item" && a.item === b.item;
    case "run":
      return b.kind === "run" && a.live === b.live && sameItems(a.items, b.items);
    case "fold":
      return (
        b.kind === "fold" &&
        a.turn === b.turn &&
        a.continued === b.continued &&
        sameItems(a.items, b.items)
      );
    case "jobs":
    case "artefacts":
      return b.kind === a.kind && sameItems(a.items, b.items);
  }
}

// buildRows makes every row afresh, so on its own every frame off the wire
// hands the transcript a whole new set of rows and every one of them redraws —
// a streamed token repainting the whole conversation above it. This keeps the
// previous row object wherever the new one would draw the same thing, so only
// the rows that actually changed are new; and when none did, the previous
// array itself comes back.
export function reuseRows(prev: Row[], next: Row[]): Row[] {
  if (prev.length === 0) return next;
  const byKey = new Map(prev.map((r) => [rowKey(r), r]));
  let unchanged = prev.length === next.length;
  const rows = next.map((row, i) => {
    const old = byKey.get(rowKey(row));
    if (old && sameRow(old, row)) {
      if (old !== prev[i]) unchanged = false;
      return old;
    }
    unchanged = false;
    return row;
  });
  return unchanged ? prev : rows;
}

// A row belongs to the turn its last item came from: that is the turn whose
// changed-files card, if it has one, comes next.
export function rowTurnID(row: Row): string | undefined {
  if (row.kind === "item") return row.item.turnId;
  if (row.kind === "fold") return row.turn.id;
  return row.items[row.items.length - 1]?.turnId;
}

// A settled run reads as a sentence: "Ran 2 commands and made 3 edits". Kinds
// group under the verb that fits them, clauses and kinds in the order below.
// These count calls, not files: one call can touch several paths and five calls
// can touch one, so "Edited 3 files" would be a claim this cannot make — hence
// "made 3 edits". The Changes panel is where the honest per-file count lives.
const count = (one: string, many: string) => (n: number) => (n === 1 ? `1 ${one}` : `${n} ${many}`);
const times = (n: number) => (n === 1 ? "once" : n === 2 ? "twice" : `${n} times`);

const SUMMARY: { verb: string; kinds: { kind: string; phrase: (n: number) => string }[] }[] = [
  {
    verb: "ran",
    kinds: [
      { kind: "execute", phrase: count("command", "commands") },
      { kind: "search", phrase: count("search", "searches") },
    ],
  },
  {
    verb: "made",
    kinds: [
      { kind: "read", phrase: count("read", "reads") },
      { kind: "edit", phrase: count("edit", "edits") },
      { kind: "delete", phrase: count("delete", "deletes") },
      { kind: "move", phrase: count("move", "moves") },
      { kind: "fetch", phrase: count("fetch", "fetches") },
      { kind: "other", phrase: count("other call", "other calls") },
    ],
  },
  { verb: "thought", kinds: [{ kind: "think", phrase: times }] },
  { verb: "started", kinds: [{ kind: "agent", phrase: count("agent", "agents") }] },
];
const KNOWN = new Set(SUMMARY.flatMap((c) => c.kinds.map((k) => k.kind)));

// "a", "a and b", "a, b and c".
function list(parts: string[]): string {
  return parts.length < 2 ? (parts[0] ?? "") : `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

export function summarise(items: Item[]): string {
  const counts = new Map<string, number>();
  for (const it of items) {
    if (it.kind !== "tool") continue;
    const kind = it.toolKind && KNOWN.has(it.toolKind) ? it.toolKind : "other";
    counts.set(kind, (counts.get(kind) ?? 0) + 1);
  }
  const clauses = SUMMARY.flatMap(({ verb, kinds }) => {
    const parts = kinds.filter((k) => counts.has(k.kind)).map((k) => k.phrase(counts.get(k.kind)!));
    if (parts.length === 0) return [];
    return [`${verb} ${list(parts)}`];
  });
  const sentence = list(clauses);
  return sentence.charAt(0).toUpperCase() + sentence.slice(1);
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
