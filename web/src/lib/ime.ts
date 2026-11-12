/**
 * Whether a keydown belongs to an IME composition rather than to the page.
 *
 * Typing Japanese, Chinese or Korean goes through an input method: Enter
 * confirms the candidate and Escape cancels it, and neither should send,
 * save or close anything. The browser flags those keys with `isComposing`,
 * but Safari ends the composition before it dispatches the keydown of the key
 * that confirmed it, so that event arrives with `isComposing` already false.
 * Every browser still gives it keyCode 229, the "IME is processing this" code,
 * which is the only sign left.
 *
 * Takes a React keyboard event (whose flag lives on `nativeEvent`) or a native
 * KeyboardEvent, such as the one a Radix `onEscapeKeyDown` hands over.
 */
export function isComposing(e: {
  isComposing?: boolean;
  keyCode?: number;
  nativeEvent?: { isComposing?: boolean };
}): boolean {
  return e.isComposing === true || e.nativeEvent?.isComposing === true || e.keyCode === 229;
}

/**
 * A Radix `onEscapeKeyDown` that keeps its surface open while an IME is
 * composing. Radix listens for Escape on the document, ahead of the field that
 * is composing, so no field's own check can stop an IME's cancel from closing
 * the dialog, sheet or popover around it. Those three wrappers in ui/ pass
 * every caller's handler through this, so no surface has to remember to; the
 * menus and select hold no text field to compose in.
 */
export function escapeUnlessComposing(
  handler?: (e: KeyboardEvent) => void,
): (e: KeyboardEvent) => void {
  return (e) => {
    if (isComposing(e)) {
      e.preventDefault();
      return;
    }
    handler?.(e);
  };
}
