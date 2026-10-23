import { useSyncExternalStore, type RefObject } from "react";

// The reader's current text selection, for read-aloud's "play selection". One
// selectionchange listener serves every message; each message asks only
// whether the selection reaches into it, so dragging a selection re-renders
// the messages it enters and leaves rather than the whole transcript.

let range: Range | null = null;
const listeners = new Set<() => void>();

function read() {
  const sel = document.getSelection();
  // Cloned: the selection's own range is live, and a tap that collapses the
  // selection would collapse a held copy with it.
  range = sel && sel.rangeCount > 0 && !sel.isCollapsed ? sel.getRangeAt(0).cloneRange() : null;
  for (const l of listeners) l();
}

function subscribe(l: () => void) {
  if (listeners.size === 0) {
    document.addEventListener("selectionchange", read);
    read();
  }
  listeners.add(l);
  return () => {
    listeners.delete(l);
    if (listeners.size === 0) {
      document.removeEventListener("selectionchange", read);
      range = null;
    }
  };
}

const noSubscribe = () => () => {};

/** The selection as last seen, or null when nothing is selected. */
export function currentSelection(): Range | null {
  return range;
}

/**
 * Whether `sel` selects any of `root`'s text. Touching is not enough: a
 * triple-clicked paragraph ends at the start of the next message, which the
 * reader never highlighted.
 */
export function selects(sel: Range | null, root: Node | null): boolean {
  if (!sel || !root || !sel.intersectsNode(root)) return false;
  const part = document.createRange();
  part.selectNodeContents(root);
  if (sel.compareBoundaryPoints(Range.START_TO_START, part) > 0) {
    part.setStart(sel.startContainer, sel.startOffset);
  }
  if (sel.compareBoundaryPoints(Range.END_TO_END, part) < 0) {
    part.setEnd(sel.endContainer, sel.endOffset);
  }
  return part.toString().trim() !== "";
}

/** Whether the reader's selection reaches into `root`. Listens only while `enabled`. */
export function useSelects(root: RefObject<Element | null> | undefined, enabled: boolean): boolean {
  return useSyncExternalStore(
    enabled && root ? subscribe : noSubscribe,
    () => enabled && !!root && selects(range, root.current),
    () => false,
  );
}
