import { describe, expect, it } from "vitest";

import type { Item, Turn } from "./protocol";
import { buildRows, foldLabel, formatDuration, rowTurnID, summarise, type Row } from "./rows";

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
    if (r.kind === "jobs") return `jobs(${r.items.length})`;
    if (r.kind === "run") return r.live ? `live(${r.items.length})` : `run(${r.items.length})`;
    return `${r.item.kind}:${r.item.role ?? "tool"}`;
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
    expect(shape(rows)).toEqual(["message:user", "fold(1)", "jobs(2)", "fold(1)", "message:agent"]);
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

  it("counts spawns in summaries", () => {
    expect(summarise([spawn(), spawn(), tool()])).toBe("1 command · 2 agents");
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
  it("summarises calls by kind", () => {
    expect(summarise([tool(), tool(), tool({ toolKind: "read" })])).toBe("1 read · 2 commands");
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
