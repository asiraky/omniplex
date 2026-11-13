// @vitest-environment jsdom
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Markdown } from "~/components/Markdown";

import { findRanges } from "./findReveal";

function root(html: string) {
  const el = document.createElement("div");
  el.innerHTML = html;
  return el;
}

const texts = (ranges: Range[]) => ranges.map((r) => r.toString());

describe("findRanges", () => {
  it("finds every occurrence, whatever its case", () => {
    const ranges = findRanges(root("<p>Deploy the deploy, then DEPLOY again</p>"), "deploy");
    expect(texts(ranges)).toEqual(["Deploy", "deploy", "DEPLOY"]);
  });

  it("matches a query typed in capitals against lower-case text", () => {
    expect(texts(findRanges(root("<p>a flaky link</p>"), "FLAKY"))).toEqual(["flaky"]);
  });

  it("finds a match that runs across inline formatting", () => {
    const el = root("<p>run <strong>go</strong> test now</p>");
    const [range, ...rest] = findRanges(el, "go test");
    expect(rest).toEqual([]);
    expect(range.toString()).toBe("go test");
    expect(range.startContainer.textContent).toBe("go");
    expect(range.endContainer.textContent).toBe(" test now");
  });

  it("does not overlap occurrences", () => {
    expect(findRanges(root("<p>aaaa</p>"), "aa")).toHaveLength(2);
  });

  it("finds nothing for an empty query or absent text", () => {
    expect(findRanges(root("<p>hello</p>"), "")).toEqual([]);
    expect(findRanges(root("<p>hello</p>"), "bye")).toEqual([]);
  });

  it("keeps offsets right after a character whose lower case is longer", () => {
    // İ lower-cases to two code units; folding it would shift every offset after it.
    const ranges = findRanges(root("<p>İstanbul is far</p>"), "far");
    expect(texts(ranges)).toEqual(["far"]);
  });

  it("skips a code block's header and copy button but searches its code", () => {
    const { container } = render(<Markdown text={"```bash\necho bash\n```"} />);
    const ranges = findRanges(container, "bash");
    // The language label says "bash" too; only the code's occurrence counts.
    expect(texts(ranges)).toEqual(["bash"]);
    expect(ranges[0].startContainer.parentElement?.closest("pre")).not.toBeNull();
  });

  it("skips controls and hidden glyphs", () => {
    const el = root('<button>match</button><span aria-hidden="true">match</span><p>match</p>');
    const ranges = findRanges(el, "match");
    expect(ranges).toHaveLength(1);
    expect(ranges[0].startContainer.parentElement?.tagName).toBe("P");
  });

  it("still searches a path drawn as a button", () => {
    const el = root('<p>see <button data-speak-as="code">web/src/app.ts</button></p>');
    expect(texts(findRanges(el, "app.ts"))).toEqual(["app.ts"]);
  });
});
