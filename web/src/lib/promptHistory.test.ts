import { describe, expect, it } from "vitest";

import type { Item } from "~/protocol";

import {
  caretOnFirstLine,
  caretOnLastLine,
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
    expect(promptHistory(items)).toEqual(["fix the build", "now the tests"]);
  });

  it("skips empty prompts, such as an image sent on its own", () => {
    expect(promptHistory([user(""), user("   "), user(undefined), user("hi")])).toEqual(["hi"]);
  });

  it("skips a subagent's messages", () => {
    expect(promptHistory([user("mine"), user("from the Task tool", { parentId: "task1" })])).toEqual(["mine"]);
  });

  it("keeps a consecutive repeat once but not a repeat further back", () => {
    expect(promptHistory([user("go"), user(" go "), user("stop"), user("go")])).toEqual(["go", "stop", "go"]);
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

describe("caret line checks", () => {
  const value = "first\nsecond\nthird";

  it("is on the first line up to and including the end of that line", () => {
    expect(caretOnFirstLine(value, 0, 0)).toBe(true);
    expect(caretOnFirstLine(value, 5, 5)).toBe(true);
    expect(caretOnFirstLine(value, 6, 6)).toBe(false);
  });

  it("judges the first line by where a selection starts", () => {
    expect(caretOnFirstLine(value, 2, 15)).toBe(true);
  });

  it("is on the last line from just after the last newline", () => {
    expect(caretOnLastLine(value, 13, 13)).toBe(true);
    expect(caretOnLastLine(value, value.length, value.length)).toBe(true);
    expect(caretOnLastLine(value, 12, 12)).toBe(false);
  });

  it("judges the last line by where a selection ends", () => {
    expect(caretOnLastLine(value, 0, 15)).toBe(true);
    expect(caretOnLastLine(value, 0, 4)).toBe(false);
  });

  it("treats a single-line value as both first and last line", () => {
    expect(caretOnFirstLine("", 0, 0)).toBe(true);
    expect(caretOnLastLine("", 0, 0)).toBe(true);
    expect(caretOnFirstLine("one line", 3, 3)).toBe(true);
    expect(caretOnLastLine("one line", 3, 3)).toBe(true);
  });
});
