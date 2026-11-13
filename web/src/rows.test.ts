import { describe, expect, it } from "vitest";

import type { Item, Turn } from "./protocol";
import { makeArtefact } from "./test/artefact";
import { buildRows, foldLabel, formatDuration, reuseRows, rowTurnID, summarise, type Row } from "./rows";

let n = 0;
function tool(over: Partial<Item> = {}): Item {
  return { id: `t${n++}`, kind: "tool", toolKind: "execute", status: "completed", turnId: "turn1", ...over };
}
function msg(text: string, over: Partial<Item> = {}): Item {
  return { id: `m${n++}`, kind: "message", role: "agent", contentKind: "text", text, turnId: "turn1", ...over };
}
function prompt(text: string, over: Partial<Item> = {}): Item {
  return msg(text, { role: "user", ...over });
}
function turn(id: string, over: Partial<Turn> = {}): Turn {
  return { id, prompt: "p", done: true, ...over };
}

const shape = (rows: Row[]) =>
  rows.map((r) => {
    if (r.kind === "fold") return `fold(${r.items.length})`;
    if (r.kind === "jobs") return r.recap ? `recap(${r.items.length})` : `jobs(${r.items.length})`;
    if (r.kind === "artefacts") return `artefacts(${r.items.map((i) => i.id.replace("artefact:", "")).join(",")})`;
    if (r.kind === "run") return r.live ? `live(${r.items.length})` : `run(${r.items.length})`;
    if (r.item.kind === "plan") return "plan";
    return `${r.item.kind}:${r.item.role ?? "tool"}`;
  });

// One showing of an artefact. The agent can show the same one several times
// in a turn; seq tells the showings apart.
function artefact(id: string, seq: number, over: Partial<Item> = {}): Item {
  return { id: `artefact:${id}@${seq}`, kind: "artefact", artefactId: id, turnId: "turn1", ...over };
}

describe("buildRows with shown artefacts", () => {
  it("keeps a finished turn's artefacts out of the fold, under the answer, one card each at its last showing", () => {
    const rows = buildRows(
      [prompt("make it"), tool(), artefact("a", 1), tool(), artefact("b", 1), artefact("a", 2), msg("done")],
      [turn("turn1")],
      "idle",
    );
    expect(shape(rows)).toEqual(["message:user", "fold(2)", "message:agent", "artefacts(a@2,b@1)"]);
  });

  it("shows artefacts in place while the turn is still running", () => {
    const rows = buildRows([prompt("make it"), artefact("a", 1), artefact("b", 1)], [turn("turn1", { done: false })], "turn");
    expect(shape(rows)).toContain("artefacts(a@1,b@1)");
  });
});

describe("buildRows on a finished turn", () => {
  it("folds everything between the prompt and the answer", () => {
    const rows = buildRows(
      [prompt("do it"), tool(), msg("Checking."), tool(), msg("Done — all good.")],
      [turn("turn1")],
      "idle",
    );
    expect(shape(rows)).toEqual(["message:user", "fold(3)", "message:agent"]);
  });

  it("folds failed calls too, instead of leaving a stack of cards", () => {
    const rows = buildRows(
      [prompt("go"), tool({ status: "failed" }), tool(), tool({ status: "failed" }), msg("Answer.")],
      [turn("turn1")],
      "idle",
    );
    expect(shape(rows)).toEqual(["message:user", "fold(3)", "message:agent"]);
  });

  it("emits no fold for a turn that was just a question and an answer", () => {
    const rows = buildRows([prompt("hi"), msg("Hello.")], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "message:agent"]);
  });

  it("folds tool calls that trail the answer", () => {
    const rows = buildRows([prompt("go"), msg("Answer."), tool(), tool()], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "fold(2)", "message:agent"]);
  });

  it("folds a turn with no answer entirely", () => {
    const rows = buildRows([prompt("go"), tool(), tool()], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "fold(2)"]);
  });

  it("drops empty message blocks", () => {
    const rows = buildRows([prompt("go"), msg(""), msg("Answer.")], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "message:agent"]);
  });

  // "what is this?" is often the picture alone. Such a prompt has no text, and
  // dropping it as empty would take the images with it.
  it("keeps a prompt that is nothing but images", () => {
    const rows = buildRows(
      [prompt("", { images: [{ id: "i1", mediaType: "image/png" }] }), msg("A walrus.")],
      [turn("turn1")],
      "idle",
    );
    expect(shape(rows)).toEqual(["message:user", "message:agent"]);
  });
});

describe("buildRows on a running turn", () => {
  const turns = [turn("done1"), turn("live1", { done: false })];

  it("keeps text visible and renders the trailing run live", () => {
    const rows = buildRows(
      [prompt("go", { turnId: "live1" }), msg("Looking around.", { turnId: "live1" }), tool({ turnId: "live1" }), tool({ turnId: "live1" })],
      turns,
      "turn",
    );
    expect(shape(rows)).toEqual(["message:user", "message:agent", "live(2)"]);
  });

  it("settles a run into a summary row once narration moves past it", () => {
    const rows = buildRows(
      [tool({ turnId: "live1" }), tool({ turnId: "live1" }), msg("Found it.", { turnId: "live1" }), tool({ turnId: "live1" })],
      turns,
      "turn",
    );
    expect(shape(rows)).toEqual(["run(2)", "message:agent", "live(1)"]);
  });

  it("is not live once the turn is over, even at the tail", () => {
    const rows = buildRows([tool({ turnId: "x" }), tool({ turnId: "x" })], [turn("x", { done: false })], "idle");
    expect(shape(rows)).toEqual(["run(2)"]);
  });

  it("still folds finished turns while a later turn runs", () => {
    const rows = buildRows(
      [prompt("first", { turnId: "done1" }), tool({ turnId: "done1" }), msg("First answer.", { turnId: "done1" }), prompt("second", { turnId: "live1" }), tool({ turnId: "live1" })],
      turns,
      "turn",
    );
    expect(shape(rows)).toEqual(["message:user", "fold(1)", "message:agent", "message:user", "live(1)"]);
  });

  it("treats items from an unknown turn as live rather than folding them", () => {
    const rows = buildRows([tool({ turnId: "ghost" }), msg("hm", { turnId: "ghost" })], [], "turn");
    expect(shape(rows)).toEqual(["run(1)", "message:agent"]);
  });
});

describe("row identity is stable while a run grows", () => {
  it("keeps the same run id as calls are appended", () => {
    const a = tool({ turnId: "live1" });
    const turns = [turn("live1", { done: false })];
    const before = buildRows([a], turns, "turn");
    const after = buildRows([a, tool({ turnId: "live1" }), tool({ turnId: "live1" })], turns, "turn");
    expect(before[0].kind).toBe("run");
    expect(after[0].kind).toBe("run");
    expect((before[0] as Extract<Row, { kind: "run" }>).id).toBe(
      (after[0] as Extract<Row, { kind: "run" }>).id,
    );
  });
});

describe("spawn batches", () => {
  const spawn = (over: Partial<Item> = {}) => tool({ toolKind: "agent", ...over });

  it("groups consecutive spawns into one card, outside the fold", () => {
    const rows = buildRows(
      [prompt("go"), tool(), spawn(), spawn(), tool(), msg("Answer.")],
      [turn("turn1")],
      "idle",
    );
    expect(shape(rows)).toEqual(["message:user", "fold(1)", "jobs(2)", "fold(1)", "message:agent", "recap(2)"]);
  });

  it("keeps the card at the first spawn while a turn runs", () => {
    const rows = buildRows(
      [prompt("go", { turnId: "live1" }), spawn({ turnId: "live1" }), tool({ turnId: "live1" })],
      [turn("live1", { done: false })],
      "turn",
    );
    expect(shape(rows)).toEqual(["message:user", "jobs(1)", "live(1)"]);
    expect((rows[1] as { id: string }).id).toBe(`jobs:${rows[1].kind === "jobs" ? rows[1].items[0].id : ""}`);
  });

  it("shows every spawn of a finished turn again under its answer", () => {
    const a = spawn();
    const b = spawn();
    const c = spawn();
    const rows = buildRows([prompt("go"), a, b, tool(), c, msg("Answer.")], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "jobs(2)", "fold(1)", "jobs(1)", "message:agent", "recap(3)"]);
    const recap = rows[5];
    expect(recap.kind === "jobs" && recap.items).toEqual([a, b, c]);
    // Its own key: the batches it repeats are still on screen above it.
    expect(new Set(rows.map((r) => (r.kind === "item" ? r.item.id : r.id))).size).toBe(rows.length);
  });

  it("puts the recap before the turn's decided cards and artefacts", () => {
    const c: Item = { id: `card:r${n++}`, kind: "card", status: "saved", turnId: "turn1", card: { kind: "add_mcp_server" } };
    const rows = buildRows([prompt("go"), spawn(), c, artefact("a", 1), msg("Answer.")], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "jobs(1)", "message:agent", "recap(1)", "card:tool", "artefacts(a@1)"]);
  });

  it("shows no recap for a finished turn with no answer", () => {
    const rows = buildRows([prompt("go"), spawn(), tool()], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "jobs(1)", "fold(1)"]);
  });

  it("shows no recap while the turn runs", () => {
    const rows = buildRows(
      [prompt("go", { turnId: "live1" }), spawn({ turnId: "live1" }), msg("Started them.", { turnId: "live1" })],
      [turn("live1", { done: false })],
      "turn",
    );
    expect(shape(rows)).toEqual(["message:user", "jobs(1)", "message:agent"]);
  });

  it("names the recap's turn, for the changed-files card", () => {
    const rows = buildRows([prompt("go"), spawn(), msg("Answer.")], [turn("turn1")], "idle");
    expect(rows.map(rowTurnID)).toEqual(["turn1", "turn1", "turn1", "turn1"]);
  });

  it("counts spawns in summaries", () => {
    expect(summarise([spawn(), spawn(), tool()])).toBe("Ran 1 command and started 2 agents");
  });
});

describe("rowTurnID", () => {
  it("names the turn for each row kind", () => {
    const rows = buildRows(
      [prompt("go"), tool(), msg("Answer.")],
      [turn("turn1")],
      "idle",
    );
    expect(rows.map(rowTurnID)).toEqual(["turn1", "turn1", "turn1"]);
  });
});

describe("labels", () => {
  it("summarises calls as a sentence, grouped by verb", () => {
    expect(summarise([tool(), tool(), tool({ toolKind: "read" })])).toBe("Ran 2 commands and made 1 read");
    expect(summarise([tool(), tool(), ...[1, 2, 3].map(() => tool({ toolKind: "edit" }))])).toBe(
      "Ran 2 commands and made 3 edits",
    );
  });

  it("names a single kind on its own", () => {
    expect(summarise([tool({ toolKind: "read" })])).toBe("Made 1 read");
    expect(summarise([tool({ toolKind: "search" }), tool({ toolKind: "search" })])).toBe("Ran 2 searches");
  });

  it("orders kinds within a clause regardless of call order", () => {
    expect(summarise([tool({ toolKind: "search" }), tool(), tool()])).toBe("Ran 2 commands and 1 search");
  });

  // A kind a newer harness sends that this client has no name for.
  const UNKNOWN = "mystery" as Item["toolKind"];

  it("joins three or more kinds with commas and a final and", () => {
    const items = [
      ...[1, 2, 3, 4].map(() => tool({ toolKind: "read" })),
      ...[1, 2, 3].map(() => tool({ toolKind: "edit" })),
      tool({ toolKind: UNKNOWN }),
    ];
    expect(summarise(items)).toBe("Made 4 reads, 3 edits and 1 other call");
  });

  it("joins three or more clauses the same way", () => {
    const items = [tool({ toolKind: "agent" }), tool({ toolKind: "think" }), tool({ toolKind: "fetch" }), tool()];
    expect(summarise(items)).toBe("Ran 1 command, made 1 fetch, thought once and started 1 agent");
  });

  it("counts thinking as once, twice, then n times", () => {
    const think = (k: number) => Array.from({ length: k }, () => tool({ toolKind: "think" }));
    expect(summarise(think(1))).toBe("Thought once");
    expect(summarise(think(2))).toBe("Thought twice");
    expect(summarise(think(3))).toBe("Thought 3 times");
  });

  it("counts unknown and missing kinds as other calls", () => {
    expect(summarise([tool({ toolKind: UNKNOWN }), tool({ toolKind: undefined })])).toBe("Made 2 other calls");
  });

  it("is empty when there are no tool calls", () => {
    expect(summarise([])).toBe("");
    expect(summarise([msg("just talk")])).toBe("");
  });

  it("labels folds with measured durations", () => {
    expect(foldLabel(turn("t", { startedAt: 1000, finishedAt: 35_000 }))).toBe("Worked for 34s");
    expect(
      foldLabel(turn("t", { startedAt: 0, finishedAt: 95_000, stopReason: "cancelled" })),
    ).toBe("Stopped after 1m 35s");
    expect(foldLabel(turn("t", { startedAt: 0, finishedAt: 5000, stopReason: "error" }))).toBe(
      "Failed after 5s",
    );
    expect(foldLabel(turn("t"))).toBe("Worked");
  });

  it("formats durations at each magnitude", () => {
    expect(formatDuration(400)).toBe("1s");
    expect(formatDuration(61_000)).toBe("1m 1s");
    expect(formatDuration(3_720_000)).toBe("1h 2m");
  });
});

describe("buildRows with cards", () => {
  function card(status: Item["status"] = "saved", over: Partial<Item> = {}): Item {
    return { id: `card:r${n++}`, kind: "card", status, turnId: "turn1", card: { kind: "add_mcp_server" }, ...over };
  }

  it("keeps a finished turn's card out of the fold, under the answer", () => {
    const c = card();
    const rows = buildRows([prompt("add linear"), tool(), c, tool(), msg("Added.")], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "fold(2)", "message:agent", "card:tool"]);
    expect(rows[3]).toEqual({ kind: "item", item: c });
  });

  it("keeps a card still waiting after its turn stopped", () => {
    const c = card("pending");
    const rows = buildRows([prompt("add linear"), tool(), c], [turn("turn1", { stopReason: "cancelled" })], "idle");
    expect(rows).toContainEqual({ kind: "item", item: c });
  });

  it("gives a card in a running turn its own row between tool runs", () => {
    const c = card("pending");
    const rows = buildRows(
      [prompt("add linear"), tool(), tool(), c, tool()],
      [turn("turn1", { done: false })],
      "turn",
    );
    expect(shape(rows)).toEqual(["message:user", "run(2)", "card:tool", "live(1)"]);
    expect(rows[2]).toEqual({ kind: "item", item: c });
  });

  it("belongs to its turn, for the changed-files card", () => {
    expect(rowTurnID({ kind: "item", item: card() })).toBe("turn1");
  });
});

describe("buildRows with plans", () => {
  function plan(over: Partial<Item> = {}): Item {
    return { id: `todo:c${n++}`, kind: "plan", steps: [{ text: "a", status: "pending" }], turnId: "turn1", ...over };
  }

  it("keeps a finished turn's plan in place, splitting the fold around it", () => {
    const p = plan();
    const rows = buildRows([prompt("go"), tool(), p, tool(), tool(), msg("Done.")], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "fold(1)", "plan", "fold(2)", "message:agent"]);
    expect(rows[2]).toEqual({ kind: "item", item: p });
  });

  it("marks only the fold pieces after the first as continued", () => {
    const rows = buildRows([prompt("go"), tool(), plan(), tool(), plan(), tool(), msg("Done.")], [turn("turn1")], "idle");
    const folds = rows.filter((r) => r.kind === "fold");
    expect(folds.map((r) => r.kind === "fold" && !!r.continued)).toEqual([false, true, true]);
  });

  it("keeps every plan of a finished turn, in order", () => {
    const a = plan();
    const b = plan();
    const rows = buildRows([prompt("go"), a, tool(), b, msg("Done.")], [turn("turn1")], "idle");
    expect(shape(rows)).toEqual(["message:user", "plan", "fold(1)", "plan", "message:agent"]);
    expect(rows[1]).toEqual({ kind: "item", item: a });
    expect(rows[3]).toEqual({ kind: "item", item: b });
  });

  it("breaks a running turn's tool run", () => {
    const rows = buildRows([prompt("go"), tool(), tool(), plan(), tool()], [turn("turn1", { done: false })], "turn");
    expect(shape(rows)).toEqual(["message:user", "run(2)", "plan", "live(1)"]);
  });
});

// A frame off the wire rebuilds every row; reuseRows is what keeps the ones
// that did not change as the objects the transcript already drew.
describe("reuseRows", () => {
  // The way apply.ts changes an item: a copy in its place, the rest untouched.
  const replace = (items: Item[], it: Item, over: Partial<Item>) =>
    items.map((x) => (x === it ? { ...x, ...over } : x));

  it("hands back the same rows, and the same array, when nothing changed", () => {
    const items = [prompt("go", { turnId: "t2" }), tool({ turnId: "t2" }), msg("Working on it", { turnId: "t2" })];
    const turns = [turn("t2", { done: false })];
    const before = buildRows(items, turns, "turn");
    // A job or usage tick: a new state whose items are a new array of the
    // same objects.
    const after = reuseRows(before, buildRows([...items], turns, "turn"));
    expect(after).toBe(before);
  });

  it("gives a streaming message a new row and keeps its neighbours", () => {
    const ask = prompt("go", { turnId: "t2" });
    const answer = msg("Work", { turnId: "t2" });
    const items = [ask, tool({ turnId: "t2" }), answer];
    const turns = [turn("t2", { done: false })];
    const before = buildRows(items, turns, "turn");

    const after = reuseRows(before, buildRows(replace(items, answer, { text: "Working" }), turns, "turn"));

    expect(after).not.toBe(before);
    expect(after[0]).toBe(before[0]);
    expect(after[1]).toBe(before[1]);
    expect(after[2]).not.toBe(before[2]);
    expect(after[2].kind === "item" && after[2].item.text).toBe("Working");
  });

  it("keeps the rows above a row that was just added", () => {
    const items = [prompt("go", { turnId: "t2" }), msg("First", { turnId: "t2" })];
    const turns = [turn("t2", { done: false })];
    const before = buildRows(items, turns, "turn");

    const after = reuseRows(before, buildRows([...items, tool({ turnId: "t2" })], turns, "turn"));

    expect(after).toHaveLength(3);
    expect(after.slice(0, 2)).toEqual(before);
    expect(after[0]).toBe(before[0]);
    expect(after[1]).toBe(before[1]);
  });

  it("gives a run that grew, or stopped being live, a new row", () => {
    const first = tool({ turnId: "t2", status: "in_progress" });
    const items = [prompt("go", { turnId: "t2" }), first];
    const turns = [turn("t2", { done: false })];
    const before = buildRows(items, turns, "turn");

    const grown = reuseRows(before, buildRows([...items, tool({ turnId: "t2" })], turns, "turn"));
    expect(grown[0]).toBe(before[0]);
    expect(grown[1]).not.toBe(before[1]);
    expect(grown[1].kind === "run" && grown[1].items).toHaveLength(2);

    // Same calls, but the thread has gone idle: the run is no longer live.
    const settled = reuseRows(before, buildRows(items, turns, "idle"));
    expect(settled[1]).not.toBe(before[1]);
    expect(settled[1].kind === "run" && settled[1].live).toBe(false);
  });

  it("keeps a recap row while its spawns are untouched, and redraws it when one changes", () => {
    const s = tool({ toolKind: "agent", status: "in_progress" });
    const items = [prompt("go"), s, msg("Done.")];
    const turns = [turn("turn1")];
    const before = buildRows(items, turns, "idle");
    const recapAt = before.findIndex((r) => r.kind === "jobs" && r.recap);
    expect(recapAt).toBeGreaterThan(-1);

    expect(reuseRows(before, buildRows([...items], turns, "idle"))).toBe(before);

    const after = reuseRows(before, buildRows(replace(items, s, { status: "completed" }), turns, "idle"));
    expect(after[recapAt]).not.toBe(before[recapAt]);
    expect(after[recapAt].kind === "jobs" && after[recapAt].recap).toBe(true);
  });

  it("does not reuse a recap row for a plain jobs row under the same key", () => {
    const s = tool({ toolKind: "agent" });
    const plain: Row = { kind: "jobs", id: "jobs:x", items: [s] };
    const recap: Row = { kind: "jobs", id: "jobs:x", items: [s], recap: true };
    expect(reuseRows([plain], [recap])[0]).toBe(recap);
    expect(reuseRows([recap], [plain])[0]).toBe(plain);
    expect(reuseRows([recap], [{ ...recap }])[0]).toBe(recap);
  });

  it("gives a fold a new row when its turn changed", () => {
    const items = [prompt("go"), tool(), msg("Done.")];
    const turns = [turn("turn1", { startedAt: 0, finishedAt: 1000 })];
    const before = buildRows(items, turns, "idle");

    // Anything that happens to a turn — its diff arriving, say — copies it,
    // as apply.ts does; the fold draws the turn, so it is drawn again.
    const after = reuseRows(before, buildRows(items, [{ ...turns[0] }], "idle"));

    expect(after[0]).toBe(before[0]);
    expect(after[1].kind).toBe("fold");
    expect(after[1]).not.toBe(before[1]);
    expect(after[2]).toBe(before[2]);
  });
});

describe("buildRows with inline pages", () => {
  // The artefacts' records, each as its latest showing left it.
  const records = (inline: string[], cards: string[] = []) => [
    ...inline.map((id) => makeArtefact({ id, inline: true })),
    ...cards.map((id) => makeArtefact({ id })),
  ];

  it("puts a finished turn's inline pages above its answer and its cards below", () => {
    const rows = buildRows(
      [prompt("chart it"), tool(), artefact("chart", 1, { inline: true }), artefact("notes", 1), tool(), msg("Here.")],
      [turn("turn1")],
      "idle",
      records(["chart"], ["notes"]),
    );
    expect(shape(rows)).toEqual(["message:user", "fold(2)", "artefacts(chart@1)", "message:agent", "artefacts(notes@1)"]);
  });

  it("goes by the last showing in the turn: a page shown again as a card moves under the answer", () => {
    const rows = buildRows(
      [prompt("go"), artefact("a", 1, { inline: true }), artefact("a", 2), msg("Done.")],
      [turn("turn1")],
      "idle",
      records([], ["a"]),
    );
    expect(shape(rows)).toEqual(["message:user", "message:agent", "artefacts(a@2)"]);
  });

  it("moves an earlier turn's page under its answer once a later turn shows it again as a card", () => {
    const items = [
      prompt("chart it"),
      artefact("a", 1, { inline: true }),
      msg("Here."),
      prompt("as a card", { turnId: "turn2" }),
      artefact("a", 2, { turnId: "turn2" }),
      msg("Done.", { turnId: "turn2" }),
    ];
    const turns = [turn("turn1"), turn("turn2")];
    expect(shape(buildRows(items.slice(0, 3), turns, "idle", records(["a"])))).toEqual([
      "message:user",
      "artefacts(a@1)",
      "message:agent",
    ]);
    expect(shape(buildRows(items, turns, "idle", records([], ["a"])))).toEqual([
      "message:user",
      "message:agent",
      "artefacts(a@1)",
      "message:user",
      "message:agent",
      "artefacts(a@2)",
    ]);
  });

  it("leaves a running turn's artefacts where they were shown, inline or not", () => {
    const rows = buildRows(
      [prompt("go"), artefact("a", 1, { inline: true }), artefact("b", 1), msg("Look.")],
      [turn("turn1", { done: false })],
      "turn",
    );
    expect(shape(rows)).toEqual(["message:user", "artefacts(a@1,b@1)", "message:agent"]);
  });
});
