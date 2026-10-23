// Rendered message → plain text a text-to-speech voice can read. It works on
// the DOM rather than the markdown source so a whole message and a reader's
// highlighted selection go through the same rules.

/** Marks a message's content element; a selection reads only what is inside one. */
export const SPEAK_ROOT_ATTR = "data-speak-root";

/**
 * Says what a rendered element is when the DOM alone no longer does:
 * "code-block" and "diagram" for fenced blocks (whose header, copy button and
 * render status would otherwise be read), "code" for inline code drawn as
 * something other than `<code>`, like a clickable path chip.
 */
export const SPEAK_AS_ATTR = "data-speak-as";

const CHROME = new Set([
  "BUTTON",
  "SVG",
  "SCRIPT",
  "STYLE",
  "TEMPLATE",
  "NOSCRIPT",
  "INPUT",
  "SELECT",
  "TEXTAREA",
]);

const BLOCKS = new Set([
  "P",
  "H1",
  "H2",
  "H3",
  "H4",
  "H5",
  "H6",
  "BLOCKQUOTE",
  "DIV",
  "SECTION",
  "ARTICLE",
  "HEADER",
  "FOOTER",
  "ASIDE",
  "NAV",
  "MAIN",
  "FIGURE",
  "FIGCAPTION",
  "DETAILS",
  "SUMMARY",
  "DL",
  "DT",
  "DD",
  "UL",
  "OL",
]);

const URL_RE = /\b(?:https?:\/\/|www\.)[^\s<>"'`]+/gi;

function host(url: string): string {
  const trail = /[.,;:!?)\]]+$/.exec(url)?.[0] ?? "";
  const bare = trail ? url.slice(0, -trail.length) : url;
  try {
    const name = new URL(/^www\./i.test(bare) ? `http://${bare}` : bare).hostname;
    return name.replace(/^www\./, "") + trail;
  } catch {
    return url;
  }
}

/** The symbols agents lean on, said the way a person would say them. */
function speakSymbols(text: string): string {
  return text
    .replace(/(\d)\s*×\s*(?=\d)/g, "$1 by ")
    .replace(/-+>|→/g, " to ")
    .replace(/←/g, " from ")
    .replace(/=>|⇒/g, " gives ")
    .replace(/≈/g, " about ")
    .replace(/&/g, " and ")
    .replace(/~\s?(?=[$€£]?\d)/g, "about ");
}

function clean(text: string): string {
  return speakSymbols(text.replace(URL_RE, host)).replace(/\s+/g, " ").trim();
}

// Ending punctuation may sit inside a closing quote or bracket: `"done."`.
function punctuate(text: string): string {
  return /[.!?:;…]["'”’)\]]*$/.test(text) ? text : `${text}.`;
}

/** Inline code, with a path read as just its file name. */
function inlineCode(raw: string): string {
  const text = raw.trim();
  if (!text.includes("/") || /\s/.test(text) || /^(?:https?:\/\/|www\.)/i.test(text)) return raw;
  const base = text.replace(/\/+$/, "").split("/").pop();
  return base || text;
}

function isChrome(el: Element): boolean {
  return (
    CHROME.has(el.tagName.toUpperCase()) ||
    el.getAttribute("aria-hidden") === "true" ||
    el.hasAttribute("hidden")
  );
}

function listNumber(li: Element): number | null {
  const list = li.parentElement;
  if (list?.tagName !== "OL") return null;
  const start = Number.parseInt(list.getAttribute("start") ?? "", 10);
  let n = Number.isNaN(start) ? 1 : start;
  for (let sib = li.previousElementSibling; sib; sib = sib.previousElementSibling)
    if (sib.tagName === "LI") n++;
  return n;
}

class Reader {
  constructor(punctuated = true) {
    this.punctuated = punctuated;
  }
  private readonly punctuated: boolean;
  sentences: string[] = [];
  private buf = "";
  private prefix = "";

  private flush() {
    const text = clean(this.buf);
    this.buf = "";
    if (text) this.say(text);
  }

  private say(text: string) {
    this.sentences.push(this.prefix + (this.punctuated ? punctuate(text) : text));
    this.prefix = "";
  }

  private block(text: string) {
    this.flush();
    this.say(text);
  }

  children(node: Node) {
    for (let c = node.firstChild; c; c = c.nextSibling) this.node(c);
  }

  node(node: Node) {
    if (node.nodeType === Node.TEXT_NODE) {
      // Markdown never leaves a blank line inside a text node, but a plain-text
      // message does: that is a paragraph break, so pause for it.
      const parts = (node.nodeValue ?? "").split(/\n[ \t]*\n/);
      parts.forEach((part, i) => {
        if (i > 0) this.flush();
        this.buf += part;
      });
      return;
    }
    if (node.nodeType === Node.DOCUMENT_FRAGMENT_NODE) {
      this.children(node);
      this.flush();
      return;
    }
    if (node.nodeType !== Node.ELEMENT_NODE) return;
    const el = node as Element;
    const as = el.getAttribute(SPEAK_AS_ATTR);
    if (as === "code-block") return this.block("Code snippet");
    if (as === "diagram") return this.block("Diagram");
    if (as === "code") {
      this.buf += inlineCode(el.textContent ?? "");
      return;
    }
    if (isChrome(el)) return;
    const tag = el.tagName.toUpperCase();
    switch (tag) {
      case "PRE":
        return this.block(el.querySelector("code.language-mermaid") ? "Diagram" : "Code snippet");
      case "CODE":
        this.buf += inlineCode(el.textContent ?? "");
        return;
      case "BR":
        this.buf += " ";
        return;
      case "IMG": {
        const alt = el.getAttribute("alt")?.trim();
        if (alt) this.buf += ` ${alt} `;
        return;
      }
      case "HR":
        return this.flush();
      case "TABLE":
        this.flush();
        for (const s of readTable(el)) this.say(s);
        return;
      case "TR":
        // A row without its table — only reachable from a hand-made fragment.
        this.flush();
        this.buf = cellTexts(el).filter(Boolean).join(", ");
        return this.flush();
      case "TD":
      case "TH":
        this.buf += ` ${inlineText(el)} `;
        return;
      case "LI": {
        this.flush();
        const n = listNumber(el);
        this.prefix = n === null ? "" : `${n}. `;
        this.children(el);
        this.flush();
        this.prefix = "";
        return;
      }
    }
    if (BLOCKS.has(tag)) {
      this.flush();
      this.children(el);
      this.flush();
      return;
    }
    this.children(el);
  }

  finish(): string[] {
    this.flush();
    return this.sentences;
  }
}

/** A cell's content as one phrase, without sentence punctuation. */
function inlineText(el: Element): string {
  const r = new Reader(false);
  r.children(el);
  return r.finish().join(", ");
}

function rowsOf(table: Element): Element[] {
  return Array.from(table.querySelectorAll("tr")).filter((tr) => tr.closest("table") === table);
}

function cellTexts(tr: Element): string[] {
  return Array.from(tr.children)
    .filter((c) => c.tagName === "TD" || c.tagName === "TH")
    .map(inlineText);
}

function headerRow(table: Element): Element | null {
  const rows = rowsOf(table);
  const inHead = rows.find((tr) => tr.parentElement?.tagName === "THEAD");
  if (inHead) return inHead;
  const first = rows[0];
  const cells = first ? Array.from(first.children) : [];
  return cells.length > 0 && cells.every((c) => c.tagName === "TH") ? first : null;
}

/**
 * The first column names each row and every other cell is read after its
 * column's header: "Flash: Price $0.04, Latency 75 ms".
 */
function readTable(table: Element): string[] {
  const head = headerRow(table);
  const headers = head ? cellTexts(head) : [];
  const body = rowsOf(table)
    .filter((tr) => tr !== head)
    .map(cellTexts);
  const width = Math.max(headers.length, ...body.map((r) => r.length));

  if (width === 1) return [...headers, ...body.flat()].filter(Boolean);
  if (!headers.some(Boolean)) return body.map((r) => r.filter(Boolean).join(", ")).filter(Boolean);
  if (body.length === 0) return [headers.filter(Boolean).join(", ")];

  return body
    .map(([label, ...rest]) => {
      const values = rest
        .map((v, i) => (v ? (headers[i + 1] ? `${headers[i + 1]} ${v}` : v) : ""))
        .filter(Boolean)
        .join(", ");
      if (label && values) return `${label}: ${values}`;
      return label || values;
    })
    .filter(Boolean);
}

/** Rendered message content (an element or fragment) as text to read aloud. */
export function speakableText(root: Node): string {
  const r = new Reader();
  r.node(root);
  return r.finish().join("\n");
}

const TABLE_PARTS = new Set(["TR", "TBODY", "THEAD", "TFOOT", "TABLE"]);

function asElement(node: Node): Element | null {
  return node.nodeType === Node.ELEMENT_NODE ? (node as Element) : node.parentElement;
}

/** How many of `parent`'s children named `tag` come before the selection. */
function skipped(range: Range, parent: Element, tags: string[]): number {
  let n = 0;
  for (const c of Array.from(parent.children)) {
    if (!tags.includes(c.tagName)) continue;
    if (range.intersectsNode(c)) break;
    n++;
  }
  return n;
}

/**
 * A cloned selection loses whatever it starts after: the list's earlier items,
 * a table's header row, a row's earlier cells. Put back what the reading
 * depends on — the number the first item really has, the column headers, and
 * blank cells so the selected ones still line up under their headers.
 */
function restore(range: Range, live: Element, clone: Element) {
  const doc = clone.ownerDocument;
  switch (live.tagName) {
    case "OL": {
      const start = Number.parseInt(live.getAttribute("start") ?? "", 10);
      clone.setAttribute("start", String((Number.isNaN(start) ? 1 : start) + skipped(range, live, ["LI"])));
      return;
    }
    case "TR": {
      const pad = skipped(range, live, ["TD", "TH"]);
      for (let i = 0; i < pad; i++) clone.insertBefore(doc.createElement("td"), clone.firstChild);
      return;
    }
    case "TABLE": {
      const head = headerRow(live);
      if (!head) return;
      const cloned = headerRow(clone);
      cloned?.remove();
      const thead = doc.createElement("thead");
      thead.append(head.cloneNode(true));
      clone.insertBefore(thead, clone.firstChild);
      return;
    }
  }
}

function restoreContext(range: Range, fragment: DocumentFragment): Node {
  const common = range.commonAncestorContainer;
  const pairs: [Element, Element][] = [];

  // The partly selected ancestors of the start are cloned shallowly, each the
  // first child of the one above it: walk the live chain and the clones together.
  const chain: Element[] = [];
  for (let n: Node | null = range.startContainer; n && n !== common; n = n.parentNode)
    if (n.nodeType === Node.ELEMENT_NODE) chain.unshift(n as Element);
  let clone = fragment.firstChild;
  for (const live of chain) {
    if (!clone || clone.nodeType !== Node.ELEMENT_NODE || (clone as Element).tagName !== live.tagName) break;
    pairs.push([live, clone as Element]);
    clone = clone.firstChild;
  }

  // The selection's own parent is not in the fragment at all. Rebuild it when
  // it carries meaning: an ordered list, or a table the selection sits in
  // without being inside a single cell.
  let out: Node = fragment;
  if (common.nodeType === Node.ELEMENT_NODE) {
    const wrap: Element[] = [];
    const el = common as Element;
    if (el.tagName === "OL") wrap.push(el);
    else
      for (let p: Element | null = el; p && TABLE_PARTS.has(p.tagName); p = p.parentElement) {
        wrap.push(p);
        if (p.tagName === "TABLE") break;
      }
    for (const live of wrap) {
      const wrapper = live.cloneNode(false) as Element;
      wrapper.append(out);
      pairs.push([live, wrapper]);
      out = wrapper;
    }
  }

  for (const [live, c] of pairs) restore(range, live, c);
  return out;
}

/**
 * A reader's selection as text to read aloud. Only what lies inside a
 * `[data-speak-root]` element is read, so timestamps, footer buttons and tool
 * cards caught between messages are skipped.
 */
export function speakableSelection(range: Range): string {
  if (range.collapsed) return "";
  const commonEl = asElement(range.commonAncestorContainer);
  const inRoot = commonEl?.closest(`[${SPEAK_ROOT_ATTR}]`) ?? null;

  // Selecting inside a code block is asking for the code itself.
  if (inRoot && commonEl?.closest("pre")) return range.toString().replace(/\s+/g, " ").trim();

  const content = restoreContext(range, range.cloneContents());
  if (inRoot) return speakableText(content);

  const roots = Array.from((content as Element | DocumentFragment).querySelectorAll(`[${SPEAK_ROOT_ATTR}]`));
  if (content.nodeType === Node.ELEMENT_NODE && (content as Element).hasAttribute(SPEAK_ROOT_ATTR))
    roots.unshift(content as Element);
  return roots
    .filter((el) => !el.parentElement?.closest(`[${SPEAK_ROOT_ATTR}]`))
    .map(speakableText)
    .filter(Boolean)
    .join("\n");
}
