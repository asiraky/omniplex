// @vitest-environment jsdom
import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ComposerHandle } from "~/components/Composer";
import type { ThreadMeta } from "~/protocol";
import { viewport } from "~/test/harness";

import { createDraftStore } from "./draftStore";
import { LAST_THREAD } from "./threadKeys";
import { useAddToChat, useAppendToChat, useBoxAddToChat } from "./useAddToChat";
import type { ComposerDrafts } from "./useComposerDrafts";
import type { PanelControls } from "./usePanel";

function setup({ expanded = false } = {}) {
  const drafts = createDraftStore({ s1: "why does this fail?" });
  const store = { setDrafts: drafts.update, getDraft: drafts.get } as unknown as ComposerDrafts;
  const panel = { close: vi.fn(), expanded, toggleExpanded: vi.fn() } as unknown as PanelControls;
  const composer: ComposerHandle = {
    focusEnd: vi.fn(),
    openModelPicker: vi.fn(),
    typeAtEnd: vi.fn(),
    sendAndNewThread: vi.fn(),
    send: vi.fn(),
  };
  const { result } = renderHook(() =>
    useAddToChat({ activeId: "s1", store, panel, composerRef: { current: composer } }),
  );
  return { add: result.current, drafts, panel, composer };
}

describe("useAddToChat", () => {
  it("puts the quote after the draft and, on a phone, closes the panel over the composer", () => {
    viewport("phone");
    const { add, drafts, panel, composer } = setup();
    add("```\nboom\n```");
    expect(drafts.get("s1")).toBe("why does this fail?\n\n```\nboom\n```");
    expect(panel.close).toHaveBeenCalledOnce();
    // No focus: it would raise the keyboard over the transcript.
    expect(composer.focusEnd).not.toHaveBeenCalled();
  });

  it("on a desktop, brings back an expanded panel's composer and puts the cursor after the quote", () => {
    const { add, drafts, panel, composer } = setup({ expanded: true });
    add("```\nboom\n```");
    expect(panel.close).not.toHaveBeenCalled();
    expect(panel.toggleExpanded).toHaveBeenCalledOnce();
    expect(composer.focusEnd).toHaveBeenCalledWith(drafts.get("s1").length);
  });

  it("leaves a docked panel that is not expanded as it is", () => {
    const { add, panel, composer } = setup();
    add("```\nboom\n```");
    expect(panel.toggleExpanded).not.toHaveBeenCalled();
    expect(composer.focusEnd).toHaveBeenCalledOnce();
  });
});

// It is never handed the panel or the composer, so it cannot close, expand or
// focus either; what is left to check is the draft.
describe("useAppendToChat", () => {
  function setupAppend(activeId: string | null) {
    const drafts = createDraftStore({ s1: "first comment" });
    const store = { setDrafts: drafts.update, getDraft: drafts.get } as unknown as ComposerDrafts;
    const { result } = renderHook(() => useAppendToChat({ activeId, store }));
    return { append: result.current, drafts };
  }

  it("piles each block up after the draft, a paragraph apart", () => {
    const { append, drafts } = setupAppend("s1");
    append("second");
    append("third");
    expect(drafts.get("s1")).toBe("first comment\n\nsecond\n\nthird");
  });

  it("does nothing with no thread open", () => {
    const { append, drafts } = setupAppend(null);
    append("lost");
    expect(drafts.get("s1")).toBe("first comment");
  });
});

describe("useBoxAddToChat", () => {
  afterEach(() => localStorage.clear());

  const thread = (id: string, phase = "idle") => ({ id, title: id, headSeq: 1, phase }) as ThreadMeta;

  function setupBox({
    activeId = null as string | null,
    attached = true,
    threads = [thread("s1"), thread("s2")],
  } = {}) {
    const drafts = createDraftStore({ s1: "why does this fail?" });
    const store = { setDrafts: drafts.update, getDraft: drafts.get } as unknown as ComposerDrafts;
    const composer: ComposerHandle = {
      focusEnd: vi.fn(),
      openModelPicker: vi.fn(),
      typeAtEnd: vi.fn(),
      sendAndNewThread: vi.fn(),
      send: vi.fn(),
    };
    const onClose = vi.fn();
    const onOpenThread = vi.fn();
    const hook = renderHook(
      (p: { activeId: string | null; attached: boolean }) =>
        useBoxAddToChat({
          ...p,
          threads,
          store,
          composerRef: { current: composer },
          onClose,
          onOpenThread,
        }),
      { initialProps: { activeId, attached } },
    );
    return { hook, drafts, composer, onClose, onOpenThread };
  }

  it("quotes into the thread behind the terminal, closes it and puts the cursor after the quote", () => {
    const { hook, drafts, composer, onClose, onOpenThread } = setupBox({ activeId: "s1" });
    hook.result.current!("```\nboom\n```");
    expect(drafts.get("s1")).toBe("why does this fail?\n\n```\nboom\n```");
    expect(onClose).toHaveBeenCalledOnce();
    expect(onOpenThread).not.toHaveBeenCalled();
    expect(composer.focusEnd).toHaveBeenCalledWith(drafts.get("s1").length);
  });

  it("leaves the keyboard down on a phone", () => {
    viewport("phone");
    const { hook, composer, onClose } = setupBox({ activeId: "s1" });
    hook.result.current!("boom");
    expect(onClose).toHaveBeenCalledOnce();
    expect(composer.focusEnd).not.toHaveBeenCalled();
  });

  it("with no thread behind it, opens the last one and focuses its composer once it attaches", () => {
    localStorage.setItem(LAST_THREAD, "s2");
    const { hook, drafts, composer, onClose, onOpenThread } = setupBox({ activeId: null, attached: false });
    hook.result.current!("boom");
    expect(drafts.get("s2")).toBe("boom");
    expect(onOpenThread).toHaveBeenCalledWith("s2");
    expect(onClose).not.toHaveBeenCalled();
    expect(composer.focusEnd).not.toHaveBeenCalled();

    // The open lands, then the thread's state arrives a round trip later.
    hook.rerender({ activeId: "s2", attached: false });
    expect(composer.focusEnd).not.toHaveBeenCalled();
    hook.rerender({ activeId: "s2", attached: true });
    expect(composer.focusEnd).toHaveBeenCalledExactlyOnceWith("boom".length);

    // Not again on a later attach of the same thread.
    hook.rerender({ activeId: "s2", attached: false });
    hook.rerender({ activeId: "s2", attached: true });
    expect(composer.focusEnd).toHaveBeenCalledOnce();
  });

  it("opens the last thread on a phone without focusing it", () => {
    viewport("phone");
    localStorage.setItem(LAST_THREAD, "s2");
    const { hook, composer, onOpenThread } = setupBox({ activeId: null, attached: false });
    hook.result.current!("boom");
    expect(onOpenThread).toHaveBeenCalledWith("s2");
    hook.rerender({ activeId: "s2", attached: true });
    expect(composer.focusEnd).not.toHaveBeenCalled();
  });

  it("offers nothing with no thread open and none opened before", () => {
    const { hook } = setupBox({ activeId: null });
    expect(hook.result.current).toBeUndefined();
  });

  it.each([
    ["deleted", [thread("s1")]],
    ["closed", [thread("s1"), thread("s2", "closed")]],
  ])("offers nothing when the last thread has been %s", (_, threads) => {
    localStorage.setItem(LAST_THREAD, "s2");
    const { hook } = setupBox({ activeId: null, threads });
    expect(hook.result.current).toBeUndefined();
  });
});
