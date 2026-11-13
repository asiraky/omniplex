/**
 * Whether the caret is on the textarea's top or bottom line as drawn, counting
 * the lines a long paragraph wraps onto and not just its line breaks. Measured
 * on a hidden copy of the textarea, so the real one keeps its value and its
 * undo history. Without layout (a test's DOM) every height is zero and this
 * says yes.
 */
export function caretOnVisualEdge(el: HTMLTextAreaElement, edge: "top" | "bottom"): boolean {
  const parent = el.parentNode;
  if (!parent) return true;
  const caret = edge === "top" ? el.selectionStart : el.selectionEnd;
  // Out to the end of the word under the caret: a word that wraps whole
  // takes the caret down a line with it.
  const wordEnd = el.value.slice(caret).search(/\s/);
  const upToCaret = el.value.slice(0, wordEnd === -1 ? undefined : caret + wordEnd);

  const probe = el.cloneNode() as HTMLTextAreaElement;
  probe.removeAttribute("id");
  probe.setAttribute("aria-hidden", "true");
  probe.tabIndex = -1;
  Object.assign(probe.style, {
    position: "absolute",
    top: "0",
    left: "0",
    visibility: "hidden",
    width: `${el.offsetWidth}px`,
    height: "0",
    minHeight: "0",
    overflow: "hidden",
  });
  parent.appendChild(probe);
  const height = (text: string) => {
    probe.value = text;
    return probe.scrollHeight;
  };
  try {
    return edge === "top" ? height(upToCaret) <= height("") : height(upToCaret) >= height(el.value);
  } finally {
    probe.remove();
  }
}
