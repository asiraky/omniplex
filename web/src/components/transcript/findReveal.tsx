import { createContext, useContext, useState } from "react";

// Find in thread, on the transcript's side: which item the find bar has
// jumped to, so whatever is hiding it (a turn's fold, a run, a plan's steps,
// a long prompt's clamp) opens, and the occurrences of the query inside it,
// so they can be painted without touching the DOM Markdown rendered.

/** The item the find bar last jumped to. A fresh object on every jump, so a
    second jump to the same item still counts as a new ask to show it. */
export type Reveal = { id: string };

const RevealContext = createContext<Reveal | null>(null);
export const FindRevealProvider = RevealContext.Provider;

/**
 * `useState(false)` for a disclosure that also opens itself whenever a find
 * jump lands on something it holds. It opens during the render the jump
 * arrives in, not an effect after it, so by the time the transcript's layout
 * effect looks for the item it is already in the DOM. Each jump opens it once:
 * the reader can close it again, and only the next jump reopens it.
 */
export function useRevealOpen(holds: (id: string) => boolean) {
  const reveal = useContext(RevealContext);
  const [open, setOpen] = useState(false);
  const [handled, setHandled] = useState<Reveal | null>(null);
  if (reveal && reveal !== handled && holds(reveal.id)) {
    setHandled(reveal);
    setOpen(true);
  }
  return [open, setOpen] as const;
}

/** Marks the element whose text find highlights inside an item's root, when
    the root carries more than the item's words (a footer, attachments). */
export const FIND_TEXT_ATTR = "data-find-text";

// Text that is chrome, not content: a code block's language label and copy
// button, a diagram's header, a control's label, an icon or glyph.
const SKIP = 'button:not([data-speak-as]), [aria-hidden="true"], svg, script, style';
const CODE = '[data-speak-as="code-block"], [data-speak-as="diagram"]';

// Lower-cases one code unit at a time, leaving any that would change length
// (İ becomes two) as they are, so an offset into the folded text is an offset
// into the original.
function fold(s: string) {
  let out = "";
  for (const c of s) {
    const l = c.toLowerCase();
    out += l.length === c.length ? l : c;
  }
  return out;
}

/**
 * Every case-insensitive occurrence of `query` in the text under `root`, as
 * DOM ranges. The text nodes are read as one string, so a match that runs
 * across formatting (`foo **bar**`) is still found; chrome is left out (see
 * SKIP), and inside a code block or diagram only the code itself counts.
 */
export function findRanges(root: Node, query: string): Range[] {
  const needle = fold(query);
  if (!needle) return [];
  const doc = root.ownerDocument ?? (root as Document);
  const nodes: Text[] = [];
  const starts: number[] = [];
  let text = "";
  const walker = doc.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      if (node.nodeType === Node.ELEMENT_NODE)
        return (node as Element).matches(SKIP) ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_SKIP;
      const parent = node.parentElement;
      const block = parent?.closest(CODE);
      if (block && (block === root || root.contains(block)) && !parent?.closest("pre"))
        return NodeFilter.FILTER_SKIP;
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    nodes.push(n as Text);
    starts.push(text.length);
    text += (n as Text).data;
  }
  const hay = fold(text);

  // The text node holding offset `at`, and the offset within it. A match
  // ending exactly at a node's end resolves to that node, not the next.
  let cursor = 0;
  const locate = (at: number, end: boolean): [Text, number] => {
    while (
      cursor + 1 < nodes.length &&
      (end ? starts[cursor + 1] < at : starts[cursor + 1] <= at)
    )
      cursor++;
    return [nodes[cursor], at - starts[cursor]];
  };

  const ranges: Range[] = [];
  for (let at = hay.indexOf(needle); at !== -1; at = hay.indexOf(needle, at + needle.length)) {
    const range = doc.createRange();
    range.setStart(...locate(at, false));
    range.setEnd(...locate(at + needle.length, true));
    ranges.push(range);
  }
  return ranges;
}

/** The name `::highlight()` styles in index.css. */
export const FIND_HIGHLIGHT = "thread-find";

const canHighlight = () =>
  typeof CSS !== "undefined" && !!CSS.highlights && typeof Highlight !== "undefined";

/**
 * Paints the query's occurrences inside `el`, and returns them. `painted` is
 * false when nothing could be: no Custom Highlight API, or no occurrence in
 * the rendered text (the server matched raw Markdown, which is not always
 * what is drawn).
 */
export function highlightIn(el: Element, query: string): { ranges: Range[]; painted: boolean } {
  const root = el.querySelector(`[${FIND_TEXT_ATTR}]`) ?? el;
  const ranges = findRanges(root, query);
  if (!canHighlight()) return { ranges, painted: false };
  if (ranges.length === 0) {
    CSS.highlights.delete(FIND_HIGHLIGHT);
    return { ranges, painted: false };
  }
  CSS.highlights.set(FIND_HIGHLIGHT, new Highlight(...ranges));
  return { ranges, painted: true };
}

/** The element find in thread knows as `id`, under `root`. */
export function findItem(root: ParentNode, id: string): HTMLElement | null {
  const escaped =
    typeof CSS !== "undefined" && CSS.escape ? CSS.escape(id) : id.replace(/["\\]/g, "\\$&");
  return root.querySelector<HTMLElement>(`[data-item-id="${escaped}"]`);
}

export function clearHighlight() {
  if (typeof CSS !== "undefined" && CSS.highlights) CSS.highlights.delete(FIND_HIGHLIGHT);
}

const FLASH_ATTR = "data-find-flash";

/** The fallback: a brief wash over the whole item. Off and on again, so a
    second jump to the same item plays it again. */
export function flash(el: HTMLElement) {
  el.removeAttribute(FLASH_ATTR);
  void el.offsetWidth;
  el.setAttribute(FLASH_ATTR, "");
  // Its own animation's end, not a row inside it finishing its fade-in.
  const done = (e: AnimationEvent) => {
    if (e.target !== el) return;
    el.removeAttribute(FLASH_ATTR);
    el.removeEventListener("animationend", done);
  };
  el.addEventListener("animationend", done);
}
