// @vitest-environment jsdom
import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ComposerHandle } from "~/components/Composer";
import { viewport } from "~/test/harness";

import { createDraftStore } from "./draftStore";
import { useAddToChat } from "./useAddToChat";
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
