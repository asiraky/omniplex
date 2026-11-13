// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { ComposerHandle } from "~/components/Composer";
import type { ComposerItem } from "~/protocol";

import { createThreadStore } from "./threadStore";
import type { ComposerDrafts } from "./useComposerDrafts";
import { useRecentSkills } from "./useRecentSkills";

const bro = { insertText: "/bro" } as ComposerItem;

/** One thread holding `draft`, and a composer that types when `types` says so. */
function setup(draft: string, { isDesktop, types }: { isDesktop: boolean; types: boolean }) {
  const setDraft = vi.fn();
  const store = { getDraft: () => draft, setDraft } as unknown as ComposerDrafts;
  const composer: ComposerHandle = {
    focusEnd: vi.fn(),
    typeAtEnd: vi.fn(() => types),
    openModelPicker: vi.fn(),
    sendAndNewThread: vi.fn(),
    send: vi.fn(),
  };
  const hook = renderHook(() =>
    useRecentSkills({
      activeId: "t1",
      projectId: "p1",
      threadStore: createThreadStore(null),
      store,
      isDesktop,
      loadComposerItems: () => Promise.resolve([]),
    }),
  );
  hook.result.current.composerRef.current = composer;
  act(() => hook.result.current.pick(bro));
  return { setDraft, composer };
}

describe("picking a recent skill", () => {
  it("types the token in on desktop, a space from what is already there", () => {
    const { setDraft, composer } = setup("fix it", { isDesktop: true, types: true });
    expect(composer.typeAtEnd).toHaveBeenCalledWith(" /bro ");
    expect(setDraft).not.toHaveBeenCalled();
  });

  it("adds no space after a draft that already ends in one, or an empty one", () => {
    expect(setup("fix it ", { isDesktop: true, types: true }).composer.typeAtEnd).toHaveBeenCalledWith(
      "/bro ",
    );
    expect(setup("", { isDesktop: true, types: true }).composer.typeAtEnd).toHaveBeenCalledWith("/bro ");
  });

  it("writes the draft and focuses its end on desktop when the browser cannot type it", () => {
    const { setDraft, composer } = setup("fix it", { isDesktop: true, types: false });
    expect(setDraft).toHaveBeenCalledWith("t1", "fix it /bro ");
    expect(composer.focusEnd).toHaveBeenCalledWith("fix it /bro ".length);
  });

  it("writes the draft on a phone without touching the input, so no keyboard comes up", () => {
    const { setDraft, composer } = setup("fix it", { isDesktop: false, types: true });
    expect(setDraft).toHaveBeenCalledWith("t1", "fix it /bro ");
    expect(composer.typeAtEnd).not.toHaveBeenCalled();
    expect(composer.focusEnd).not.toHaveBeenCalled();
  });
});
