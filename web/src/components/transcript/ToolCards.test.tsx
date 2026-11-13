// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Item, ToolContent, Turn } from "~/protocol";
import { render, wrap } from "~/test/harness";

import { ToolCard, ToolRun, TurnFold } from "./ToolCards";

function bash(id: string, over: Partial<Item> = {}): Item {
  return { id, kind: "tool", toolKind: "execute", title: `bash ${id}`, status: "in_progress", ...over };
}

const tail = () => screen.queryByRole("group", { name: "Latest output" });

describe("the live tail of a running call", () => {
  it("shows the active call's last lines under a live run, cleaned of escapes", () => {
    const items = [
      bash("1", { status: "completed", content: [{ type: "text", text: "earlier" }] }),
      bash("2", { tail: "\x1b[32mstream line 1\x1b[0m\nprogress 50%\rprogress 100%\n" }),
    ];
    render(<ToolRun threadId="s1" items={items} live />);
    expect(tail()?.textContent).toBe("stream line 1\nprogress 100%");
  });

  it("follows the run's active call, not the first one", () => {
    const items = [bash("1", { tail: "first's output\n" }), bash("2", { tail: "second's output\n" })];
    render(<ToolRun threadId="s1" items={items} live />);
    expect(tail()?.textContent).toBe("second's output");
  });

  it("moves into the card when the run is opened, rather than showing twice", () => {
    render(<ToolRun threadId="s1" items={[bash("1", { tail: "building\n" })]} live />);
    fireEvent.click(screen.getByRole("button", { name: /bash 1/ }));
    expect(screen.getAllByRole("group", { name: "Latest output" })).toHaveLength(1);
    expect(tail()?.textContent).toBe("building");
  });

  it("shows nothing under a run that is no longer live", () => {
    render(<ToolRun threadId="s1" items={[bash("1", { tail: "building\n" })]} live={false} />);
    expect(tail()).toBeNull();
  });

  it("shows on a running card without opening it", () => {
    render(<ToolCard threadId="s1" item={bash("1", { tail: "line a\nline b\n" })} />);
    expect(screen.getByRole("button", { name: /bash 1/ }).getAttribute("aria-expanded")).toBe("false");
    expect(tail()?.textContent).toBe("line a\nline b");
  });

  it("shows nothing for a running call that has printed nothing visible", () => {
    render(<ToolCard threadId="s1" item={bash("1")} />);
    render(<ToolCard threadId="s1" item={bash("2", { tail: "\x1b[0m\n" })} />);
    expect(tail()).toBeNull();
  });

  it("shows nothing for a completed call, which has its output instead", () => {
    const done = bash("1", { status: "completed", content: [{ type: "text", text: "all\nof it" }] });
    render(<ToolCard threadId="s1" item={done} />);
    expect(tail()).toBeNull();
    expect(screen.getByText("2 lines")).toBeTruthy();
  });
});

const preview = "head\n\n[… 9000 characters truncated …]\n\ntail";
const url = "/api/threads/s1/items/c%2F1/content";

function tool(over: Partial<Item> = {}): Item {
  return {
    id: "c/1",
    kind: "tool",
    toolKind: "execute",
    title: "go test ./...",
    status: "completed",
    content: [{ type: "text", text: preview }],
    truncated: true,
    ...over,
  };
}

function serve(...responses: (() => Response)[]) {
  const fetch = vi.fn(async (_input: RequestInfo | URL) => {
    const next = responses.shift();
    return next ? next() : new Response(JSON.stringify({ error: "no more responses" }), { status: 500 });
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

const full = (text: string) => () => new Response(JSON.stringify({ content: [{ type: "text", text }] }));

function openCard(item: Item) {
  render(<ToolCard threadId="s1" item={item} />);
  fireEvent.click(screen.getByRole("button", { name: /go test/ }));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("a truncated tool card", () => {
  it("says it is truncated rather than counting the preview's lines", () => {
    render(<ToolCard threadId="s1" item={tool()} />);
    expect(screen.getByText("truncated")).toBeTruthy();
    expect(screen.queryByText(/lines/)).toBeNull();
  });

  it("loads the full output from the item's content route and shows it in place of the preview", async () => {
    const fetch = serve(full("the whole output"));
    openCard(tool());
    expect(screen.getByText(/characters truncated/)).toBeTruthy();

    fireEvent.click(await screen.findByRole("button", { name: "Load full output" }));
    expect(await screen.findByText("the whole output")).toBeTruthy();
    expect(String(fetch.mock.calls[0][0])).toBe(url);
    expect(screen.queryByText(/characters truncated/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Load full output" })).toBeNull();
  });

  it("shows an image the preview left out once the full output is loaded", async () => {
    const shot = { type: "image", url: "/api/threads/s1/attachments/shot.png", width: 1280, height: 800 };
    serve(() => new Response(JSON.stringify({ content: [{ type: "text", text: "the whole output" }, shot] })));
    openCard(tool());
    expect(screen.queryByRole("img")).toBeNull();

    fireEvent.click(await screen.findByRole("button", { name: "Load full output" }));
    const img = (await screen.findByRole("img")) as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(shot.url);
  });

  it("reports a failed load and loads again on retry", async () => {
    serve(() => new Response(JSON.stringify({ error: "the disk is on fire" }), { status: 500 }), full("second time lucky"));
    openCard(tool());

    fireEvent.click(await screen.findByRole("button", { name: "Load full output" }));
    expect(await screen.findByText("the disk is on fire")).toBeTruthy();
    // The preview stays readable while the full output is out of reach.
    expect(screen.getByText(/characters truncated/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("second time lucky")).toBeTruthy();
  });
});

describe("a tool card whose input was cut", () => {
  it("says it is truncated with no output to count", () => {
    render(<ToolCard threadId="s1" item={tool({ content: undefined, input: { command: "x" } })} />);
    expect(screen.getByText("truncated")).toBeTruthy();
  });

  it("offers the load with no output at all, and shows the full input once loaded", async () => {
    serve(() => new Response(JSON.stringify({ content: [], input: { command: "the whole command" } })));
    openCard(tool({ content: undefined, status: "in_progress", input: { command: "the [… cut …] command" } }));
    expect(screen.getByText(/the \[… cut …\] command/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Load full output" }));
    expect(await screen.findByText(/the whole command/)).toBeTruthy();
    expect(screen.queryByText(/cut …/)).toBeNull();
  });

  it("shows output that lands after the load, and offers the load again", async () => {
    serve(() => new Response(JSON.stringify({ content: [], input: { command: "the whole command" } })));
    const running = tool({ content: undefined, status: "in_progress", input: { command: "the [… cut …] command" } });
    const { rerender } = render(<ToolCard threadId="s1" item={running} />);
    fireEvent.click(screen.getByRole("button", { name: /go test/ }));
    fireEvent.click(screen.getByRole("button", { name: "Load full output" }));
    expect(await screen.findByText(/the whole command/)).toBeTruthy();

    rerender(wrap(<ToolCard threadId="s1" item={{ ...running, status: "completed", content: [{ type: "text", text: preview }] }} />));
    expect(screen.getByText(/characters truncated/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Load full output" })).toBeTruthy();
  });

  it("drops a body asked for before more output landed", async () => {
    let answer!: (r: Response) => void;
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((resolve) => (answer = resolve))));
    const running = tool({ status: "in_progress" });
    const { rerender } = render(<ToolCard threadId="s1" item={running} />);
    fireEvent.click(screen.getByRole("button", { name: /go test/ }));
    fireEvent.click(screen.getByRole("button", { name: "Load full output" }));

    const more = [...running.content!, { type: "text" as const, text: "a later line" }];
    rerender(wrap(<ToolCard threadId="s1" item={{ ...running, content: more }} />));
    await act(async () => answer(full("the output before that line")()));
    expect(screen.queryByText("the output before that line")).toBeNull();
    expect(screen.getByRole("button", { name: "Load full output" })).toBeTruthy();
  });

  it("keeps the preview input when the call recorded no input", async () => {
    serve(full("the whole output"));
    openCard(tool({ input: { command: "kept" } }));
    fireEvent.click(screen.getByRole("button", { name: "Load full output" }));
    expect(await screen.findByText("the whole output")).toBeTruthy();
    expect(screen.getByText(/kept/)).toBeTruthy();
  });
});

describe("an untruncated tool card", () => {
  it("counts its lines and offers no load", () => {
    const fetch = serve();
    render(<ToolCard threadId="s1" item={tool({ truncated: undefined, content: [{ type: "text", text: "ok\nPASS" }] })} />);
    expect(screen.getByText("2 lines")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /go test/ }));
    expect(screen.queryByRole("button", { name: "Load full output" })).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });
});

const SRC = "/api/threads/t1/attachments/a1";

function result(content: ToolContent[], over: Partial<Item> = {}): Item {
  return { id: "tool:1", kind: "tool", title: "browser_screenshot", status: "completed", content, ...over };
}

const screenshot: ToolContent = { type: "image", url: SRC, mediaType: "image/png", width: 1280, height: 800 };

// The text a rendered diff row shows: gutter numbers, the +/- mark, the line.
// The renderer loads in its own chunk, so wait for its first row.
async function diffRows(container: HTMLElement) {
  await waitFor(() => expect(container.querySelector("[data-diff-row]")).not.toBeNull(), { timeout: 5000 });
  return Array.from(container.querySelectorAll("[data-diff-row]")).map((r) => r.textContent ?? "");
}

/** The viewer loads in its own chunk, which a cold, busy test run can take
    well past findBy's one-second default to transform. */
function viewer(name?: string) {
  return screen.findByRole("dialog", { name }, { timeout: 5000 });
}

describe("an image in a tool result", () => {
  it("shows a thumbnail loaded from the url, without opening the card", () => {
    const { container } = render(<ToolCard threadId="t1" item={result([screenshot])} />);
    const img = screen.getByRole("img") as HTMLImageElement;
    expect(img.getAttribute("src")).toBe(SRC);
    expect(container.innerHTML).not.toContain("data:");
  });

  it("reserves the thumbnail's box from the reported size before it loads", () => {
    render(<ToolCard threadId="t1" item={result([screenshot])} />);
    const img = screen.getByRole("img") as HTMLImageElement;
    // 1280x800 held to 96px tall is 153.6px wide.
    expect(parseFloat(img.style.width)).toBeCloseTo(153.6);
    expect(img.style.aspectRatio).toBe("1280 / 800");
  });

  it("crops a full-page screenshot to a readable box rather than a sliver", () => {
    render(<ToolCard threadId="t1" item={result([{ ...screenshot, width: 1280, height: 20000 }])} />);
    const img = screen.getByRole("img") as HTMLImageElement;
    // Held to 1:2, 96px tall is 48px wide.
    expect(parseFloat(img.style.width)).toBeCloseTo(48);
    expect(img.style.aspectRatio).toBe("0.5 / 1");
  });

  it("refuses an image that is not a same-origin path", () => {
    const { container } = render(
      <ToolCard
        threadId="t1"
        item={result([
          { type: "image", url: "data:image/png;base64,iVBORw0KGgo=" },
          { type: "image", url: "//evil.example/x.png" },
        ])}
      />,
    );
    expect(screen.queryByRole("img")).toBeNull();
    expect(container.innerHTML).not.toContain("data:");
  });

  it("opens full size on tap, closes on Escape, and hands focus back to the thumbnail", async () => {
    render(<ToolCard threadId="t1" item={result([screenshot])} />);
    const thumb = screen.getByRole("button", { name: "View browser_screenshot image full size" });
    fireEvent.click(thumb);
    const dialog = await viewer("browser_screenshot image");
    expect(within(dialog).getByRole("img").getAttribute("src")).toBe(SRC);
    expect(within(dialog).getByRole("link", { name: "Open original in a new tab" }).getAttribute("href")).toBe(SRC);
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(thumb));
  });

  it("draws the thumbnail from the small copy and the viewer from the original", async () => {
    const THUMB = "/api/threads/t1/attachments/a1-thumb";
    render(<ToolCard threadId="t1" item={result([{ ...screenshot, thumbUrl: THUMB }])} />);
    const thumb = screen.getByRole("button", { name: "View browser_screenshot image full size" });
    expect(within(thumb).getByRole("img").getAttribute("src")).toBe(THUMB);
    fireEvent.click(thumb);
    const dialog = await viewer("browser_screenshot image");
    expect(within(dialog).getByRole("img").getAttribute("src")).toBe(SRC);
    expect(within(dialog).getByRole("link", { name: "Open original in a new tab" }).getAttribute("href")).toBe(SRC);
  });

  it("ignores a small copy that is not a same-origin path", () => {
    const { container } = render(
      <ToolCard threadId="t1" item={result([{ ...screenshot, thumbUrl: "data:image/jpeg;base64,/9j/4AAQ" }])} />,
    );
    expect(screen.getByRole("img").getAttribute("src")).toBe(SRC);
    expect(container.innerHTML).not.toContain("data:");
  });

  it("jumps from fitted to the image's own pixel size", async () => {
    let measure!: () => void;
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(private callback: ResizeObserverCallback) {}
        observe(el: Element) {
          if (el.getAttribute("aria-label") === "Expanded browser_screenshot image")
            measure = () =>
              this.callback(
                [{ contentRect: { width: 390, height: 800 } } as ResizeObserverEntry],
                this as unknown as ResizeObserver,
              );
        }
        unobserve() {}
        disconnect() {}
      },
    );
    render(<ToolCard threadId="t1" item={result([screenshot])} />);
    fireEvent.click(screen.getByRole("button", { name: /full size/ }));
    const dialog = await viewer();
    act(() => measure());
    const img = within(dialog).getByRole("img") as HTMLImageElement;
    // Sized from the event before a byte arrived: fitted to 390 - 32 padding.
    expect(img.style.width).toBe("358px");
    fireEvent.click(screen.getByRole("button", { name: "Actual size" }));
    expect(img.style.width).toBe("1280px");
    fireEvent.click(screen.getByRole("button", { name: "Fit image" }));
    expect(img.style.width).toBe("358px");
    vi.unstubAllGlobals();
  });

  it("names each of several images apart", async () => {
    render(<ToolCard threadId="t1" item={result([screenshot, { ...screenshot, url: "/api/threads/t1/attachments/a2" }])} />);
    fireEvent.click(screen.getByRole("button", { name: "View browser_screenshot (image 2 of 2) full size" }));
    expect(within(await viewer()).getByRole("img").getAttribute("src")).toBe("/api/threads/t1/attachments/a2");
  });
});

describe("a run of tool calls", () => {
  const items = [
    result([{ type: "text", text: "clicked" }], { id: "tool:a", title: "click" }),
    result([screenshot], { id: "tool:b" }),
  ];

  it("shows the run's screenshots while closed, and only the cards' once open", () => {
    render(<ToolRun threadId="t1" items={items} live={false} />);
    expect(screen.getAllByRole("img")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(screen.getAllByRole("img")).toHaveLength(1);
  });

  it("keeps a finished turn's screenshots in view behind its fold", () => {
    const turn: Turn = { id: "turn:1", prompt: "check it", done: true } as Turn;
    render(<TurnFold threadId="t1" turn={turn} items={items} />);
    expect(screen.getByRole("img").getAttribute("src")).toBe(SRC);
  });
});

describe("an edit in a tool result", () => {
  function open(content: ToolContent[]) {
    const view = render(<ToolCard threadId="t1" item={result(content, { title: "Edit a.ts", toolKind: "edit" })} />);
    fireEvent.click(screen.getByRole("button", { name: /Edit a\.ts/ }));
    return view;
  }

  it("renders a full unified patch as added and removed lines under its path", async () => {
    const { container } = open([
      {
        type: "diff",
        path: "src/a.ts",
        text: "--- a/src/a.ts\n+++ b/src/a.ts\n@@ -1,2 +1,2 @@\n keep\n-old\n+new\n",
      },
    ]);
    expect(screen.getByText("src/a.ts")).toBeTruthy();
    expect(await diffRows(container)).toEqual(["@@ -1,2 +1,2 @@", "11 keep", "2-old", "2+new"]);
  });

  it("renders a hunks-only patch the way Codex sends it", async () => {
    const { container } = open([{ type: "diff", path: "a.ts", text: "@@ -3 +3 @@\n-before\n+after\n" }]);
    expect(await diffRows(container)).toEqual(["@@ -3 +3 @@", "3-before", "3+after"]);
  });

  it("shows a hunkless diff as the file's text, not a patch missing a character per line", () => {
    open([{ type: "diff", path: "new.ts", text: "export const a = 1;\nexport const b = 2;\n" }]);
    expect(screen.getByText(/export const a = 1;\s+export const b = 2;/)).toBeTruthy();
  });

  it("counts a patch's lines on the closed card, not the newline that ends it", () => {
    render(
      <ToolCard
        threadId="t1"
        item={result([{ type: "diff", path: "a.ts", text: "@@ -1,2 +1,3 @@\n-x\n+y\n+z\n w\n" }], { title: "Edit a.ts" })}
      />,
    );
    expect(screen.getByText("5 lines")).toBeTruthy();
  });

  it("shows where a capped preview cut the patch, and says truncated rather than counting the preview", async () => {
    // The head was cut at a line break, so the marker follows it.
    const patch = "@@ -1,9 +1,9 @@\n-a\n+b\n\n\n[… 9000 characters truncated …]\n\n-y\n+z\n";
    const view = render(
      <ToolCard
        threadId="t1"
        item={result([{ type: "diff", path: "a.ts", text: patch }], { title: "Edit a.ts", truncated: true })}
      />,
    );
    expect(screen.getByText("truncated")).toBeTruthy();
    expect(screen.queryByText(/lines/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Edit a\.ts/ }));
    expect(await diffRows(view.container)).toEqual([
      "@@ -1,9 +1,9 @@",
      "1-a",
      "1+b",
      "[… 9000 characters truncated …]",
      "-y",
      "+z",
    ]);
  });
});

describe("text in a tool result", () => {
  it("shows neighbouring text pieces as one output once the card is open", () => {
    render(
      <ToolCard
        threadId="t1"
        item={result([
          { type: "text", text: "line one" },
          { type: "text", text: "line two" },
        ])}
      />,
    );
    expect(screen.queryByText(/line one/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /browser_screenshot/ }));
    expect(screen.getByText("line one line two").tagName).toBe("PRE");
  });
});
