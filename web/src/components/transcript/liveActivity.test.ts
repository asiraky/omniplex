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
    expect(liveActivity([prompt()], turns, "idle", false)).toBeUndefined();
  });

  it("times the running turn, not the one before it", () => {
    expect(liveActivity([prompt()], turns, "turn", false)?.startedAt).toBe(5_000);
  });

  it("is thinking before the turn has said anything", () => {
    const a = liveActivity([prompt()], turns, "turn", false);
    expect(a?.state).toBe("thinking");
    expect(a?.thought).toBeUndefined();
  });

  it("is thinking when the newest item is the previous turn's answer", () => {
    expect(liveActivity([said("done", "text", "t1")], turns, "turn", false)?.state).toBe("thinking");
  });

  it("is working while a call runs or is waiting to", () => {
    expect(liveActivity([prompt(), tool("in_progress")], turns, "turn", false)?.state).toBe("working");
    expect(liveActivity([prompt(), tool("pending")], turns, "turn", false)?.state).toBe("working");
  });

  it("is working while an earlier call runs beside a finished one", () => {
    const quick: Item = { ...tool("completed"), id: "y" };
    expect(liveActivity([prompt(), tool("in_progress"), quick], turns, "turn", false)?.state).toBe("working");
  });

  it("ignores a call a finished turn left running", () => {
    expect(liveActivity([tool("in_progress", "t1"), prompt()], turns, "turn", false)?.state).toBe("thinking");
  });

  it("is waiting while the user has something to answer", () => {
    expect(liveActivity([prompt(), tool("pending")], turns, "turn", true)).toEqual({ state: "waiting", startedAt: 5_000 });
  });

  it("leaves a call to its own row while that row is the newest thing", () => {
    const quick: Item = { ...tool("completed"), id: "y" };
    expect(liveActivity([prompt(), tool("in_progress"), quick], turns, "turn", false)?.foldedCall).toBeUndefined();
  });

  it("names a running call the agent has written past", () => {
    const build = tool("in_progress");
    const a = liveActivity([prompt(), build, said("While that builds")], turns, "turn", false);
    expect(a).toMatchObject({ state: "working", foldedCall: build });
  });

  it("names a running call the agent is thinking past", () => {
    const build = tool("in_progress");
    const thought = said("The build is slow", "thought");
    const a = liveActivity([prompt(), build, { ...tool("completed"), id: "y" }, thought], turns, "turn", false);
    expect(a).toMatchObject({ state: "thinking", thought, foldedCall: build });
  });

  it("names a running call that agents were spawned after", () => {
    const build = tool("in_progress");
    const spawn: Item = { ...tool("completed"), id: "s", toolKind: "agent" };
    expect(liveActivity([prompt(), build, spawn], turns, "turn", false)).toMatchObject({ state: "working", foldedCall: build });
  });

  it("leaves a running spawn to its own card", () => {
    const spawn: Item = { ...tool("in_progress"), toolKind: "agent" };
    const a = liveActivity([prompt(), spawn, said("They are reading")], turns, "turn", false);
    expect(a?.state).toBe("working");
    expect(a?.foldedCall).toBeUndefined();
  });

  it("is thinking once the newest call has finished", () => {
    expect(liveActivity([prompt(), tool("completed")], turns, "turn", false)?.state).toBe("thinking");
  });

  it("is working while text streams", () => {
    expect(liveActivity([prompt(), said("Here is")], turns, "turn", false)?.state).toBe("working");
  });

  it("carries the reasoning streaming at the tail", () => {
    const thought = said("Looking at the router", "thought");
    expect(liveActivity([prompt(), thought], turns, "turn", false)).toMatchObject({ state: "thinking", thought });
  });

  it("drops reasoning the agent has moved past", () => {
    const a = liveActivity([prompt(), said("Hmm", "thought"), tool("in_progress")], turns, "turn", false);
    expect(a?.thought).toBeUndefined();
  });

  it("looks past a block the harness opened and never filled", () => {
    const thought = said("Reading the tests", "thought");
    const a = liveActivity([prompt(), thought, said("  ", "thought")], turns, "turn", false);
    expect(a?.thought).toBe(thought);
  });
});

describe("thoughtPreview", () => {
  it("is the newest bold heading when the reasoning has them", () => {
    const md = "**Reading the router**\n\nIt routes by path.\n\n**Checking the tests**\n\nThere are three.";
    expect(thoughtPreview(md)).toBe("Checking the tests");
  });

  it("is the paragraph streaming now, past a bold line far back up the prose", () => {
    const md = "**Plan**\n\nRead the router.\n\nThen the tests, which are slow.";
    expect(thoughtPreview(md)).toBe("Then the tests, which are slow.");
  });

  it("is the newest finished sentences of a paragraph too long for two lines", () => {
    const md = "The router was rewritten last spring. ".repeat(6) + "Now the tests fail on the back button. And then it ret";
    const fit = "The router was rewritten last spring. ".repeat(3) + "Now the tests fail on the back button.";
    expect(thoughtPreview(md)).toBe(`…${fit}`);
  });

  it("is the start of a long paragraph with no sentence finished yet", () => {
    const md = "word ".repeat(50);
    expect(thoughtPreview(md)).toBe(md.trim());
  });

  it("is the heading while its section has just started", () => {
    expect(thoughtPreview("Earlier.\n\n**Checking the tests**")).toBe("Checking the tests");
  });

  it("keeps arithmetic asterisks", () => {
    expect(thoughtPreview("So 2 * 3 * 4 is 24.")).toBe("So 2 * 3 * 4 is 24.");
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
