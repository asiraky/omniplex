import { describe, expect, it } from "vitest";

import type { Item, Turn } from "~/protocol";

import { liveActivity, thoughtPreview } from "./liveActivity";

const prompt = (turnId = "t2"): Item => ({ id: "u", kind: "message", role: "user", text: "go", turnId });
const tool = (status: string, turnId = "t2"): Item => ({ id: "x", kind: "tool", toolKind: "execute", status, turnId });
const said = (text: string, contentKind: "text" | "thought" = "text", turnId = "t2"): Item => ({
  id: `m:${text}`,
  kind: "message",
  role: "agent",
  contentKind,
  text,
  turnId,
});
const turns: Turn[] = [
  { id: "t1", prompt: "a", done: true, startedAt: 1_000, finishedAt: 2_000 },
  { id: "t2", prompt: "b", done: false, startedAt: 5_000 },
];

describe("liveActivity", () => {
  it("is nothing unless a turn is running", () => {
    expect(liveActivity([prompt()], turns, "idle")).toBeUndefined();
  });

  it("times the running turn, not the one before it", () => {
    expect(liveActivity([prompt()], turns, "turn")?.startedAt).toBe(5_000);
  });

  it("is thinking before the turn has said anything", () => {
    const a = liveActivity([prompt()], turns, "turn");
    expect(a?.state).toBe("thinking");
    expect(a?.thought).toBeUndefined();
  });

  it("is thinking when the newest item is the previous turn's answer", () => {
    expect(liveActivity([said("done", "text", "t1")], turns, "turn")?.state).toBe("thinking");
  });

  it("is working while a call runs or is waiting to", () => {
    expect(liveActivity([prompt(), tool("in_progress")], turns, "turn")?.state).toBe("working");
    expect(liveActivity([prompt(), tool("pending")], turns, "turn")?.state).toBe("working");
  });

  it("is thinking once the newest call has finished", () => {
    expect(liveActivity([prompt(), tool("completed")], turns, "turn")?.state).toBe("thinking");
  });

  it("is working while text streams", () => {
    expect(liveActivity([prompt(), said("Here is")], turns, "turn")?.state).toBe("working");
  });

  it("carries the reasoning streaming at the tail", () => {
    const thought = said("Looking at the router", "thought");
    expect(liveActivity([prompt(), thought], turns, "turn")).toMatchObject({ state: "thinking", thought });
  });

  it("drops reasoning the agent has moved past", () => {
    const a = liveActivity([prompt(), said("Hmm", "thought"), tool("in_progress")], turns, "turn");
    expect(a?.thought).toBeUndefined();
  });

  it("looks past a block the harness opened and never filled", () => {
    const thought = said("Reading the tests", "thought");
    const a = liveActivity([prompt(), thought, said("  ", "thought")], turns, "turn");
    expect(a?.thought).toBe(thought);
  });
});

describe("thoughtPreview", () => {
  it("is the newest bold heading when the reasoning has them", () => {
    const md = "**Reading the router**\n\nIt routes by path.\n\n**Checking the tests**\n\nThere are three.";
    expect(thoughtPreview(md)).toBe("Checking the tests");
  });

  it("is the newest paragraph of plain prose", () => {
    expect(thoughtPreview("First I looked.\n\nNow the `foo` call\nis *odd*.")).toBe("Now the foo call is odd.");
  });

  it("flattens links and list markers", () => {
    expect(thoughtPreview("- see [the docs](http://x) and **this**")).toBe("see the docs and this");
  });

  it("is empty for nothing", () => {
    expect(thoughtPreview("  \n\n ")).toBe("");
  });
});
