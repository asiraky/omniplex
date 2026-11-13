// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { caretOnEdge } from "./caretEdge";

function textarea(value: string, start: number, end = start) {
  const el = document.createElement("textarea");
  document.body.appendChild(el);
  el.value = value;
  el.setSelectionRange(start, end);
  return el;
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("caretOnEdge by line breaks", () => {
  const value = "first\nsecond\nthird";

  it("is on the top line up to and including the end of that line", () => {
    expect(caretOnEdge(textarea(value, 0), "top")).toBe(true);
    expect(caretOnEdge(textarea(value, 5), "top")).toBe(true);
    expect(caretOnEdge(textarea(value, 6), "top")).toBe(false);
  });

  it("is on the bottom line from just after the last break", () => {
    expect(caretOnEdge(textarea(value, 13), "bottom")).toBe(true);
    expect(caretOnEdge(textarea(value, value.length), "bottom")).toBe(true);
    expect(caretOnEdge(textarea(value, 12), "bottom")).toBe(false);
  });

  it("judges a selection by its start for the top and its end for the bottom", () => {
    expect(caretOnEdge(textarea(value, 2, 15), "top")).toBe(true);
    expect(caretOnEdge(textarea(value, 0, 15), "bottom")).toBe(true);
    expect(caretOnEdge(textarea(value, 0, 4), "bottom")).toBe(false);
  });

  it("leaves the copy it measures with out of the page", () => {
    const el = textarea("one line", 3);
    caretOnEdge(el, "top");
    expect(document.querySelectorAll("textarea")).toHaveLength(1);
  });
});

describe("caretOnEdge by wrapped lines", () => {
  // Lays text out ten characters to a line, twenty pixels each, the empty
  // value taking one line as a real textarea does.
  beforeEach(() => {
    vi.spyOn(HTMLTextAreaElement.prototype, "scrollHeight", "get").mockImplementation(function (
      this: HTMLTextAreaElement,
    ) {
      return Math.max(1, Math.ceil(this.value.length / 10)) * 20;
    });
  });

  // 35 characters: three full lines and a short fourth.
  const value = "aaaa bbbb cccc dddd eeee ffff gggg h";

  it("is not on the top line once the text before the caret wraps", () => {
    expect(caretOnEdge(textarea(value, 4), "top")).toBe(true);
    expect(caretOnEdge(textarea(value, 25), "top")).toBe(false);
  });

  it("is not on the bottom line while text after the caret wraps below it", () => {
    expect(caretOnEdge(textarea(value, value.length), "bottom")).toBe(true);
    expect(caretOnEdge(textarea(value, 12), "bottom")).toBe(false);
  });

  it("counts the rest of the word under the caret, which wraps with it", () => {
    // "aaaaaaaa|bbbbbbbb": the caret is in a word that runs onto line two.
    expect(caretOnEdge(textarea("aaaaaaaabbbbbbbb", 8), "top")).toBe(false);
  });
});
