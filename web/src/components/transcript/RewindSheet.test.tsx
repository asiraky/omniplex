// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { emptyState } from "~/apply";
import { createThreadStore, ThreadStoreContext, type ThreadStore } from "~/app/threadStore";
import { RewindContext, type RewindTarget } from "~/lib/rewind";
import { toast } from "~/lib/toast";
import type { Item, ThreadState, Turn } from "~/protocol";
import { render, viewport } from "~/test/harness";

import RewindSheet from "./RewindSheet";
import { UserMessage } from "./UserMessage";

vi.mock("~/lib/toast", () => ({ toast: { error: vi.fn() } }));

afterEach(() => vi.clearAllMocks());

const turn = (id: string, rewindable = true): Turn => ({ id, prompt: `ask ${id}`, done: true, rewindable });
const prompt = (turnId: string, id = `prompt:${turnId}`): Item => ({
  id,
  kind: "message",
  role: "user",
  turnId,
  text: `ask ${turnId}`,
  receivedAt: 1,
});

function thread(turns: Turn[]): ThreadState {
  return { ...emptyState("th1"), turns, items: turns.map((t) => prompt(t.id)) };
}

describe("the edit-from-here button", () => {
  function prompts(store: ThreadStore, items: Item[], open: ((item: Item) => void) | null) {
    render(
      <ThreadStoreContext.Provider value={store}>
        <RewindContext.Provider value={open}>
          {items.map((item) => (
            <UserMessage key={item.id} item={item} threadId="th1" artefacts={[]} />
          ))}
        </RewindContext.Provider>
      </ThreadStoreContext.Provider>,
    );
  }

  it("opens the confirmation for a prompt that opened a rewindable turn", () => {
    const open = vi.fn();
    prompts(createThreadStore(thread([turn("t1")])), [prompt("t1")], open);
    fireEvent.click(screen.getByRole("button", { name: "Edit from here" }));
    expect(open).toHaveBeenCalledWith(expect.objectContaining({ id: "prompt:t1" }));
  });

  it("is only on turn openers the thread can be cut back to", () => {
    // t2 has no anchor, so it cannot be rewound to; q1 was steered into t3.
    const store = createThreadStore(thread([turn("t1"), turn("t2", false), turn("t3"), turn("t4")]));
    prompts(store, [prompt("t1"), prompt("t2"), prompt("t3"), prompt("t3", "prompt:q1"), prompt("t4")], vi.fn());
    const offered = screen
      .getAllByRole("button", { name: "Edit from here" })
      .map((b) => b.closest("[data-msg-id]")?.getAttribute("data-msg-id"));
    expect(offered).toEqual(["prompt:t1", "prompt:t3", "prompt:t4"]);
  });

  it("goes while a turn runs and comes back when it is over", () => {
    const store = createThreadStore(thread([turn("t1")]));
    prompts(store, [prompt("t1")], vi.fn());
    act(() => store.set({ ...store.get()!, phase: "turn" }));
    expect(screen.queryByRole("button", { name: "Edit from here" })).toBeNull();
    act(() => store.set({ ...store.get()!, phase: "idle" }));
    expect(screen.getByRole("button", { name: "Edit from here" })).toBeTruthy();
  });

  it("is not there where the thread cannot rewind", () => {
    prompts(createThreadStore(thread([turn("t1")])), [prompt("t1")], null);
    expect(screen.queryByRole("button", { name: "Edit from here" })).toBeNull();
  });
});

describe("the rewind confirmation", () => {
  const target = (patch: Partial<RewindTarget> = {}): RewindTarget => ({
    threadId: "th1",
    turnId: "t2",
    text: "ask t2",
    later: 1,
    files: { count: 2, atLeast: false },
    restore: null,
    ...patch,
  });

  function sheet(t: RewindTarget, rewind: (restoreFiles: boolean) => Promise<string | null>) {
    const store = createThreadStore(thread([turn("t1"), turn("t2"), turn("t3")]));
    const onRewound = vi.fn();
    const onClose = vi.fn();
    render(
      <ThreadStoreContext.Provider value={store}>
        <RewindSheet target={t} rewind={rewind} onRewound={onRewound} onClose={onClose} />
      </ThreadStoreContext.Provider>,
    );
    return { store, onRewound, onClose };
  }

  it.each([
    ["Rewind, keep files", false],
    ["Rewind and restore files", true],
  ])("%s rewinds, then hands the prompt back", async (button, restoreFiles) => {
    const rewind = vi.fn(async () => null);
    const { onRewound, onClose } = sheet(target(), rewind);
    fireEvent.click(screen.getByRole("button", { name: button }));
    await waitFor(() => expect(onRewound).toHaveBeenCalledTimes(1));
    expect(rewind).toHaveBeenCalledWith(restoreFiles);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("holds shut while the rewind runs, and stays open with the reason when it is refused", async () => {
    let answer: (err: string | null) => void = () => {};
    const rewind = vi.fn(() => new Promise<string | null>((resolve) => (answer = resolve)));
    const { onRewound, onClose } = sheet(target(), rewind);
    fireEvent.click(screen.getByRole("button", { name: "Rewind, keep files" }));
    expect((screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => answer("a background job is running"));
    expect(screen.getByText("a background job is running")).toBeTruthy();
    expect(onRewound).not.toHaveBeenCalled();
    expect((screen.getByRole("button", { name: "Rewind, keep files" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("takes the turn leaving the thread as done, when the answer never comes", async () => {
    const rewind = vi.fn(() => new Promise<string | null>(() => {}));
    const { store, onRewound } = sheet(target(), rewind);
    fireEvent.click(screen.getByRole("button", { name: "Rewind, keep files" }));
    act(() => store.set({ ...store.get()!, seq: 9, turns: [turn("t1")] }));
    expect(onRewound).toHaveBeenCalledTimes(1);
  });

  it("hands the prompt back when the conversation went though the files did not", async () => {
    const { store, onRewound } = sheet(target(), async () => {
      store.set({ ...store.get()!, seq: 9, turns: [turn("t1")] });
      return "the files could not be restored";
    });
    fireEvent.click(screen.getByRole("button", { name: "Rewind and restore files" }));
    await waitFor(() => expect(onRewound).toHaveBeenCalledTimes(1));
    expect(toast.error).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ description: "the files could not be restored" }),
    );
  });

  it("offers no restore where files cannot be put back", () => {
    sheet(target({ restore: "shared" }), vi.fn());
    expect(screen.queryByRole("button", { name: "Rewind and restore files" })).toBeNull();
    expect(screen.getByRole("button", { name: "Rewind, keep files" })).toBeTruthy();
  });

  it("is a bottom sheet on a phone", () => {
    viewport("phone");
    sheet(target(), vi.fn());
    expect(document.querySelector('[data-slot="sheet-content"]')).not.toBeNull();
    expect(screen.getByRole("button", { name: "Rewind, keep files" })).toBeTruthy();
  });
});
