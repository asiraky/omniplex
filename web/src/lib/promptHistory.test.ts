import { describe, expect, it } from "vitest";

import type { Item, Turn } from "~/protocol";

import {
  promptHistory,
  recentPrompts,
  sameHistory,
  stepHistory,
} from "./promptHistory";

let n = 0;
const user = (text: string | undefined, extra: Partial<Item> = {}): Item => ({
  id: `i${n++}`,
  kind: "message",
  role: "user",
  text,
  ...extra,
});

describe("promptHistory", () => {
  it("keeps the user's own prompts, oldest first and trimmed", () => {
    const items: Item[] = [
      user("  fix the build \n"),
      { id: "a", kind: "message", role: "agent", text: "done" },
      { id: "t", kind: "tool", text: "ls" },
      user("now the tests"),
    ];
    expect(promptHistory(items, [])).toEqual(["fix the build", "now the tests"]);
  });

  it("skips empty prompts, such as an image sent on its own", () => {
    expect(promptHistory([user(""), user("   "), user(undefined), user("hi")], [])).toEqual(["hi"]);
  });

  it("skips a subagent's messages", () => {
    expect(promptHistory([user("mine"), user("from the Task tool", { parentId: "task1" })], [])).toEqual(["mine"]);
  });

  it("keeps a consecutive repeat once but not a repeat further back", () => {
    expect(promptHistory([user("go"), user(" go "), user("stop"), user("go")], [])).toEqual(["go", "stop", "go"]);
  });

  it("skips the prompt the server wrote to pick up a recovered turn", () => {
    const turns: Turn[] = [
      { id: "t1", prompt: "build it", done: true },
      { id: "t2", prompt: "Continue", done: true, recovery: { resumeOf: "t1", attempt: 1 } },
    ];
    const items = [user("build it", { turnId: "t1" }), user("Continue", { turnId: "t2" })];
    expect(promptHistory(items, turns)).toEqual(["build it"]);
  });
});

describe("sameHistory", () => {
  it("compares length and every string", () => {
    expect(sameHistory(["a", "b"], ["a", "b"])).toBe(true);
    expect(sameHistory([], [])).toBe(true);
    expect(sameHistory(["a"], ["a", "b"])).toBe(false);
    expect(sameHistory(["a", "b"], ["a", "c"])).toBe(false);
  });
});

describe("stepHistory", () => {
  const entries = ["one", "two", "three"];
  const back = (position: { index: number; recalled: string } | null, current: string, list = entries) =>
    stepHistory({ direction: "back", entries: list, position, current });
  const forward = (position: { index: number; recalled: string } | null, current: string, list = entries) =>
    stepHistory({ direction: "forward", entries: list, position, current });

  it("recalls the newest prompt into an empty composer", () => {
    expect(back(null, "")).toEqual({ position: { index: 2, recalled: "three" }, text: "three" });
  });

  it("counts a composer holding only whitespace as empty", () => {
    expect(back(null, " \n")?.text).toBe("three");
  });

  it("leaves a typed draft alone", () => {
    expect(back(null, "half a thought")).toBeNull();
  });

  it("does nothing with no history", () => {
    expect(back(null, "", [])).toBeNull();
  });

  it("steps back one at a time and stops at the oldest", () => {
    expect(back({ index: 2, recalled: "three" }, "three")).toEqual({
      position: { index: 1, recalled: "two" },
      text: "two",
    });
    expect(back({ index: 0, recalled: "one" }, "one")).toBeNull();
  });

  it("treats an edited recall as a fresh draft", () => {
    expect(back({ index: 2, recalled: "three" }, "three!")).toBeNull();
    expect(forward({ index: 1, recalled: "two" }, "two!")).toBeNull();
  });

  it("finds its place again when a prompt lands mid-recall", () => {
    // "two" was recalled at index 1, but the history changed under it and
    // index 1 now holds a different prompt.
    const grown = ["zero", "one", "two", "three"];
    expect(back({ index: 1, recalled: "two" }, "two", grown)).toEqual({
      position: { index: 1, recalled: "one" },
      text: "one",
    });
  });

  it("falls back to the last matching entry when the text repeats", () => {
    const list = ["go", "stop", "go"];
    expect(back({ index: 5, recalled: "go" }, "go", list)).toEqual({
      position: { index: 1, recalled: "stop" },
      text: "stop",
    });
  });

  it("drops a position whose text is no longer in the history", () => {
    expect(back({ index: 1, recalled: "gone" }, "gone")).toBeNull();
    expect(forward({ index: 1, recalled: "gone" }, "gone")).toBeNull();
  });

  it("does nothing forward without a position", () => {
    expect(forward(null, "")).toBeNull();
    expect(forward(null, "draft")).toBeNull();
  });

  it("steps forward, then past the newest back to an empty composer", () => {
    expect(forward({ index: 0, recalled: "one" }, "one")).toEqual({
      position: { index: 1, recalled: "two" },
      text: "two",
    });
    expect(forward({ index: 2, recalled: "three" }, "three")).toEqual({ position: null, text: "" });
  });
});

describe("recentPrompts", () => {
  it("lists newest first, each prompt once where it was last sent", () => {
    expect(recentPrompts(["a", "b", "a", "c", "b"], 10)).toEqual(["b", "c", "a"]);
  });

  it("stops at the limit", () => {
    expect(recentPrompts(["a", "b", "c", "d"], 2)).toEqual(["d", "c"]);
    expect(recentPrompts(["a"], 0)).toEqual([]);
  });

  it("is empty with no history", () => {
    expect(recentPrompts([], 5)).toEqual([]);
  });
});
