// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";

import { typeInto } from "~/lib/typeInto";

// jsdom has no execCommand. This one edits the focused textarea's selection
// the way a browser does, and fires the textarea's own input event.
function browserEditing() {
  const exec = vi.fn((command: string, _ui?: boolean, text?: string) => {
    const el = document.activeElement;
    if (!(el instanceof HTMLTextAreaElement)) return false;
    el.setRangeText(command === "insertText" ? (text ?? "") : "", el.selectionStart, el.selectionEnd, "end");
    el.dispatchEvent(new InputEvent("input", { bubbles: true }));
    return true;
  });
  document.execCommand = exec;
  return exec;
}

function textarea(value: string) {
  const el = document.createElement("textarea");
  el.value = value;
  document.body.appendChild(el);
  return el;
}

const original = document.execCommand;
afterEach(() => {
  document.execCommand = original;
  document.body.innerHTML = "";
});

describe("typeInto", () => {
  it("types the text over the range, focused, and reports it done", () => {
    const exec = browserEditing();
    const el = textarea("say /rev now");
    const input = vi.fn();
    el.addEventListener("input", input);

    expect(typeInto(el, 4, 8, "/review ")).toBe(true);

    expect(exec).toHaveBeenCalledWith("insertText", false, "/review ");
    expect(el.value).toBe("say /review  now");
    expect(document.activeElement).toBe(el);
    expect(input).toHaveBeenCalledTimes(1);
  });

  it("deletes the range when there is nothing to type", () => {
    const exec = browserEditing();
    const el = textarea("/compact");

    expect(typeInto(el, 0, 8, "")).toBe(true);

    expect(exec).toHaveBeenCalledWith("delete");
    expect(el.value).toBe("");
  });

  it("gives up when the browser has no execCommand", () => {
    // @ts-expect-error: a browser without it
    document.execCommand = undefined;
    const el = textarea("/rev");

    expect(typeInto(el, 0, 4, "/review ")).toBe(false);
    expect(el.value).toBe("/rev");
  });

  it("gives up when the browser says it typed but the text is not there", () => {
    document.execCommand = vi.fn(() => true);
    const el = textarea("/rev");

    expect(typeInto(el, 0, 4, "/review ")).toBe(false);
  });

  it("gives up without typing when the textarea cannot take focus", () => {
    const exec = browserEditing();
    const el = textarea("/rev");
    el.disabled = true;

    expect(typeInto(el, 0, 4, "/review ")).toBe(false);
    expect(exec).not.toHaveBeenCalled();
    expect(el.value).toBe("/rev");
  });
});
