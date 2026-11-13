// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { applyEvent, emptyState } from "~/apply";
import { createDraftStore } from "~/app/draftStore";
import { createOutbox } from "~/app/outbox";
import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import type { Event, ThreadState } from "~/protocol";
import { render } from "~/test/harness";

import { SuggestionChip } from "./SuggestionChip";

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

  it("is spent by a send, even once the send is acked before its turn starts", () => {
    const { outbox } = mount();
    act(() => outbox.add({ commandId: "c", threadId: "t", text: "run the tests", attachments: [] }));
    expect(screen.queryByRole("button", { name: "Send suggestion" })).toBeNull();
    act(() => void outbox.ack("c"));
    expect(screen.queryByRole("button", { name: "Send suggestion" })).toBeNull();
  });
});
