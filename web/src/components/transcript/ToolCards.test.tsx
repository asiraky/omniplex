// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

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
    render(<ToolRun items={items} live />);
    expect(tail()?.textContent).toBe("stream line 1\nprogress 100%");
  });

  it("follows the run's active call, not the first one", () => {
    const items = [bash("1", { tail: "first's output\n" }), bash("2", { tail: "second's output\n" })];
    render(<ToolRun items={items} live />);
    expect(tail()?.textContent).toBe("second's output");
  });

  it("moves into the card when the run is opened, rather than showing twice", () => {
    render(<ToolRun items={[bash("1", { tail: "building\n" })]} live />);
    fireEvent.click(screen.getByRole("button", { name: /bash 1/ }));
    expect(screen.getAllByRole("group", { name: "Latest output" })).toHaveLength(1);
    expect(tail()?.textContent).toBe("building");
  });

  it("shows nothing under a run that is no longer live", () => {
    render(<ToolRun items={[bash("1", { tail: "building\n" })]} live={false} />);
    expect(tail()).toBeNull();
  });

  it("shows on a running card without opening it", () => {
    render(<ToolCard item={bash("1", { tail: "line a\nline b\n" })} />);
    expect(screen.getByRole("button", { name: /bash 1/ }).getAttribute("aria-expanded")).toBe("false");
    expect(tail()?.textContent).toBe("line a\nline b");
  });

  it("shows nothing for a running call that has printed nothing visible", () => {
    render(<ToolCard item={bash("1")} />);
    render(<ToolCard item={bash("2", { tail: "\x1b[0m\n" })} />);
    expect(tail()).toBeNull();
  });

  it("shows nothing for a completed call, which has its output instead", () => {
    const done = bash("1", { status: "completed", content: [{ type: "text", text: "all\nof it" }] });
    render(<ToolCard item={done} />);
    expect(tail()).toBeNull();
    expect(screen.getByText("2 lines")).toBeTruthy();
  });
});
