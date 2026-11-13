// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { attachmentUrl, type Attachment } from "~/lib/attachments";

import {
  DRAFT_DEBOUNCE_MS,
  LAPSED_REASON,
  loadAttachments,
  loadDrafts,
  loadOutbox,
  loadStashes,
  persistDrafts,
  persistOutbox,
  persistStashes,
  saveAttachments,
  UPLOAD_LAPSED_REASON,
} from "./composerStorage";
import { createDraftStore } from "./draftStore";
import { createOutbox, type OutboxSend } from "./outbox";
import { createStashStore, pushStash, takeStash, type StashEntry } from "./stashStore";

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

/** A store seeded the way the app seeds it: from what the last page left. */
function loadedStore() {
  const store = createDraftStore();
  store.update(() => loadDrafts());
  return store;
}

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange"));
}

const ready = (over: Partial<Attachment> = {}): Attachment => ({
  key: "k1",
  name: "shot.png",
  previewUrl: "blob:x",
  status: "ready",
  id: "att1",
  mediaType: "image/png",
  size: 10,
  ...over,
});

describe("drafts", () => {
  it("survive a reload, debounced while typing", () => {
    vi.useFakeTimers();
    const store = loadedStore();
    const dispose = persistDrafts(store);

    store.set("a", "h");
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS - 1);
    store.set("a", "hello");
    store.set("b", "other");
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS - 1);
    expect(loadDrafts()).toEqual({});

    vi.advanceTimersByTime(1);
    expect(loadDrafts()).toEqual({ a: "hello", b: "other" });
    dispose();
  });

  it.each([
    ["the page is hidden", () => setVisibility("hidden")],
    ["the page is torn down", () => window.dispatchEvent(new Event("pagehide"))],
  ])("are written at once when %s", (_, trigger) => {
    vi.useFakeTimers();
    const store = loadedStore();
    const dispose = persistDrafts(store);

    store.set("a", "typed");
    trigger();
    expect(loadDrafts()).toEqual({ a: "typed" });
    dispose();
    setVisibility("visible");
  });

  it("forget a thread once its draft is emptied or it is gone", () => {
    const store = loadedStore();
    let dispose = persistDrafts(store);
    store.set("a", "one");
    store.set("b", "two");
    dispose();

    const next = loadedStore();
    expect(next.get("a")).toBe("one");
    dispose = persistDrafts(next);
    next.set("a", "");
    next.update(({ b: _, ...rest }) => rest);
    dispose();

    expect(loadDrafts()).toEqual({});
  });

  it("write an emptied draft at once, since its prompt is already in the outbox", () => {
    vi.useFakeTimers();
    const store = loadedStore();
    persistDrafts(store);
    store.set("a", "hello");
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS);
    expect(loadDrafts()).toEqual({ a: "hello" });

    store.set("a", "");
    expect(loadDrafts()).toEqual({});

    // Typing again is debounced as before.
    store.set("a", "next");
    expect(loadDrafts()).toEqual({});
    vi.advanceTimersByTime(DRAFT_DEBOUNCE_MS);
    expect(loadDrafts()).toEqual({ a: "next" });
  });

  it("leave alone a thread this page has not changed", () => {
    const store = loadedStore();
    const dispose = persistDrafts(store);
    // Another tab writes its own thread after this one loaded.
    localStorage.setItem("omniplex.draft.v1:other", "theirs");
    store.set("a", "mine");
    dispose();

    expect(loadDrafts()).toEqual({ a: "mine", other: "theirs" });
  });
});

describe("attachments", () => {
  it("keep only the ready refs across a reload, thumbnails pointed at the server", () => {
    const list = [
      ready({ progress: 1 }),
      ready({ key: "k2", name: "notes.zip", kind: "file", id: undefined, artefactId: "art1", previewUrl: "" }),
      ready({ key: "k3", status: "uploading", id: undefined }),
      ready({ key: "k4", status: "staged", id: undefined }),
      ready({ key: "k5", status: "error", error: "too big" }),
    ];
    saveAttachments({}, { t1: list });

    expect(loadAttachments()).toEqual({
      t1: [
        { key: "k1", name: "shot.png", status: "ready", id: "att1", mediaType: "image/png", size: 10, previewUrl: attachmentUrl("t1", "att1") },
        { key: "k2", name: "notes.zip", kind: "file", status: "ready", artefactId: "art1", mediaType: "image/png", size: 10, previewUrl: "" },
      ],
    });
  });

  it("forget a thread with none ready, or one that is gone", () => {
    const a = [ready()];
    const b = [ready({ key: "kb" })];
    saveAttachments({}, { a, b });

    saveAttachments({ a, b }, { a: [ready({ status: "uploading", id: undefined })] });
    expect(loadAttachments()).toEqual({});
  });

  it("do not rewrite a list that changed only in what is not stored", () => {
    const list = [ready()];
    saveAttachments({}, { t1: list });
    const setItem = vi.spyOn(Storage.prototype, "setItem");

    saveAttachments({ t1: list }, { t1: [{ ...list[0], progress: 0.5 }] });
    expect(setItem).not.toHaveBeenCalled();
  });
});

describe("stashes", () => {
  const stash = (id: string, over: Partial<StashEntry> = {}): StashEntry => ({
    id,
    text: id,
    attachments: [],
    at: 1,
    ...over,
  });

  it("survive a reload newest first, keeping only the ready files", () => {
    const store = createStashStore();
    const dispose = persistStashes(store);
    const files = [ready(), ready({ key: "k2", status: "uploading", id: undefined }), ready({ key: "k3", id: undefined })];
    store.update((all) => pushStash(all, "t1", stash("old", { at: 1 })).next);
    store.update((all) => pushStash(all, "t1", stash("new", { at: 2, attachments: files })).next);
    store.update((all) => pushStash(all, "t2", stash("other")).next);

    expect(loadStashes()).toEqual({
      t1: [
        {
          id: "new",
          text: "new",
          at: 2,
          attachments: [{ key: "k1", name: "shot.png", status: "ready", id: "att1", mediaType: "image/png", size: 10, previewUrl: attachmentUrl("t1", "att1") }],
        },
        stash("old", { at: 1 }),
      ],
      t2: [stash("other")],
    });
    dispose();
  });

  it("forget a thread whose stash empties or that goes", () => {
    const store = createStashStore();
    const dispose = persistStashes(store);
    store.update(() => ({ a: [stash("x")], b: [stash("y")] }));

    store.update((all) => takeStash(all, "a", "x").next);
    expect(Object.keys(loadStashes())).toEqual(["b"]);
    store.update(() => ({ b: [] }));
    expect(loadStashes()).toEqual({});
    dispose();
  });

  it("write only the thread that changed", () => {
    const store = createStashStore();
    const dispose = persistStashes(store);
    store.update(() => ({ a: [stash("x")] }));
    const setItem = vi.spyOn(Storage.prototype, "setItem");

    store.update((all) => pushStash(all, "b", stash("y")).next);
    expect(setItem).toHaveBeenCalledTimes(1);
    expect(setItem).toHaveBeenCalledWith("omniplex.stash.v1:b", expect.any(String));
    dispose();
  });

  it("drop invalid entries rather than fail on them, and rewrite the list clean", () => {
    localStorage.setItem("omniplex.stash.v1:t1", "{not json");
    localStorage.setItem("omniplex.stash.v1:t2", JSON.stringify({ id: "x" }));
    localStorage.setItem(
      "omniplex.stash.v1:t3",
      JSON.stringify([
        "x",
        { id: 1, text: "t", at: 1 },
        { id: "no-at", text: "t" },
        { id: "no-text", at: 1 },
        // Its only file was never uploaded, so nothing is left of it.
        { id: "empty", text: " ", at: 1, attachments: [{ key: "k", name: "n", status: "uploading" }] },
        { id: "ok", text: "kept", at: 1, attachments: "bad" },
      ]),
    );

    const loaded = loadStashes();
    expect(loaded).toEqual({ t3: [stash("ok", { text: "kept" })] });

    const dispose = persistStashes(createStashStore(loaded));
    expect(localStorage.getItem("omniplex.stash.v1:t1")).toBeNull();
    expect(localStorage.getItem("omniplex.stash.v1:t2")).toBeNull();
    expect(JSON.parse(localStorage.getItem("omniplex.stash.v1:t3")!)).toEqual([stash("ok", { text: "kept" })]);
    dispose();
  });
});

describe("outbox", () => {
  const entry = (commandId: string, over: Partial<OutboxSend> = {}): OutboxSend => ({
    commandId,
    threadId: "t1",
    text: `text of ${commandId}`,
    attachments: [ready()],
    ...over,
  });
  /** Another tab of the same browser: same localStorage, its own session. */
  const inAnotherTab = <T,>(run: () => T): T => {
    const mine = sessionStorage.getItem("omniplex.tab.v1");
    sessionStorage.clear();
    try {
      return run();
    } finally {
      if (mine) sessionStorage.setItem("omniplex.tab.v1", mine);
    }
  };
  const ids = (entries: readonly { commandId: string }[]) => entries.map((e) => e.commandId);

  beforeEach(() => vi.useFakeTimers());

  it("brings back what was unacked and what failed, files pointed at the server", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    for (const id of ["c1", "c2", "c3"]) {
      outbox.add(entry(id));
      vi.advanceTimersByTime(1);
    }
    outbox.fail("c2", "timed out");
    outbox.ack("c3");

    const revived = [ready({ previewUrl: attachmentUrl("t1", "att1") })];
    expect(loadOutbox().entries).toEqual([
      { ...entry("c1"), attachments: revived, status: "pending", sentAt: expect.any(Number) },
      { ...entry("c2"), attachments: revived, status: "failed", reason: "timed out", sentAt: expect.any(Number) },
    ]);
  });

  it("keeps a send's steer or queue choice across a reload, and drops one it does not know", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    outbox.add(entry("c1", { delivery: "queue" }));
    outbox.add(entry("c2", { delivery: "steer" }));
    outbox.add(entry("c3"));
    const key = "omniplex.outbox.v1:c3";
    localStorage.setItem(key, JSON.stringify({ ...JSON.parse(localStorage.getItem(key)!), delivery: "shout" }));

    const byId = Object.fromEntries(loadOutbox().entries.map((e) => [e.commandId, e]));
    expect(byId.c1.delivery).toBe("queue");
    expect(byId.c2.delivery).toBe("steer");
    expect(byId.c3).not.toHaveProperty("delivery");
  });

  it("keeps the order of sends across a reload, a retry counting as the latest", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    for (const id of ["c1", "c2", "c3"]) {
      outbox.add(entry(id));
      vi.advanceTimersByTime(1);
    }
    outbox.fail("c1", "x");
    outbox.add(entry("c1"));

    const reloaded = loadOutbox().entries;
    expect(ids(reloaded)).toEqual(["c2", "c3", "c1"]);

    // And again once the reloaded outbox is the one persisting.
    const next = createOutbox(reloaded);
    persistOutbox(next);
    next.ack("c2");
    vi.advanceTimersByTime(1);
    next.add(entry("c4"));
    expect(ids(loadOutbox().entries)).toEqual(["c3", "c1", "c4"]);
  });

  it("keeps a dismissed note dismissed across a reload", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    outbox.add(entry("c1"));
    outbox.fail("c1", "x");
    outbox.dismiss("c1");

    expect(loadOutbox().entries).toMatchObject([{ commandId: "c1", dismissed: true }]);
  });

  it("removes stored entries the outbox it persists no longer holds", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    outbox.add(entry("c1", { threadId: "gone" }));
    outbox.add(entry("c2"));

    // As the app runs it: persisting what was loaded, then pruning it.
    const loaded = createOutbox(loadOutbox().entries);
    persistOutbox(loaded);
    loaded.prune((id) => id !== "gone");

    expect(ids(loadOutbox().entries)).toEqual(["c2"]);
  });

  it("leaves alone what another tab is still waiting on", () => {
    const mine = createOutbox();
    persistOutbox(mine);
    mine.add(entry("c1"));

    // The other tab sends and hears back, and a reload of it comes up while
    // this tab's send is still out.
    inAnotherTab(() => {
      const theirs = createOutbox();
      persistOutbox(theirs);
      theirs.add(entry("c2"));
      theirs.ack("c2");
      expect(loadOutbox().entries).toEqual([]);
    });

    expect(ids(loadOutbox().entries)).toEqual(["c1"]);
  });

  it("takes over another tab's send once that tab has been gone too long to be waiting", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    outbox.add(entry("c1"));
    vi.advanceTimersByTime(60_000);

    inAnotherTab(() => expect(loadOutbox().entries).toMatchObject([{ commandId: "c1", status: "pending" }]));
  });

  it("lapses a send left unanswered too long, whichever tab sent it, rather than send it", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    outbox.add(entry("mine"));
    vi.advanceTimersByTime(10 * 60_000);
    outbox.add(entry("recent"));

    const { entries, lapsed } = loadOutbox();
    expect(entries.map((e) => [e.commandId, e.status])).toEqual([
      ["mine", "failed"],
      ["recent", "pending"],
    ]);
    expect(lapsed).toEqual([entries[0]]);
    expect(lapsed[0]).toMatchObject({ reason: LAPSED_REASON, text: "text of mine" });

    inAnotherTab(() => {
      const there = loadOutbox();
      expect(ids(there.lapsed)).toEqual(["mine"]);
      // This tab's recent send is still its own.
      expect(ids(there.entries)).toEqual(["mine"]);
    });
  });

  it("lapses a send left waiting on its uploads, since the files it waited on are gone", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    outbox.wait(entry("c1", { attachments: [ready(), ready({ key: "k2", status: "uploading", id: undefined })] }));

    const { entries, lapsed } = loadOutbox();
    expect(lapsed).toEqual(entries);
    expect(entries).toMatchObject([
      {
        commandId: "c1",
        status: "failed",
        reason: UPLOAD_LAPSED_REASON,
        cannotRetry: true,
        text: "text of c1",
      },
    ]);
    // Only what had finished comes back to the composer.
    expect(entries[0].attachments.map((a) => a.key)).toEqual(["k1"]);

    // And stays an upload failure across the next reload.
    persistOutbox(createOutbox(entries));
    expect(loadOutbox().entries).toMatchObject([{ status: "failed", cannotRetry: true }]);
  });

  it("leaves alone a send another live tab is waiting on the uploads of, for minutes", () => {
    // This tab has an id of its own before the other one writes.
    loadOutbox();
    inAnotherTab(() => {
      const theirs = createOutbox();
      persistOutbox(theirs);
      theirs.wait(entry("c1"));
    });

    expect(loadOutbox()).toEqual({ entries: [], lapsed: [] });
    // Past the minute a pending send gets: a big file on 4G takes longer.
    vi.advanceTimersByTime(5 * 60_000);
    expect(loadOutbox()).toEqual({ entries: [], lapsed: [] });
    vi.advanceTimersByTime(5 * 60_000);
    expect(loadOutbox().lapsed).toMatchObject([{ commandId: "c1", cannotRetry: true }]);
  });

  it("shares a failed send with another tab, so a retry from there is still a retry", () => {
    const outbox = createOutbox();
    persistOutbox(outbox);
    outbox.add(entry("c1"));
    outbox.fail("c1", "x");

    inAnotherTab(() =>
      expect(loadOutbox().entries).toMatchObject([{ commandId: "c1", reason: "x" }]),
    );
  });
});

describe("what storage holds", () => {
  it("skips corrupt and foreign values", () => {
    localStorage.setItem("omniplex.attachments.v1:t1", "{not json");
    localStorage.setItem(
      "omniplex.attachments.v1:t2",
      JSON.stringify([{ key: 1 }, "x", { key: "k", name: "n", status: "uploading", id: "i" }, { key: "k", name: "n", status: "ready" }, { key: "ok", name: "n", status: "ready", id: "i" }]),
    );
    const stored = {
      c1: [],
      c2: { threadId: "t", text: "x", status: "pending", sentAt: 1 },
      c3: { commandId: "c3", threadId: "t", text: "x", status: "sent", sentAt: 1 },
      c4: { commandId: "c4", threadId: "t", text: "x", status: "failed", sentAt: 1 },
      c5: { commandId: "c5", threadId: "t", text: "x", status: "pending" },
      c6: { commandId: "c6", threadId: "t", text: "kept", status: "failed", reason: "r", sentAt: 1, attachments: "bad" },
    };
    for (const [id, v] of Object.entries(stored)) localStorage.setItem(`omniplex.outbox.v1:${id}`, JSON.stringify(v));
    localStorage.setItem("omniplex.outbox.v1:c7", "{not json");

    expect(loadAttachments()).toEqual({ t2: [{ key: "ok", name: "n", status: "ready", id: "i", previewUrl: attachmentUrl("t2", "i") }] });
    expect(loadOutbox().entries).toEqual([{ commandId: "c6", threadId: "t", text: "kept", status: "failed", reason: "r", sentAt: 1, attachments: [] }]);
  });

  it("costs the memory, not the interaction, when storage is denied", () => {
    vi.useFakeTimers();
    for (const method of ["getItem", "setItem", "removeItem", "key"] as const) {
      // Storage.prototype covers sessionStorage too.
      vi.spyOn(Storage.prototype, method).mockImplementation(() => {
        throw new DOMException("denied", "SecurityError");
      });
    }

    expect(loadDrafts()).toEqual({});
    expect(loadAttachments()).toEqual({});
    expect(loadOutbox().entries).toEqual([]);
    expect(loadStashes()).toEqual({});

    const stashes = createStashStore();
    persistStashes(stashes);
    expect(() => stashes.update(() => ({ a: [{ id: "x", text: "x", attachments: [], at: 1 }] }))).not.toThrow();

    const store = createDraftStore();
    const dispose = persistDrafts(store);
    store.set("a", "typed");
    expect(() => vi.runAllTimers()).not.toThrow();
    expect(() => dispose()).not.toThrow();

    expect(() => saveAttachments({}, { t1: [ready()] })).not.toThrow();
    const outbox = createOutbox();
    persistOutbox(outbox);
    expect(() => outbox.add(bare("c1"))).not.toThrow();
  });
});

function bare(commandId: string) {
  return { commandId, threadId: "t1", text: "x", attachments: [] };
}
