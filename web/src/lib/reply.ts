import { createContext, useContext } from "react";

/**
 * How an agent's reply is quoted into the attached thread's composer. Provided
 * by the thread view around the transcript; absent (null) wherever there is no
 * composer to write into, which hides every reply button.
 */
export const ReplyContext = createContext<((quote: string) => void) | null>(null);

export function useReply() {
  return useContext(ReplyContext);
}

/**
 * The draft with `text` added as a markdown blockquote and a blank line for
 * the answer. What was already typed stays, ahead of it and a paragraph away,
 * so quoting a second passage builds the reply up rather than replacing it.
 * Blank lines inside the quote keep their `>`: without it the quote would end
 * at the first paragraph break.
 */
export function quoteInto(draft: string, text: string): string {
  const body = text.trim();
  if (!body) return draft;
  const quote = body
    .split(/\r?\n/)
    .map((line) => (line.trim() ? `> ${line.trimEnd()}` : ">"))
    .join("\n");
  const before = draft.trimEnd();
  return `${before ? `${before}\n\n` : ""}${quote}\n\n`;
}

// What a line of rendered markdown sits in. Moving from one to the next is a
// line break; text is otherwise run together, as the reader sees it.
const BLOCKS = "p, li, pre, h1, h2, h3, h4, h5, h6, td, th";
// Chrome, not content: a code block's header (its language and copy button),
// and a diagram's drawing, whose labels out of their boxes say nothing.
const SKIP = '[data-speak-as="code-block"] > :not(pre), [data-speak-as="diagram"] svg';
const FENCE = "```";

/** What goes between the text of two blocks. */
function between(prev: Element, next: Element | null): string {
  const row = prev.closest("tr");
  if (row && row === next?.closest("tr")) return " | ";
  const table = prev.closest("table");
  if (table && table === next?.closest("table")) return "\n";
  return prev.closest("li") && next?.closest("li") ? "\n" : "\n\n";
}

/** A list item's bullet or number, as it starts: not again for its second paragraph. */
function marker(next: Element | null, prev: Element | null): string {
  const item = next?.closest("li");
  if (!item || item === prev?.closest("li")) return "";
  if (item.parentElement?.tagName !== "OL") return "- ";
  const start = Number(item.parentElement.getAttribute("start") ?? 1);
  return `${start + Array.prototype.indexOf.call(item.parentElement.children, item)}. `;
}

/**
 * A selection in a rendered reply, as the text to quote: each paragraph, list
 * item (bulleted or numbered) and table row on its own line, paragraphs a blank line apart, and a
 * code block fenced so it stays code. `Range.toString` would run them all into
 * one line.
 */
export function renderedText(range: Range): string {
  const top = range.commonAncestorContainer;
  const walker = document.createTreeWalker(top, NodeFilter.SHOW_TEXT);
  let out = "";
  let block: Element | null = null;
  const leave = () => {
    if (block?.tagName === "PRE") out = `${out.trimEnd()}\n${FENCE}`;
  };
  let node = top.nodeType === Node.TEXT_NODE ? top : walker.nextNode();
  for (; node; node = walker.nextNode()) {
    const el = node.parentElement;
    if (!range.intersectsNode(node) || el?.closest(SKIP)) continue;
    let text = node.nodeValue ?? "";
    if (node === range.endContainer) text = text.slice(0, range.endOffset);
    if (node === range.startContainer) text = text.slice(range.startOffset);
    const here = el?.closest(BLOCKS) ?? null;
    if (here !== block) {
      // The newlines markdown leaves between blocks are layout, not text.
      if (!text.trim()) continue;
      leave();
      if (block && out) out += between(block, here);
      else if (out) out += "\n\n";
      if (here?.tagName === "PRE") out += `${FENCE}\n`;
      out += marker(here, block);
      block = here;
    }
    out += text;
  }
  leave();
  return out.trim();
}
