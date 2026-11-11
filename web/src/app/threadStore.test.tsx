// @vitest-environment jsdom
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ThreadState } from "~/protocol";

import { createThreadStore, shallow, useThread, type ThreadStore } from "./threadStore";

afterEach(cleanup);

const state = (over: Partial<ThreadState> = {}) =>
  ({ threadId: "a", seq: 1, title: "one", jobs: [], items: [], ...over }) as unknown as ThreadState;

const titleOf = (s: ThreadState | null) => s?.title;
// A fresh object every call, as a selector picking several fields makes.
const pick = (s: ThreadState | null) => s && { title: s.title, items: s.items };

/** Two readers of the one store: one draws the title, one a shallow pick. */
function mount(store: ThreadStore) {
  const renders = { title: 0, pick: 0 };
  function Title() {
    renders.title++;
    return <span data-testid="title">{useThread(store, titleOf)}</span>;
  }
  function Pick() {
    renders.pick++;
    const p = useThread(store, pick, shallow);
    return <span data-testid="pick">{p ? `${p.title}:${p.items.length}` : "none"}</span>;
  }
  const view = render(
    <>
      <Title />
      <Pick />
    </>,
  );
  const text = (id: string) => view.getByTestId(id).textContent;
  return { renders, text };
}

describe("thread store", () => {
  it("rerenders a reader only when its slice changes", () => {
    const s = state();
    const store = createThreadStore(s);
    const { renders, text } = mount(store);
    expect(renders).toEqual({ title: 1, pick: 1 });

    // A job tick: a new state, nothing either reader draws changed.
    act(() => store.set({ ...s, seq: 2, jobs: [{ id: "j" }] as any }));
    expect(renders).toEqual({ title: 1, pick: 1 });

    act(() => store.set({ ...s, seq: 3, title: "two" }));
    expect(text("title")).toBe("two");
    expect(text("pick")).toBe("two:0");
    expect(renders).toEqual({ title: 2, pick: 2 });
  });

  it("settles on a selector that builds a new object each time", () => {
    const store = createThreadStore(state());
    const { renders, text } = mount(store);
    // A selector whose result were compared by identity would loop here
    // before the first render finished.
    expect(text("pick")).toBe("one:0");
    expect(renders.pick).toBe(1);
  });

  it("tells nobody when the same state is set again", () => {
    const s = state();
    const store = createThreadStore(s);
    const listener = vi.fn();
    store.subscribe(listener);

    store.set(s);
    expect(listener).not.toHaveBeenCalled();

    store.set(null);
    expect(listener).toHaveBeenCalledTimes(1);
    expect(store.get()).toBeNull();
  });

  it("stops telling a listener once it unsubscribes", () => {
    const store = createThreadStore(null);
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);
    unsubscribe();
    store.set(state());
    expect(listener).not.toHaveBeenCalled();
  });

  it("follows a thread being let go and another attached", () => {
    const store = createThreadStore(state());
    const { text } = mount(store);

    act(() => store.set(null));
    expect(text("pick")).toBe("none");

    act(() => store.set(state({ threadId: "b", title: "other" })));
    expect(text("pick")).toBe("other:0");
  });
});

describe("shallow", () => {
  it("compares objects and arrays field by field, not deeper", () => {
    const items: unknown[] = [];
    expect(shallow({ a: 1, items }, { a: 1, items })).toBe(true);
    expect(shallow({ a: 1, items }, { a: 1, items: [] })).toBe(false);
    expect(shallow({ a: 1 }, { a: 1, b: undefined })).toBe(false);
    expect(shallow([1, 2], [1, 2])).toBe(true);
    expect(shallow([1, 2], { 0: 1, 1: 2 } as unknown)).toBe(false);
    expect(shallow(null, {})).toBe(false);
    expect(shallow(NaN, NaN)).toBe(true);
  });
});
