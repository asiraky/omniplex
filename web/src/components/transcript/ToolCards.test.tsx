// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import type { Item } from "~/protocol";

import { ToolCard, ToolRun } from "./ToolCards";

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

  it("keeps the preview input when the server sends output only", async () => {
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
