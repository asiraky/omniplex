/**
 * Whether the caret is on the textarea's top or bottom line, so ArrowUp or
 * ArrowDown can step through history there and move the caret everywhere
 * else. Lines are counted as drawn: a long paragraph wrapped over three lines
 * has three, not one. A selection is judged by its start for the top and its
 * end for the bottom.
 */
export function caretOnEdge(el: HTMLTextAreaElement, edge: "top" | "bottom"): boolean {
  const { value } = el;
  const caret = edge === "top" ? el.selectionStart : el.selectionEnd;
  const breakBetween = edge === "top" ? value.slice(0, caret).includes("\n") : value.slice(caret).includes("\n");
  if (breakBetween) return false;
  const parent = el.parentNode;
  if (!parent) return true;

  // Out to the end of the word under the caret: a word that wraps whole
  // takes the caret down a line with it.
  const wordEnd = value.slice(caret).search(/\s/);
  const upToCaret = value.slice(0, wordEnd === -1 ? undefined : caret + wordEnd);

  // Measured on a hidden copy, so the real textarea keeps its value and its
  // undo history. Without layout (a test's DOM) every height is zero, and
  // only the line breaks above decide.
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
    return edge === "top" ? height(upToCaret) <= height("") : height(upToCaret) >= height(value);
  } finally {
    probe.remove();
  }
}
