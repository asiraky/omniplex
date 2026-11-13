// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { createThreadStore } from "./threadStore";
import { useThreadSelection } from "./useThreadSelection";
import type { Wire } from "./useWire";

const threads = [{ id: "t1", projectId: "p1", phase: "idle" }] as ThreadMeta[];

function setup({ loaded = false, list = threads } = {}) {
  const client = { attach: vi.fn(), detach: vi.fn() };
  const wire = {
    clientRef: { current: client },
    activeRef: { current: null },
    threads: list,
    // Not loaded unless asked, so the restore stays out of it.
    threadsLoaded: loaded,
    threadStore: createThreadStore(null),
    dropResume: vi.fn(),
    resume: null,
    link: null,
    bootThread: null,
  } as unknown as Wire;
  const hook = renderHook((w: Wire) =>
    useThreadSelection({
      wire: w,
      isDesktop: true,
      onLeave: vi.fn(),
      onForget: vi.fn(),
      onOpenLink: vi.fn(),
    }),
    { initialProps: wire },
  );
  /** The first (or a later) thread list arriving. */
  const land = (next = list) => hook.rerender({ ...wire, threads: next, threadsLoaded: true });
  return { ...hook, client, land };
}

/** Back or Forward, settled: jsdom moves through history a task later. */
async function travel(go: () => void) {
  const popped = new Promise((r) => window.addEventListener("popstate", r, { once: true }));
  await act(async () => {
    go();
    await popped;
  });
}

// No `globals`, so Testing Library's own teardown never runs; a hook left
// mounted would go on answering the next test's popstates.
afterEach(cleanup);
beforeEach(() => {
  localStorage.clear();
  window.history.replaceState(null, "", "/");
});

describe("starting a new thread", () => {
  it("opens on the open thread's project when nothing more is asked", () => {
    const { result } = setup();
    act(() => result.current.select("t1"));
    act(() => result.current.startNew());
    expect(result.current.creating).toMatchObject({ projectId: "p1" });
    expect(result.current.activeId).toBeNull();
  });

  it("carries what the request asked for, the project falling back to the open thread's", () => {
    const { result } = setup();
    act(() => result.current.select("t1"));
    act(() => result.current.startNew({ folderId: "f1", kind: "attach", attachPath: "/w", focus: true }));
    expect(result.current.creating).toMatchObject({
      projectId: "p1",
      folderId: "f1",
      kind: "attach",
      attachPath: "/w",
      focus: true,
    });

    act(() => result.current.startNew({ projectId: "p2" }));
    expect(result.current.creating).toMatchObject({ projectId: "p2" });
    expect(result.current.creating?.folderId).toBeUndefined();
  });

  it("is a new request every time, even with a draft already open", () => {
    const { result } = setup();
    act(() => result.current.startNew({ projectId: "p1" }));
    const first = result.current.creating!.nonce;
    act(() => result.current.startNew({ projectId: "p1" }));
    expect(result.current.creating!.nonce).not.toBe(first);
  });

  it("keeps the draft on a project pick, dropping what belonged to the old project", () => {
    const { result } = setup();
    act(() => result.current.startNew({ projectId: "p1", folderId: "f1", kind: "attach", attachPath: "/w" }));
    const nonce = result.current.creating!.nonce;
    act(() => result.current.pickDraftProject("p2"));
    expect(result.current.creating).toEqual({ projectId: "p2", nonce });
  });
});

describe("history", () => {
  const two = [
    { id: "t1", projectId: "p1", phase: "idle" },
    { id: "t2", projectId: "p1", phase: "idle" },
  ] as ThreadMeta[];

  it("gives each picked thread its own entry, and Back and Forward move between them", async () => {
    const { result, client, land } = setup({ loaded: true, list: two });
    land(two);
    act(() => result.current.select("t1"));
    act(() => result.current.select("t2"));
    expect(window.location.pathname).toBe("/thread/t2");

    client.attach.mockClear();
    await travel(() => window.history.back());
    expect(window.location.pathname).toBe("/thread/t1");
    expect(result.current.activeId).toBe("t1");
    expect(client.attach).toHaveBeenCalledWith("t1");

    await travel(() => window.history.forward());
    expect(result.current.activeId).toBe("t2");
    expect(client.attach).toHaveBeenLastCalledWith("t2");
  });

  it("puts a new thread's draft on an entry of its own, and Back leaves it for the thread", async () => {
    const { result, client, land } = setup({ list: two });
    land(two);
    act(() => result.current.select("t1"));
    act(() => result.current.startNew());
    expect(window.location.pathname).toBe("/");
    expect(result.current.creating).not.toBeNull();

    client.attach.mockClear();
    await travel(() => window.history.back());
    expect(result.current.creating).toBeNull();
    expect(result.current.activeId).toBe("t1");
    expect(client.attach).toHaveBeenCalledWith("t1");

    await travel(() => window.history.forward());
    expect(result.current.creating).toMatchObject({ projectId: "p1" });
    expect(result.current.activeId).toBeNull();
  });

  it("turns the draft's entry into the thread it became", async () => {
    const { result, land } = setup({ list: two });
    land(two);
    act(() => result.current.select("t1"));
    act(() => result.current.startNew());
    const length = window.history.length;

    // What creating a thread does once the server has made it.
    act(() => result.current.select("t2"));
    expect(window.location.pathname).toBe("/thread/t2");
    expect(window.history.length).toBe(length);

    await travel(() => window.history.back());
    expect(result.current.activeId).toBe("t1");
  });

  it("goes home from Back onto a thread deleted since, and the entry stops naming it", async () => {
    const { result, client, land } = setup({ list: two });
    land(two);
    act(() => result.current.select("t1"));
    act(() => result.current.select("t2"));
    // t1 deleted elsewhere.
    land([two[1]]);

    client.attach.mockClear();
    await travel(() => window.history.back());
    expect(client.attach).not.toHaveBeenCalled();
    expect(result.current.activeId).toBeNull();
    expect(client.detach).toHaveBeenCalled();
    expect(window.location.pathname).toBe("/");
  });

  it("leaves the open thread alone on a popstate to its own URL", async () => {
    const { result, client, land } = setup({ list: two });
    land(two);
    act(() => result.current.select("t1"));
    // A sheet's entry sits at the thread's own URL.
    window.history.pushState({ omniplexLayer: 1 }, "", window.location.href);

    client.attach.mockClear();
    await travel(() => window.history.back());
    expect(client.attach).not.toHaveBeenCalled();
    expect(client.detach).not.toHaveBeenCalled();
    expect(result.current.activeId).toBe("t1");
  });

  it("goes home on Back onto the / the app opened at", async () => {
    const { result, client, land } = setup({ list: two });
    land(two);
    act(() => result.current.select("t1"));

    await travel(() => window.history.back());
    expect(window.location.pathname).toBe("/");
    expect(result.current.activeId).toBeNull();
    expect(client.detach).toHaveBeenCalled();
  });

  it("names the restored last thread in the URL, in place", () => {
    localStorage.setItem("omniplex.lastThread", "t1");
    const { result, land } = setup();
    const length = window.history.length;
    land();
    expect(result.current.activeId).toBe("t1");
    expect(window.location.pathname).toBe("/thread/t1");
    expect(window.history.length).toBe(length);
  });

  it("keeps the hash when it names the restored thread", () => {
    window.history.replaceState(null, "", "/#themes");
    localStorage.setItem("omniplex.lastThread", "t1");
    const { land } = setup();
    land();
    expect(window.location.pathname + window.location.hash).toBe("/thread/t1#themes");
  });

  it("leaves /setup alone when it restores", () => {
    window.history.replaceState(null, "", "/setup");
    localStorage.setItem("omniplex.lastThread", "t1");
    const { result, land } = setup();
    land();
    expect(result.current.activeId).toBe("t1");
    expect(window.location.pathname).toBe("/setup");
  });

  it("stops naming a thread that goes away while it is open", () => {
    const { result, land } = setup({ list: two });
    land(two);
    act(() => result.current.select("t1"));
    land([two[1]]);
    expect(result.current.activeId).toBeNull();
    expect(window.location.pathname).toBe("/");
    expect(localStorage.getItem("omniplex.lastThread")).toBeNull();
  });
});
