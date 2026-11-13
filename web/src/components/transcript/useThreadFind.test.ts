// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ThreadMatch, ThreadSearchResult } from "~/lib/threadFind";

import { FIND_DEBOUNCE_MS, useThreadFind } from "./useThreadFind";

type Call = {
  q: string;
  signal: AbortSignal;
  resolve: (r: ThreadSearchResult) => void;
  reject: (e: Error) => void;
};

/** A server that answers each search only when the test says so. */
function setup() {
  const calls: Call[] = [];
  const search = vi.fn(
    (q: string, signal: AbortSignal) =>
      new Promise<ThreadSearchResult>((resolve, reject) => calls.push({ q, signal, resolve, reject })),
  );
  const hook = renderHook(() => useThreadFind(search));
  const find = () => hook.result.current;
  const type = (q: string) => act(() => find().setQuery(q));
  const wait = (ms: number) => act(() => vi.advanceTimersByTime(ms));
  const answer = (call: Call, result: ThreadSearchResult) => act(async () => call.resolve(result));
  /** Type `q`, let the debounce pass, and answer with `result`. */
  const searchFor = async (q: string, result: ThreadSearchResult) => {
    type(q);
    wait(FIND_DEBOUNCE_MS);
    await answer(calls.at(-1)!, result);
  };
  return { hook, calls, search, find, type, wait, answer, searchFor };
}

const at = (...indexes: number[]): ThreadMatch[] => indexes.map((index) => ({ id: `i${index}`, index }));
const step = (f: () => void) => act(() => f());

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("useThreadFind", () => {
  it("runs the query once typing pauses", () => {
    const { calls, find, type, wait } = setup();
    type("he");
    wait(FIND_DEBOUNCE_MS - 50);
    type("hel");
    wait(FIND_DEBOUNCE_MS - 1);
    expect(calls).toHaveLength(0);
    expect(find().status).toBe("loading");

    wait(1);
    expect(calls.map((c) => c.q)).toEqual(["hel"]);
  });

  it("keeps a short query off the server and clears the result", async () => {
    const { calls, find, type, wait, searchFor } = setup();
    await searchFor("hello", { matches: at(1, 2), total: 2 });

    type("h");
    wait(FIND_DEBOUNCE_MS * 4);
    expect(calls).toHaveLength(1);
    expect(find()).toMatchObject({ status: "short", matches: [], total: 0, current: null, ordinal: 0 });

    type("");
    expect(find().status).toBe("idle");
  });

  it("counts characters, not UTF-16 units, and waits on blank text", () => {
    const { calls, find, type, wait } = setup();
    // One emoji: two units, one character.
    type("🦓");
    wait(FIND_DEBOUNCE_MS * 4);
    expect(find().status).toBe("short");
    type("   ");
    wait(FIND_DEBOUNCE_MS * 4);
    expect(find().status).toBe("short");
    expect(calls).toHaveLength(0);
  });

  it("opens on the newest match, counted against the true total", async () => {
    const { find, searchFor } = setup();
    await searchFor("hello", { matches: at(3, 7, 9), total: 12 });
    expect(find()).toMatchObject({ status: "ready", current: { index: 9 }, ordinal: 12, step: 1 });
  });

  it("finds nothing without selecting anything", async () => {
    const { find, searchFor } = setup();
    await searchFor("hello", { matches: [], total: 0 });
    expect(find()).toMatchObject({ status: "ready", current: null, ordinal: 0, step: 0 });
  });

  it("steps toward older with prev, wrapping from the oldest to the newest, and back with next", async () => {
    const { search, find, searchFor } = setup();
    await searchFor("hello", { matches: at(3, 7, 9), total: 12 });

    step(find().prev);
    expect(find()).toMatchObject({ current: { index: 7 }, ordinal: 11 });
    step(find().prev);
    expect(find()).toMatchObject({ current: { index: 3 }, ordinal: 10 });
    step(find().prev);
    expect(find()).toMatchObject({ current: { index: 9 }, ordinal: 12 });

    step(find().prev);
    step(find().next);
    expect(find().current?.index).toBe(9);
    expect(search).toHaveBeenCalledTimes(1);
  });

  it("re-runs on next from the newest, and wraps to the oldest when nothing newer came", async () => {
    const { calls, find, answer, searchFor } = setup();
    await searchFor("hello", { matches: at(3, 7, 9), total: 3 });

    step(find().next);
    expect(calls).toHaveLength(2);
    expect(calls[1].q).toBe("hello");
    expect(find().status).toBe("loading");
    // Stepping waits on the answer.
    step(find().next);
    step(find().prev);
    expect(calls).toHaveLength(2);
    expect(find().current?.index).toBe(9);

    await answer(calls[1], { matches: at(3, 7, 9), total: 3 });
    expect(find()).toMatchObject({ status: "ready", current: { index: 3 }, ordinal: 1 });
  });

  it("re-runs on next from the newest and lands on the first match said since", async () => {
    const { calls, find, answer, searchFor } = setup();
    await searchFor("hello", { matches: at(3, 7, 9), total: 3 });

    step(find().next);
    // The cap has pushed the oldest out by now.
    await answer(calls[1], { matches: at(7, 9, 15, 20), total: 5 });
    expect(find()).toMatchObject({ current: { index: 15 }, ordinal: 4 });
  });

  it("aborts a superseded request and ignores its answer", async () => {
    const { hook, calls, find, type, wait, answer } = setup();
    type("ab");
    wait(FIND_DEBOUNCE_MS);
    type("abc");
    expect(calls[0].signal.aborted).toBe(true);
    wait(FIND_DEBOUNCE_MS);

    await answer(calls[0], { matches: at(1), total: 1 });
    expect(find().status).toBe("loading");
    await answer(calls[1], { matches: at(2), total: 1 });
    expect(find().current?.index).toBe(2);

    step(find().rerun);
    hook.unmount();
    expect(calls[2].signal.aborted).toBe(true);
  });

  it("bumps step on every selection, even of the same match", async () => {
    const { calls, find, answer, searchFor } = setup();
    await searchFor("hello", { matches: at(4), total: 1 });
    expect(find().step).toBe(1);

    step(find().prev);
    expect(find()).toMatchObject({ current: { index: 4 }, step: 2 });

    step(find().next);
    await answer(calls[1], { matches: at(4), total: 1 });
    expect(find()).toMatchObject({ current: { index: 4 }, step: 3 });
  });

  it("reports a failure and keeps the query", async () => {
    const { calls, find, type, wait } = setup();
    type("hello");
    wait(FIND_DEBOUNCE_MS);
    await act(async () => calls[0].reject(new Error("search failed (500)")));
    expect(find()).toMatchObject({ status: "error", query: "hello", current: null });
  });
});
