// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { applyEvent, emptyState } from "~/apply";
import { createDraftStore } from "~/app/draftStore";
import { createOutbox } from "~/app/outbox";
import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import type { Event, PendingElicitation, PendingPermission, ThreadState } from "~/protocol";
import { render } from "~/test/harness";

import { SuggestionChip, suggestionShown, threadSuggestion } from "./SuggestionChip";

function ev(seq: number, type: string, payload: Record<string, unknown>): Event {
  return { threadId: "t", seq, timestamp: 0, type, payload };
}

/** A thread whose one turn ended the given way, with a suggestion for it. */
function after(stopReason = "end_turn"): ThreadState {
  let s: ThreadState = { ...emptyState("t"), phase: "idle" };
  s = applyEvent(s, ev(1, "turn.started", { turnId: "u1", prompt: "go" }));
  s = applyEvent(s, ev(2, "turn.finished", { turnId: "u1", stopReason }));
  return applyEvent(s, ev(3, "turn.suggested", { turnId: "u1", text: "run the tests" }));
}

const EMPTY = { empty: true, staged: false, sending: false };
const shown = (s: ThreadState, composer = EMPTY) => suggestionShown(threadSuggestion(s), composer);

describe("suggestionShown", () => {
  it("shows the suggestion after a turn that ended normally, with the composer empty", () => {
    expect(shown(after())).toBe("run the tests");
  });

  it("hides while a turn runs", () => {
    expect(shown({ ...after(), phase: "turn" })).toBeNull();
  });

  it.each(["error", "cancelled", "max_tokens", "refusal"])("hides after a turn that ended in %s", (reason) => {
    expect(shown(after(reason))).toBeNull();
  });

  it("hides while a permission is pending", () => {
    expect(shown({ ...after(), pendingPermissions: [{ requestId: "p" } as PendingPermission] })).toBeNull();
  });

  it("hides while a question is pending", () => {
    expect(shown({ ...after(), pendingElicitations: [{ requestId: "q" } as PendingElicitation] })).toBeNull();
  });

  it("hides while a prompt is queued", () => {
    expect(shown({ ...after(), queuedPrompts: [{ queueId: "q", prompt: "later", held: true }] })).toBeNull();
  });

  it("hides a suggestion for an older turn", () => {
    const s = after();
    const next = { ...s, turns: [...s.turns, { id: "u2", prompt: "more", done: true, stopReason: "end_turn" as const }] };
    expect(shown(next)).toBeNull();
  });

  it("hides on a closed thread", () => {
    expect(shown({ ...after(), closed: true })).toBeNull();
  });

  it("hides once anything is typed, files are staged, or a send is in flight", () => {
    expect(shown(after(), { ...EMPTY, empty: false })).toBeNull();
    expect(shown(after(), { ...EMPTY, staged: true })).toBeNull();
    expect(shown(after(), { ...EMPTY, sending: true })).toBeNull();
  });

  it("shows nothing with no suggestion", () => {
    let s: ThreadState = { ...emptyState("t"), phase: "idle" };
    s = applyEvent(s, ev(1, "turn.started", { turnId: "u1", prompt: "go" }));
    s = applyEvent(s, ev(2, "turn.finished", { turnId: "u1", stopReason: "end_turn" }));
    expect(shown(s)).toBeNull();
  });
});

describe("SuggestionChip", () => {
  function mount() {
    const draftStore = createDraftStore();
    const outbox = createOutbox();
    const onEdit = vi.fn();
    const onSend = vi.fn();
    render(
      <ThreadStoreContext.Provider value={createThreadStore(after())}>
        <SuggestionChip
          threadId="t"
          draftStore={draftStore}
          outbox={outbox}
          staged={false}
          onEdit={onEdit}
          onSend={onSend}
        />
      </ThreadStoreContext.Provider>,
    );
    return { draftStore, outbox, onEdit, onSend };
  }

  it("hands the text to the composer when tapped", () => {
    const { onEdit, onSend } = mount();
    fireEvent.click(screen.getByRole("button", { name: "Edit suggestion: run the tests" }));
    expect(onEdit).toHaveBeenCalledWith("run the tests");
    expect(onSend).not.toHaveBeenCalled();
  });

  it("sends the text from its arrow", () => {
    const { onEdit, onSend } = mount();
    fireEvent.click(screen.getByRole("button", { name: "Send suggestion" }));
    expect(onSend).toHaveBeenCalledWith("run the tests");
    expect(onEdit).not.toHaveBeenCalled();
  });

  it("goes while the composer has text and comes back when it is emptied", () => {
    const { draftStore } = mount();
    act(() => draftStore.set("t", "x"));
    expect(screen.queryByRole("button", { name: "Send suggestion" })).toBeNull();
    act(() => draftStore.set("t", ""));
    expect(screen.getByRole("button", { name: "Send suggestion" })).toBeTruthy();
  });

  it("goes while a send is waiting for its ack", () => {
    const { outbox } = mount();
    act(() => outbox.add({ commandId: "c", threadId: "t", text: "run the tests", attachments: [] }));
    expect(screen.queryByRole("button", { name: "Send suggestion" })).toBeNull();
    act(() => void outbox.ack("c"));
    expect(screen.getByRole("button", { name: "Send suggestion" })).toBeTruthy();
  });
});
