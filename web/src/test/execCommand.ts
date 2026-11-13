import { afterEach, vi } from "vitest";

/**
 * jsdom has no `document.execCommand`. These stand in for a browser that does,
 * and one that does not, and put back whatever was there after each test.
 */
export function stubExecCommand() {
  const original = document.execCommand;
  afterEach(() => {
    document.execCommand = original;
  });
  return {
    /** Edits the focused textarea's selection the way a browser does, and
        fires the textarea's own input event. */
    editing() {
      const exec = vi.fn((command: string, _ui?: boolean, text?: string) => {
        const el = document.activeElement;
        if (!(el instanceof HTMLTextAreaElement)) return false;
        el.setRangeText(command === "insertText" ? (text ?? "") : "", el.selectionStart, el.selectionEnd, "end");
        el.dispatchEvent(new InputEvent("input", { bubbles: true }));
        return true;
      });
      document.execCommand = exec;
      return exec;
    },
    absent() {
      // @ts-expect-error: a browser without it
      document.execCommand = undefined;
    },
  };
}
