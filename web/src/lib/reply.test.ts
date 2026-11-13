// @vitest-environment jsdom
import { describe, expect, it } from "vitest";

import { quoteInto, renderedText } from "./reply";

describe("quoteInto", () => {
  it("quotes into an empty draft, leaving a blank line to answer on", () => {
    expect(quoteInto("", "Use a mutex.")).toBe("> Use a mutex.\n\n");
  });

  it("keeps what was typed, a paragraph ahead of the quote", () => {
    expect(quoteInto("About this:  \n", "Use a mutex.")).toBe("About this:\n\n> Use a mutex.\n\n");
  });

  it("builds up when a second passage is quoted", () => {
    const once = quoteInto("", "First point.");
    expect(quoteInto(`${once}Agreed.`, "Second point.")).toBe(
      "> First point.\n\nAgreed.\n\n> Second point.\n\n",
    );
  });

  it("keeps a multi-paragraph quote one quote", () => {
    expect(quoteInto("", "\nOne.\r\n\r\n   \nTwo.  \n- three\n")).toBe(
      "> One.\n>\n>\n> Two.\n> - three\n\n",
    );
  });

  it("leaves the draft alone when there is nothing to quote", () => {
    expect(quoteInto("Half typed", " \n\t")).toBe("Half typed");
  });
});

/** Renders `html` and selects from the first match of `from` to the end of `to`. */
function selection(html: string, from: string, to: string): Range {
  document.body.innerHTML = html;
  const texts: Text[] = [];
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) texts.push(n as Text);
  const start = texts.find((t) => t.data.includes(from))!;
  const end = texts.findLast((t) => t.data.includes(to))!;
  const r = document.createRange();
  r.setStart(start, start.data.indexOf(from));
  r.setEnd(end, end.data.indexOf(to) + to.length);
  return r;
}

describe("renderedText", () => {
  it("keeps paragraphs apart, with the inline formatting's text run in", () => {
    const r = selection("<div><p>One <strong>bold</strong> line.</p>\n<p>Two.</p></div>", "bold", "Two");
    expect(renderedText(r)).toBe("bold line.\n\nTwo");
  });

  it("puts list items and table rows on their own lines, cells on one", () => {
    const r = selection(
      "<div><ul>\n<li>alpha</li>\n<li>beta</li>\n</ul><table><tbody><tr><td>k</td><td>v</td></tr><tr><td>k2</td><td>v2</td></tr></tbody></table></div>",
      "alpha",
      "v2",
    );
    expect(renderedText(r)).toBe("- alpha\n- beta\n\nk | v\nk2 | v2");
  });

  it("numbers an ordered list's items from where it starts, once per item", () => {
    const r = selection(
      '<div><ol start="3">\n<li><p>three</p>\n<p>more of three</p></li>\n<li>four</li>\n</ol></div>',
      "three",
      "four",
    );
    expect(renderedText(r)).toBe("3. three\nmore of three\n4. four");
  });

  it("fences a code block and leaves its header out", () => {
    const r = selection(
      '<div><p>Run it:</p><div data-speak-as="code-block"><div><span>go</span><button>copy</button></div><pre><code>go test\n./...\n</code></pre></div><p>Then.</p></div>',
      "Run",
      "Then",
    );
    expect(renderedText(r)).toBe("Run it:\n\n```\ngo test\n./...\n```\n\nThen");
  });

  it("is the slice of one text node when that is all that is selected", () => {
    const r = selection("<p>Use a mutex here.</p>", "a mutex", "a mutex");
    expect(renderedText(r)).toBe("a mutex");
  });
});
