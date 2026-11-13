// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";

import { typeInto } from "~/lib/typeInto";
import { stubExecCommand } from "~/test/execCommand";

const exec = stubExecCommand();

function textarea(value: string) {
  const el = document.createElement("textarea");
  el.value = value;
  document.body.appendChild(el);
  return el;
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("typeInto", () => {
  it("types the text over the range, focused, and reports it done", () => {
    const edit = exec.editing();
    const el = textarea("say /rev now");
    const input = vi.fn();
    el.addEventListener("input", input);

    expect(typeInto(el, 4, 8, "/review ")).toBe(true);

    expect(edit).toHaveBeenCalledWith("insertText", false, "/review ");
    expect(el.value).toBe("say /review  now");
    expect(document.activeElement).toBe(el);
    expect(input).toHaveBeenCalledTimes(1);
  });

  it("deletes the range when there is nothing to type", () => {
    const edit = exec.editing();
    const el = textarea("/compact");

    expect(typeInto(el, 0, 8, "")).toBe(true);

    expect(edit).toHaveBeenCalledWith("delete");
    expect(el.value).toBe("");
  });

  it("gives up when the browser has no execCommand", () => {
    exec.absent();
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
    const edit = exec.editing();
    const el = textarea("/rev");
    el.disabled = true;

    expect(typeInto(el, 0, 4, "/review ")).toBe(false);
    expect(edit).not.toHaveBeenCalled();
    expect(el.value).toBe("/rev");
  });
});
