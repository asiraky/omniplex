// @vitest-environment jsdom
import { act, cleanup, render, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Attachment } from "~/lib/attachments";
import type { ThreadMeta } from "~/protocol";

import { loadStashes } from "./composerStorage";
import {
  createStashStore,
  MAX_STASH,
  pushStash,
  takeStash,
  useStash,
  type StashEntry,
} from "./stashStore";
import { useComposerDrafts } from "./useComposerDrafts";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
beforeEach(() => localStorage.clear());

const entry = (id: string, text = id): StashEntry => ({ id, text, attachments: [], at: 1 });
const thread = (id: string) => ({ id }) as ThreadMeta;
const file = (key: string, over: Partial<Attachment> = {}): Attachment => ({
  key,
  name: key,
  previewUrl: `blob:${key}`,
  status: "ready",
  id: `att-${key}`,
  ...over,
});

describe("stash store", () => {
  it("puts a new entry at the head and evicts the oldest past the limit", () => {
    let all = {};
    for (let i = 0; i < MAX_STASH; i++) {
      const pushed = pushStash(all, "a", entry(`e${i}`));
      expect(pushed.evicted).toEqual([]);
      all = pushed.next;
    }
    const { next, evicted } = pushStash(all, "a", entry("newest"));
    expect(evicted.map((e) => e.id)).toEqual(["e0"]);
    expect(next.a).toHaveLength(MAX_STASH);
    expect(next.a[0].id).toBe("newest");
    expect(next.a[MAX_STASH - 1].id).toBe("e1");
  });

  it("takes an entry out, drops a thread left with none, and changes nothing for one not there", () => {
    const all = { a: [entry("x"), entry("y")], b: [entry("z")] };
    const one = takeStash(all, "a", "x");
    expect(one.taken?.id).toBe("x");
    expect(one.next).toEqual({ a: [entry("y")], b: [entry("z")] });

    const last = takeStash(all, "b", "z");
    expect(last.next).toEqual({ a: all.a });

    expect(takeStash(all, "a", "gone")).toEqual({ next: all });
    expect(takeStash(all, "c", "x")).toEqual({ next: all });
  });

  it("keeps each thread's list the same while another thread's changes", () => {
    const store = createStashStore({ a: [entry("x")] });
    const before = store.get("a");
    expect(store.get("none")).toBe(store.get("other"));

    store.update((all) => pushStash(all, "b", entry("y")).next);
    expect(store.get("a")).toBe(before);
  });

  it("tells subscribers of a change, and nobody of a write that changes nothing", () => {
    const store = createStashStore();
    const listener = vi.fn();
    const unsubscribe = store.subscribe(listener);
    store.update((all) => all);
    expect(listener).not.toHaveBeenCalled();

    store.update((all) => pushStash(all, "a", entry("x")).next);
    expect(listener).toHaveBeenCalledTimes(1);

    unsubscribe();
    store.update((all) => takeStash(all, "a", "x").next);
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it("rerenders a reader only when its thread's stash changes", () => {
    const store = createStashStore();
    let renders = 0;
    function Menu() {
      renders++;
      return <span data-testid="n">{useStash(store, "a").length}</span>;
    }
    const view = render(<Menu />);

    act(() => store.update((all) => pushStash(all, "b", entry("y")).next));
    expect(renders).toBe(1);

    act(() => store.update((all) => pushStash(all, "a", entry("x")).next));
    expect(renders).toBe(2);
    expect(view.getByTestId("n").textContent).toBe("1");
  });
});

describe("stashing from the composer", () => {
  function mount() {
    const hook = renderHook(({ threads }) => useComposerDrafts(threads, "a"), {
      initialProps: { threads: [thread("a"), thread("b")] },
    });
    return { hook, page: () => hook.result.current };
  }

  it("moves the text and ready files into an entry, leaving unfinished uploads behind", () => {
    const { page } = mount();
    const uploading = file("up", { status: "uploading", id: undefined });
    act(() => {
      page().setDraft("a", "half a thought");
      page().setAttachments((all) => ({ ...all, a: [file("k1"), uploading] }));
    });
    const revoke = vi.spyOn(URL, "revokeObjectURL");

    let stashed = false;
    act(() => {
      stashed = page().stashDraft("a");
    });

    expect(stashed).toBe(true);
    expect(page().getDraft("a")).toBe("");
    expect(page().attachments.a).toEqual([uploading]);
    expect(page().stashStore.get("a")).toMatchObject([{ text: "half a thought", attachments: [file("k1")] }]);
    // The thumbnail lives on in the entry.
    expect(revoke).not.toHaveBeenCalled();
    // And survives a reload.
    expect(loadStashes().a).toMatchObject([{ text: "half a thought" }]);
  });

  it("does nothing for a composer with only blank text and no ready file", () => {
    const { page } = mount();
    act(() => {
      page().setDraft("a", "  \n");
      page().setAttachments((all) => ({ ...all, a: [file("e", { status: "error" })] }));
    });
    let stashed = true;
    act(() => {
      stashed = page().stashDraft("a");
    });
    expect(stashed).toBe(false);
    expect(page().getDraft("a")).toBe("  \n");
    expect(page().attachments.a).toHaveLength(1);
    expect(page().stashStore.get("a")).toEqual([]);
  });

  it("releases the thumbnails of an entry pushed past the limit", () => {
    const { page } = mount();
    act(() => {
      page().setDraft("a", "oldest");
      page().setAttachments((all) => ({ ...all, a: [file("old")] }));
    });
    act(() => {
      page().stashDraft("a");
    });
    for (let i = 0; i < MAX_STASH - 1; i++) {
      act(() => {
        page().setDraft("a", `draft ${i}`);
        page().stashDraft("a");
      });
    }
    const revoke = vi.spyOn(URL, "revokeObjectURL");

    act(() => {
      page().setDraft("a", "one more");
      page().stashDraft("a");
    });
    expect(revoke).toHaveBeenCalledWith("blob:old");
    expect(page().stashStore.get("a")).toHaveLength(MAX_STASH);
    expect(page().stashStore.get("a").some((e) => e.text === "oldest")).toBe(false);
  });

  it("brings an entry back into an empty composer, after any upload still going", () => {
    const { page } = mount();
    act(() => {
      page().setDraft("a", "parked");
      page().setAttachments((all) => ({ ...all, a: [file("k1")] }));
    });
    act(() => {
      page().stashDraft("a");
    });
    const uploading = file("up", { status: "uploading", id: undefined });
    act(() => page().setAttachments((all) => ({ ...all, a: [uploading] })));
    const id = page().stashStore.get("a")[0].id;

    let back: StashEntry | undefined;
    act(() => {
      back = page().unstash("a", id);
    });

    expect(back).toMatchObject({ text: "parked" });
    expect(page().getDraft("a")).toBe("parked");
    expect(page().attachments.a.map((a) => a.key)).toEqual(["up", "k1"]);
    expect(page().stashStore.get("a")).toEqual([]);
    expect(loadStashes()).toEqual({});
  });

  it("swaps what the composer holds into the stash when bringing one back", () => {
    const { page } = mount();
    act(() => {
      page().setDraft("a", "first");
      page().setAttachments((all) => ({ ...all, a: [file("k1")] }));
    });
    act(() => {
      page().stashDraft("a");
    });
    act(() => {
      page().setDraft("a", "second");
      page().setAttachments((all) => ({ ...all, a: [file("k2")] }));
    });
    const first = page().stashStore.get("a")[0].id;

    act(() => {
      page().unstash("a", first);
    });

    expect(page().getDraft("a")).toBe("first");
    expect(page().attachments.a.map((a) => a.key)).toEqual(["k1"]);
    expect(page().stashStore.get("a")).toMatchObject([{ text: "second", attachments: [{ key: "k2" }] }]);
  });

  it("gives back nothing for an entry already gone", () => {
    const { page } = mount();
    act(() => page().setDraft("a", "typed"));
    let back: StashEntry | undefined = entry("x");
    act(() => {
      back = page().unstash("a", "missing");
    });
    expect(back).toBeUndefined();
    expect(page().getDraft("a")).toBe("typed");
    expect(page().stashStore.get("a")).toEqual([]);
  });

  it("drops an entry, releasing its thumbnails", () => {
    const { page } = mount();
    act(() => {
      page().setDraft("a", "never mind");
      page().setAttachments((all) => ({ ...all, a: [file("k1")] }));
    });
    act(() => {
      page().stashDraft("a");
    });
    const revoke = vi.spyOn(URL, "revokeObjectURL");

    act(() => page().dropStash("a", page().stashStore.get("a")[0].id));
    expect(revoke).toHaveBeenCalledWith("blob:k1");
    expect(page().stashStore.get("a")).toEqual([]);
    expect(page().attachments.a).toEqual([]);
  });

  it("forgets the stashes of a thread that leaves the list, releasing their thumbnails", () => {
    const { hook, page } = mount();
    act(() => {
      page().setDraft("a", "for a");
      page().setAttachments((all) => ({ ...all, a: [file("k1")] }));
    });
    act(() => {
      page().stashDraft("a");
      page().setDraft("b", "for b");
      page().stashDraft("b");
    });
    const revoke = vi.spyOn(URL, "revokeObjectURL");

    hook.rerender({ threads: [thread("b")] });

    expect(revoke).toHaveBeenCalledWith("blob:k1");
    expect(page().stashStore.get("a")).toEqual([]);
    expect(page().stashStore.get("b")).toHaveLength(1);
    expect(Object.keys(loadStashes())).toEqual(["b"]);
  });

  it("brings stashes back after a reload, and forgets those of a thread deleted while away", () => {
    const stored = (threadId: string, text: string) =>
      localStorage.setItem(`omniplex.stash.v1:${threadId}`, JSON.stringify([{ id: `${threadId}1`, text, attachments: [], at: 1 }]));
    stored("a", "kept");
    stored("gone", "orphan");
    const hook = renderHook(({ threads }) => useComposerDrafts(threads, null), {
      initialProps: { threads: [] as ThreadMeta[] },
    });
    // The list has not arrived yet: nothing is known to be gone.
    expect(hook.result.current.stashStore.get("gone")).toHaveLength(1);

    hook.rerender({ threads: [thread("a")] });
    expect(hook.result.current.stashStore.get("a")).toMatchObject([{ text: "kept" }]);
    expect(hook.result.current.stashStore.get("gone")).toEqual([]);
    expect(localStorage.getItem("omniplex.stash.v1:gone")).toBeNull();
  });
});
