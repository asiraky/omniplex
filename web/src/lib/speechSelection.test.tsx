// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { SPEAK_ROOT_ATTR } from "./speechAttrs";
import { currentSelection, selects, useSelects } from "./speechSelection";

function transcript(...texts: string[]) {
  document.body.innerHTML = texts
    .map((t) => `<div><div ${SPEAK_ROOT_ATTR}><p>${t}</p></div><button>copy</button></div>`)
    .join("");
  return Array.from(document.querySelectorAll<HTMLElement>(`[${SPEAK_ROOT_ATTR}]`));
}

const textOf = (root: Element) => root.querySelector("p")!.firstChild!;

function selectRange(range: Range | null) {
  const sel = document.getSelection()!;
  sel.removeAllRanges();
  if (range) sel.addRange(range);
  document.dispatchEvent(new Event("selectionchange"));
}

function between(start: [Node, number], end: [Node, number]) {
  const r = document.createRange();
  r.setStart(...start);
  r.setEnd(...end);
  return r;
}

afterEach(() => {
  cleanup();
  document.getSelection()?.removeAllRanges();
  document.body.innerHTML = "";
});

describe("selects", () => {
  it("is every message a selection runs through, and none outside it", () => {
    const [a, b, c, d] = transcript("alpha one", "beta two", "gamma three", "delta four");
    const r = between([textOf(a), 6], [textOf(c), 5]);
    expect([a, b, c, d].map((root) => selects(r, root))).toEqual([true, true, true, false]);
  });

  it("does not count a message the selection only ends at the start of", () => {
    // What a triple-click on a paragraph produces in Chrome.
    const [a, b] = transcript("alpha one", "beta two");
    const r = between([textOf(a), 0], [b, 0]);
    expect(selects(r, a)).toBe(true);
    expect(selects(r, b)).toBe(false);
  });

  it("does not count a message whose footer alone is selected", () => {
    const [a, b] = transcript("alpha one", "beta two");
    const footer = a.parentElement!.querySelector("button")!.firstChild!;
    const r = between([footer, 0], [footer, 4]);
    expect(selects(r, a)).toBe(false);
    expect(selects(r, b)).toBe(false);
  });

  it("is false with no selection or no message", () => {
    const [a] = transcript("alpha");
    expect(selects(null, a)).toBe(false);
    expect(selects(between([textOf(a), 0], [textOf(a), 2]), null)).toBe(false);
  });
});

describe("useSelects", () => {
  it("follows the selection into and out of a message", () => {
    const [a, b] = transcript("alpha one", "beta two");
    const refA = { current: a };
    const refB = { current: b };
    const hookA = renderHook(() => useSelects(refA, true));
    const hookB = renderHook(() => useSelects(refB, true));
    expect([hookA.result.current, hookB.result.current]).toEqual([false, false]);

    act(() => selectRange(between([textOf(a), 0], [textOf(a), 5])));
    expect([hookA.result.current, hookB.result.current]).toEqual([true, false]);
    expect(currentSelection()?.toString()).toBe("alpha");

    act(() => selectRange(between([textOf(a), 6], [textOf(b), 4])));
    expect([hookA.result.current, hookB.result.current]).toEqual([true, true]);

    act(() => selectRange(null));
    expect([hookA.result.current, hookB.result.current]).toEqual([false, false]);
    expect(currentSelection()).toBeNull();
  });

  it("keeps a copy that a collapsing selection does not take with it", () => {
    const [a] = transcript("alpha one");
    renderHook(() => useSelects({ current: a }, true));
    act(() => selectRange(between([textOf(a), 0], [textOf(a), 5])));
    const held = currentSelection();
    act(() => document.getSelection()!.collapseToStart());
    expect(held?.toString()).toBe("alpha");
  });

  it("is off, and does not listen, when read-aloud is", () => {
    const [a] = transcript("alpha one");
    const hook = renderHook(() => useSelects({ current: a }, false));
    act(() => selectRange(between([textOf(a), 0], [textOf(a), 5])));
    expect(hook.result.current).toBe(false);
    expect(currentSelection()).toBeNull();
  });
});
