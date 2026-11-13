/**
 * Replaces `[start, end)` of a textarea with `text` the way typing does, so the
 * browser puts the edit on its undo stack. Writing a new `value` from script,
 * which is all a controlled React textarea can do with a new draft, wipes that
 * stack instead: Ctrl/Cmd-Z after it undoes nothing.
 *
 * The edit fires the textarea's own `input` event, so React's `onChange` sees
 * it as it would a keystroke and the draft moves through the usual handler.
 * The textarea is focused first, because the browser only edits the focused
 * one. Returns false when the browser would not make the edit, or made a
 * different one; the caller then writes the value itself, undo or not.
 */
export function typeInto(el: HTMLTextAreaElement, start: number, end: number, text: string): boolean {
  if (typeof document.execCommand !== "function") return false;
  const expected = `${el.value.slice(0, start)}${text}${el.value.slice(end)}`;
  if (expected === el.value) return true;
  el.focus({ preventScroll: true });
  if (document.activeElement !== el) return false;
  el.setSelectionRange(start, end);
  // An empty insertText is not an edit everywhere; delete is, for a selection.
  const done = text ? document.execCommand("insertText", false, text) : document.execCommand("delete");
  return done && el.value === expected;
}
