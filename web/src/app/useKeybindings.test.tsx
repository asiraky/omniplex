// @vitest-environment jsdom
import { cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useKeybindings, type AppKeyState, type KeyHandlers } from "./useKeybindings";

// jsdom reports no platform, so mod is Ctrl throughout.

const APP: AppKeyState = {
  paletteOpen: false,
  helpOpen: false,
  threadOpen: true,
  transcriptShown: true,
  turnRunning: true,
  panelOpen: false,
};

const added: Element[] = [];
const cleanups: (() => void)[] = [];

afterEach(() => {
  cleanup();
  for (const el of added.splice(0)) el.remove();
  for (const undo of cleanups.splice(0)) undo();
});

function mount(handlers: KeyHandlers, app: Partial<AppKeyState> = {}) {
  return renderHook(() => useKeybindings(handlers, { ...APP, ...app }));
}

function add<K extends keyof HTMLElementTagNameMap>(tag: K, attrs: Record<string, string> = {}) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  document.body.append(el);
  added.push(el);
  return el;
}

function press(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent("keydown", { bubbles: true, cancelable: true, ...init });
  target.dispatchEvent(e);
  return e;
}

const ctrlB: KeyboardEventInit = { key: "b", code: "KeyB", ctrlKey: true };
const escape: KeyboardEventInit = { key: "Escape", code: "Escape" };

describe("useKeybindings", () => {
  it("runs the command's handler and claims the event", () => {
    const toggleSidebar = vi.fn();
    mount({ toggleSidebar });

    const e = press(document.body, ctrlB);

    expect(toggleSidebar).toHaveBeenCalledOnce();
    expect(e.defaultPrevented).toBe(true);
  });

  it.each<[string, KeyboardEventInit]>([
    ["a key repeat", { repeat: true }],
    ["IME composition", { isComposing: true }],
    ["keyCode 229", { keyCode: 229 }],
  ])("ignores %s", (_, extra) => {
    const toggleSidebar = vi.fn();
    mount({ toggleSidebar });

    const e = press(document.body, { ...ctrlB, ...extra });

    expect(toggleSidebar).not.toHaveBeenCalled();
    expect(e.defaultPrevented).toBe(false);
  });

  it("ignores an event something else already claimed", () => {
    const claim = (e: Event) => e.preventDefault();
    window.addEventListener("keydown", claim, true);
    cleanups.push(() => window.removeEventListener("keydown", claim, true));
    const toggleSidebar = vi.fn();
    mount({ toggleSidebar });

    press(document.body, ctrlB);

    expect(toggleSidebar).not.toHaveBeenCalled();
  });

  it("takes a mod chord ahead of the focused field's own handler", () => {
    const textarea = add("textarea");
    const own = vi.fn();
    textarea.addEventListener("keydown", own);
    textarea.focus();
    const toggleSidebar = vi.fn();
    mount({ toggleSidebar });

    press(textarea, ctrlB);

    expect(toggleSidebar).toHaveBeenCalledOnce();
    expect(own).not.toHaveBeenCalled();
  });

  it("leaves Escape to an inner handler that claims it", () => {
    const composer = add("textarea", { "data-composer-input": "" });
    const claim = vi.fn((e: KeyboardEvent) => e.preventDefault());
    composer.addEventListener("keydown", claim);
    composer.focus();
    const stopTurnEscape = vi.fn();
    mount({ stopTurnEscape });

    press(composer, escape);

    expect(claim).toHaveBeenCalledOnce();
    expect(stopTurnEscape).not.toHaveBeenCalled();
  });

  it("stops the turn on Escape from the composer, and only the composer", () => {
    const composer = add("textarea", { "data-composer-input": "" });
    const other = add("textarea");
    const stopTurnEscape = vi.fn();
    mount({ stopTurnEscape });

    other.focus();
    press(other, escape);
    expect(stopTurnEscape).not.toHaveBeenCalled();

    composer.focus();
    const e = press(composer, escape);
    expect(stopTurnEscape).toHaveBeenCalledOnce();
    expect(e.defaultPrevented).toBe(true);
  });

  it("does not stop the turn on Escape when none is running", () => {
    const composer = add("textarea", { "data-composer-input": "" });
    composer.focus();
    const stopTurnEscape = vi.fn();
    mount({ stopTurnEscape }, { turnRunning: false });

    press(composer, escape);

    expect(stopTurnEscape).not.toHaveBeenCalled();
  });

  it.each([
    ["a dialog", { role: "dialog" }],
    ["an alert dialog", { role: "alertdialog" }],
    ["a menu", { role: "menu" }],
    ["a select's listbox", { role: "listbox" }],
  ])("blocks ordinary commands while %s is open", (_, attrs) => {
    add("div", attrs);
    const toggleSidebar = vi.fn();
    mount({ toggleSidebar });

    const e = press(document.body, ctrlB);

    expect(toggleSidebar).not.toHaveBeenCalled();
    expect(e.defaultPrevented).toBe(false);
  });

  it("lets commands through a dialog marked to pass them", () => {
    add("div", { role: "dialog", "data-keybindings": "pass" });
    const toggleSidebar = vi.fn();
    mount({ toggleSidebar });

    press(document.body, ctrlB);

    expect(toggleSidebar).toHaveBeenCalledOnce();
  });

  it("does not count a dialog that is animating closed", () => {
    add("div", { role: "dialog", "data-state": "closed" });
    const toggleSidebar = vi.fn();
    mount({ toggleSidebar });

    press(document.body, ctrlB);

    expect(toggleSidebar).toHaveBeenCalledOnce();
  });

  it("lets ⌘K through the open palette", () => {
    add("div", { role: "dialog" });
    const palette = vi.fn();
    mount({ palette }, { paletteOpen: true });

    press(document.body, { key: "k", code: "KeyK", ctrlKey: true });

    expect(palette).toHaveBeenCalledOnce();
  });

  it("sees the model picker open from its trigger", () => {
    add("div", { role: "dialog" });
    add("button", { "aria-label": "Harness and model", "aria-expanded": "true" });
    const effort = vi.fn();
    mount({ effort });

    press(document.body, { key: "E", code: "KeyE", ctrlKey: true, shiftKey: true });

    expect(effort).toHaveBeenCalledOnce();
  });

  describe("Ctrl+F", () => {
    const ctrlF: KeyboardEventInit = { key: "f", code: "KeyF", ctrlKey: true };
    const handlers = () => ({ findInThread: vi.fn(), findInDiff: vi.fn() });

    it("finds in the thread from the composer", () => {
      const composer = add("textarea", { "data-composer-input": "" });
      composer.focus();
      const h = handlers();
      mount(h);

      const e = press(composer, ctrlF);

      expect(h.findInThread).toHaveBeenCalledOnce();
      expect(h.findInDiff).not.toHaveBeenCalled();
      expect(e.defaultPrevented).toBe(true);
    });

    it("finds in the diff from anywhere inside the diff surface", () => {
      const surface = add("div", { "data-diff-surface": "" });
      const button = document.createElement("button");
      surface.append(button);
      button.focus();
      const h = handlers();
      mount(h);

      press(button, ctrlF);

      expect(h.findInDiff).toHaveBeenCalledOnce();
      expect(h.findInThread).not.toHaveBeenCalled();
    });

    it("finds in the diff from the phone's panel sheet, which holds focus itself", () => {
      const sheet = add("div", { role: "dialog", tabindex: "-1" });
      const surface = document.createElement("div");
      surface.setAttribute("data-diff-surface", "");
      sheet.append(surface);
      sheet.focus();
      const h = handlers();
      mount(h);

      press(sheet, ctrlF);

      expect(h.findInDiff).toHaveBeenCalledOnce();
      expect(h.findInThread).not.toHaveBeenCalled();
    });

    it("leaves the terminal's Ctrl+F to the browser", () => {
      const term = add("div", { "data-terminal": "" });
      const xterm = document.createElement("div");
      xterm.className = "xterm";
      const input = document.createElement("textarea");
      input.className = "xterm-helper-textarea";
      xterm.append(input);
      term.append(xterm);
      input.focus();
      const h = handlers();
      mount(h);

      const e = press(input, ctrlF);

      expect(h.findInThread).not.toHaveBeenCalled();
      expect(h.findInDiff).not.toHaveBeenCalled();
      expect(e.defaultPrevented).toBe(false);
    });
  });

  it("leaves a command with no handler alone", () => {
    const textarea = add("textarea");
    const own = vi.fn();
    textarea.addEventListener("keydown", own);
    textarea.focus();
    mount({});

    const e = press(textarea, ctrlB);

    expect(own).toHaveBeenCalledOnce();
    expect(e.defaultPrevented).toBe(false);
  });

  it("calls the newest handler after a re-render", () => {
    const first = vi.fn();
    const second = vi.fn();
    const { rerender } = renderHook(({ h }) => useKeybindings({ toggleSidebar: h }, APP), {
      initialProps: { h: first },
    });
    rerender({ h: second });

    press(document.body, ctrlB);

    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledOnce();
  });

  it("stops listening on unmount", () => {
    const toggleSidebar = vi.fn();
    const { unmount } = mount({ toggleSidebar });
    unmount();

    press(document.body, ctrlB);

    expect(toggleSidebar).not.toHaveBeenCalled();
  });
});
