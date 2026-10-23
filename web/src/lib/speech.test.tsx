// @vitest-environment jsdom
import { act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Markdown } from "~/components/Markdown";
import { renderMermaid } from "~/lib/mermaid";
import { OpenPathContext } from "~/lib/openPath";
import { render } from "~/test/harness";

import { speakableSelection, speakableText } from "./speech";
import { SPEAK_ROOT_ATTR } from "./speechAttrs";

vi.mock("~/lib/mermaid", () => ({ renderMermaid: vi.fn() }));
const renderer = vi.mocked(renderMermaid);

beforeEach(() => {
  vi.useFakeTimers();
  renderer.mockReset().mockResolvedValue("data:image/svg+xml,test");
});
afterEach(() => {
  vi.useRealTimers();
});

async function settle() {
  await act(async () => {
    await vi.dynamicImportSettled();
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(250);
  });
}

function speak(md: string): string {
  const { container } = render(<Markdown text={md} />);
  return speakableText(container);
}

/** The text node holding `text`, and the offset `text` starts at within it. */
function at(root: Node, text: string): [Text, number] {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const i = (n.nodeValue ?? "").indexOf(text);
    if (i >= 0) return [n as Text, i];
  }
  throw new Error(`no text ${JSON.stringify(text)}`);
}

/** A range from the start of `from` to the end of `to`. */
function select(root: Node, from: string, to: string): Range {
  const range = document.createRange();
  const [a, i] = at(root, from);
  const [b, j] = at(root, to);
  range.setStart(a, i);
  range.setEnd(b, j + to.length);
  return range;
}

describe("speakableText", () => {
  it("reads each block as its own sentence and drops inline formatting", () => {
    expect(
      speak("# Title\n\nSome **bold**, *slanted* and ~~old~~ text\nnext line\n\n> quoted!\n\nAsk: why?"),
    ).toBe("Title.\nSome bold, slanted and old text next line.\nquoted!\nAsk: why?");
  });

  it("keeps punctuation that sits inside a closing quote", () => {
    expect(speak('He said "done."')).toBe('He said "done."');
  });

  it("reads a fenced code block as a placeholder, without its language or copy button", () => {
    expect(speak("Run this:\n\n```js\nconst x = 1;\n```\n\nDone")).toBe(
      "Run this:\nCode snippet.\nDone.",
    );
  });

  it("reads a Mermaid diagram as a placeholder while rendering and once rendered", async () => {
    const { container } = render(<Markdown text={"See\n\n```mermaid\nflowchart LR\nA --> B\n```"} />);
    expect(speakableText(container)).toBe("See.\nDiagram.");
    await settle();
    expect(container.querySelector("img")).toBeTruthy();
    expect(speakableText(container)).toBe("See.\nDiagram.");
  });

  it("reads inline code, and a path as just its file name", () => {
    expect(speak("Run `npm test` in `internal/server/server.go` or `web/src/`")).toBe(
      "Run npm test in server.go or src.",
    );
    expect(speak("See `~/.omniplex/config.json` and `./scripts/dev-link`")).toBe(
      "See config.json and dev-link.",
    );
    expect(speak("At `internal/server/tts.go:172` in `cmd/omniplex/Makefile`")).toBe(
      "At tts.go:172 in Makefile.",
    );
  });

  it("reads slashed code that is not a path as written", () => {
    expect(speak("Rebase on `origin/main`, `and/or` open `https://x.io/a.md`")).toBe(
      "Rebase on origin/main, and/or open x.io.",
    );
  });

  it("reads a clickable path chip as its file name", () => {
    const { container } = render(
      <OpenPathContext.Provider value={() => {}}>
        <Markdown text="Edit `web/src/lib/speech.ts` now" />
      </OpenPathContext.Provider>,
    );
    expect(container.querySelector("button")).toBeTruthy();
    expect(speakableText(container)).toBe("Edit speech.ts now.");
  });

  it("drops bullets and flattens nested lists in reading order", () => {
    expect(speak("- one\n- two\n  - two a\n  - two b\n- three")).toBe(
      "one.\ntwo.\ntwo a.\ntwo b.\nthree.",
    );
  });

  it("numbers ordered items from the list's start", () => {
    expect(speak("3. alpha\n4. beta\n    1. inner\n5. gamma")).toBe(
      "3. alpha.\n4. beta.\n1. inner.\n5. gamma.",
    );
  });

  it("numbers a loose ordered list's paragraphs", () => {
    expect(speak("1. first\n\n2. second")).toBe("1. first.\n2. second.");
  });

  it("reads table rows labelled by their first cell, each value after its header", () => {
    expect(
      speak(
        "| Model | Price | Latency |\n|---|---|---|\n| Flash | $0.04 | 75 ms |\n| Turbo | | 250 ms |\n| | $1 | |",
      ),
    ).toBe("Flash: Price $0.04, Latency 75 ms.\nTurbo: Latency 250 ms.\nPrice $1.");
  });

  it("reads a one-column table's cells as sentences", () => {
    expect(speak("| Steps |\n|---|\n| Build |\n| Ship it! |")).toBe("Steps.\nBuild.\nShip it!");
  });

  it("joins cells with commas when the header row is empty", () => {
    expect(speak("| | |\n|---|---|\n| a | b |\n| c | |")).toBe("a, b.\nc.");
  });

  it("reads link text, and a URL as its host", () => {
    expect(
      speak(
        "[the docs](https://x.com/a) at https://github.com/pingdotgg/t3code, or [https://www.example.com/a](https://www.example.com/a).",
      ),
    ).toBe("the docs at github.com, or example.com.");
  });

  it("reads an image's alt text", () => {
    expect(speak("Look ![a cat](https://x.test/cat.png) here")).toBe("Look a cat here.");
  });

  it("says common agent symbols as words and leaves emoji alone", () => {
    expect(speak("A → B -> C ← D => E ≈ 5 & 1920×1080 in ~5 ms 🚀")).toBe(
      "A to B to C from D gives E about 5 and 1920 by 1080 in about 5 ms 🚀.",
    );
  });

  it("pauses at blank lines in plain text but joins single line breaks", () => {
    const div = document.createElement("div");
    div.textContent = "First line\nsecond\n\nNew paragraph";
    expect(speakableText(div)).toBe("First line second.\nNew paragraph.");
  });
});

describe("speakableSelection", () => {
  function messages(...texts: string[]) {
    return render(
      <div>
        {texts.map((t, i) => (
          <div key={i}>
            <span>{`10:4${i} AM`}</span>
            <div {...{ [SPEAK_ROOT_ATTR]: "" }}>
              <Markdown text={t} />
            </div>
            <footer>
              <button type="button">Copy</button>
              <span>Tool call: read file</span>
            </footer>
          </div>
        ))}
      </div>,
    ).container;
  }

  it("reads code literally when the selection is inside a code block", () => {
    const root = messages("Intro\n\n```go\nfunc main() {\n\treturn\n}\n```");
    expect(speakableSelection(select(root, "main()", "return"))).toBe("main() { return");
  });

  it("reads a selection over a code block as a placeholder", () => {
    const root = messages("Intro\n\n```go\nfunc main() {}\n```\n\nAfter");
    expect(speakableSelection(select(root, "Intro", "After"))).toBe("Intro.\nCode snippet.\nAfter.");
  });

  it("keeps column headers for rows selected without the header row", () => {
    const root = messages(
      "| Model | Price | Latency |\n|---|---|---|\n| Flash | $0.04 | 75 ms |\n| Turbo | $0.08 | 250 ms |\n| Pro | $0.30 | 400 ms |",
    );
    expect(speakableSelection(select(root, "$0.08", "Pro"))).toBe(
      "Price $0.08, Latency 250 ms.\nPro.",
    );
  });

  it("keeps column headers when a selection starts in a table and runs past it", () => {
    const root = messages("| Model | Price |\n|---|---|\n| Flash | $0.04 |\n| Turbo | $0.08 |\n\nThat is all");
    expect(speakableSelection(select(root, "Turbo", "That"))).toBe("Turbo: Price $0.08.\nThat.");
  });

  it("keeps an ordered item's number when the selection starts mid-list", () => {
    const root = messages("1. alpha\n2. beta\n3. gamma");
    expect(speakableSelection(select(root, "eta", "gam"))).toBe("2. eta.\n3. gam.");
  });

  it("just reads a selection inside a single item", () => {
    const root = messages("## A heading here\n\n1. alpha beta");
    expect(speakableSelection(select(root, "heading", "here"))).toBe("heading here.");
    expect(speakableSelection(select(root, "alpha", "beta"))).toBe("alpha beta.");
  });

  it("reads only message content when a selection spans messages and the chrome between", () => {
    const root = messages("First message ends here", "Second `a/b.go` starts here");
    expect(speakableSelection(select(root, "ends", "Second"))).toBe("ends here.\nSecond.");
    expect(speakableSelection(select(root, "10:40", "starts"))).toBe(
      "First message ends here.\nSecond b.go starts.",
    );
  });

  it("reads nothing for a selection outside every message", () => {
    const root = messages("Hello");
    expect(speakableSelection(select(root, "Copy", "Tool call"))).toBe("");
  });
});
