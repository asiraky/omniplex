// @vitest-environment jsdom
import { act, cleanup, render, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { createDraftStore, useDraft, type DraftStore } from "./draftStore";
import { useComposerDrafts } from "./useComposerDrafts";

afterEach(cleanup);
beforeEach(() => localStorage.clear());

const thread = (id: string) => ({ id }) as ThreadMeta;

/** An app-shaped tree: a parent holding the store, and one composer per thread. */
function mount(store: DraftStore) {
  const renders = { app: 0, a: 0, b: 0 };
  function Composer({ id }: { id: "a" | "b" }) {
    renders[id]++;
    return <span data-testid={id}>{useDraft(store, id)}</span>;
  }
  function App() {
    renders.app++;
    return (
      <>
        <Composer id="a" />
        <Composer id="b" />
      </>
    );
  }
  const view = render(<App />);
  const text = (id: string) => view.getByTestId(id).textContent;
  return { renders, text };
}

describe("draft store", () => {
  it("rerenders only the composer whose draft changed", () => {
    const store = createDraftStore();
    const { renders, text } = mount(store);

    act(() => store.set("a", "h"));
    act(() => store.set("a", "hi"));

    expect(text("a")).toBe("hi");
    expect(renders).toEqual({ app: 1, a: 3, b: 1 });

    act(() => store.set("b", "yo"));
    expect(text("b")).toBe("yo");
    expect(renders).toEqual({ app: 1, a: 3, b: 2 });
  });

  it("tells nobody when a write changes nothing", () => {
    const store = createDraftStore();
    store.set("a", "hi");
    const listener = vi.fn();
    store.subscribe(listener);

    store.set("a", "hi");
    // A thread with no draft already reads as empty.
    store.set("b", "");
    store.update((all) => all);
    expect(listener).not.toHaveBeenCalled();

    store.set("a", "hello");
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("applies an update against the drafts as they are when it runs", () => {
    const store = createDraftStore();
    store.set("a", "typed");
    store.update((all) => ({ ...all, a: ["queued", all.a].join("\n\n") }));
    expect(store.get("a")).toBe("queued\n\ntyped");
  });

  it("stops telling a listener once it unsubscribes", () => {
    const store = createDraftStore();
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);
    unsubscribe();
    store.set("a", "hi");
    expect(listener).not.toHaveBeenCalled();
  });
});

describe("useComposerDrafts", () => {
  it("keeps one store and one result object while only drafts change", () => {
    const threads = [thread("a")];
    const hook = renderHook(() => useComposerDrafts(threads, "a"));
    const first = hook.result.current;
    act(() => first.setDraft("a", "hi"));
    hook.rerender();
    expect(hook.result.current).toBe(first);
    expect(first.getDraft("a")).toBe("hi");
  });

  it("drops the drafts of threads that leave the list, but not of ones not listed yet", () => {
    const hook = renderHook(({ threads }) => useComposerDrafts(threads, "a"), {
      initialProps: { threads: [thread("a"), thread("b")] },
    });
    const { setDraft, getDraft } = hook.result.current;
    act(() => {
      setDraft("a", "for a");
      setDraft("b", "for b");
      // Created and typed into before the broadcast listing it arrives.
      setDraft("c", "for c");
    });

    hook.rerender({ threads: [thread("b")] });

    expect(getDraft("a")).toBe("");
    expect(getDraft("b")).toBe("for b");
    expect(getDraft("c")).toBe("for c");
  });

  it("takes a prompt about to go again out of a draft the last page left it in", () => {
    localStorage.setItem(
      "omniplex.outbox.v2:c1",
      JSON.stringify({ commandId: "c1", threadId: "a", text: "sent", attachments: [], status: "pending", sentAt: 0 }),
    );
    // The page died after the send but before the cleared draft was written.
    localStorage.setItem("omniplex.draft.v1:a", "sent\n\ntyped after");

    const page = renderHook(() => useComposerDrafts([thread("a")], "a")).result.current;
    expect(page.getDraft("a")).toBe("typed after");
    expect(page.outbox.get()).toMatchObject([{ commandId: "c1", status: "pending" }]);
  });

  it("forgets what a thread deleted while the page was away left behind", () => {
    localStorage.setItem("omniplex.draft.v1:gone", "orphan");
    localStorage.setItem("omniplex.draft.v1:new-thread", "first message");
    const hook = renderHook(({ threads }) => useComposerDrafts(threads, null), {
      initialProps: { threads: [] as ThreadMeta[] },
    });
    // The list has not arrived yet: nothing is known to be gone.
    expect(hook.result.current.getDraft("gone")).toBe("orphan");

    hook.rerender({ threads: [thread("a")] });
    expect(hook.result.current.getDraft("gone")).toBe("");
    expect(hook.result.current.getDraft("new-thread")).toBe("first message");
  });
});
