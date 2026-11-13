// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { type ComponentProps, useState } from "react";
import { describe, expect, it, vi } from "vitest";

import { type CommentDraft, Diff, type LineComments } from "./Diff";
import { render, wrap } from "~/test/harness";

const PATCH = [
  "@@ -1,3 +1,4 @@",
  " keep",
  "-before change",
  "+after change",
  "+brand new",
  " tail",
].join("\n");

function rowOf(text: string): HTMLElement {
  const row = screen.getByText(text).closest<HTMLElement>("[data-diff-row]");
  if (!row) throw new Error(`no row for ${text}`);
  return row;
}

function side(row: HTMLElement, which: "left" | "right"): HTMLElement {
  const cell = row.querySelector<HTMLElement>(`[data-side="${which}"]`);
  if (!cell) throw new Error(`no ${which} cell`);
  return cell;
}

describe("Diff split layout", () => {
  it("puts a modified line beside its replacement", () => {
    render(<Diff patch={PATCH} layout="split" />);
    const row = rowOf("before change");
    expect(side(row, "left").textContent).toContain("before change");
    expect(side(row, "left").textContent).toContain("2");
    expect(side(row, "right").textContent).toContain("after change");
  });

  it("leaves the left side empty for a line that was only added", () => {
    render(<Diff patch={PATCH} layout="split" />);
    const row = rowOf("brand new");
    expect(side(row, "left").textContent).toBe("");
    expect(side(row, "left").hasAttribute("data-empty")).toBe(true);
    expect(side(row, "right").textContent).toContain("3");
  });

  it("shows context on both sides with each side's own number", () => {
    render(<Diff patch={PATCH} layout="split" />);
    const row = screen.getAllByText("tail")[0]?.closest<HTMLElement>("[data-diff-row]");
    if (!row) throw new Error("no row for tail");
    expect(side(row, "left").textContent).toBe("3 tail");
    expect(side(row, "right").textContent).toBe("4 tail");
  });
});

function marks(container: HTMLElement): string[] {
  return [...container.querySelectorAll("mark[data-diff-match]")].map((m) => m.textContent ?? "");
}

describe("Diff highlight", () => {
  it("marks every case-insensitive occurrence, in every kind of line", () => {
    const patch = ["@@ -1,2 +1,2 @@ Change", " change CHANGE", "-a change", "+b Change"].join("\n");
    const { container } = render(<Diff patch={patch} highlight="change" />);
    expect(marks(container)).toEqual(["Change", "change", "CHANGE", "change", "Change"]);
  });

  it("marks in both columns of a split diff", () => {
    const { container } = render(<Diff patch={PATCH} layout="split" highlight="change" />);
    expect(marks(container)).toEqual(["change", "change"]);
  });

  it("marks nothing when the highlight is empty or only whitespace", () => {
    for (const highlight of ["", "   "]) {
      const { container, unmount } = render(<Diff patch={PATCH} highlight={highlight} />);
      expect(marks(container)).toEqual([]);
      expect(container.textContent).toContain("before change");
      unmount();
    }
  });

  it("treats regex metacharacters as literal text", () => {
    const patch = ["@@ -1 +1 @@", "-call(a.b)", "+call(axb) [x]+"].join("\n");
    const { container } = render(<Diff patch={patch} highlight="(a.b)" />);
    expect(marks(container)).toEqual(["(a.b)"]);
    const second = render(<Diff patch={patch} highlight="[x]+" />);
    expect(marks(second.container)).toEqual(["[x]+"]);
  });

  it("keeps the text intact around the marks", () => {
    render(<Diff patch={PATCH} highlight="CHANGE" />);
    expect(screen.getByText((_, el) => el?.tagName === "SPAN" && el.textContent === "before change")).toBeTruthy();
  });
});

/** A diff whose owner holds the comment draft, as the panel does. */
function Commentable({
  onAdd,
  ...props
}: Omit<ComponentProps<typeof Diff>, "comments"> & { onAdd: LineComments["onAdd"] }) {
  const [draft, setDraft] = useState<CommentDraft | null>(null);
  return <Diff {...props} comments={{ draft, onDraft: setDraft, onAdd }} />;
}

function box(): HTMLElement | null {
  return document.querySelector<HTMLElement>("[data-diff-comment]");
}

function type(text: string) {
  fireEvent.change(screen.getByRole("textbox"), { target: { value: text } });
}

function addButton(): HTMLButtonElement {
  return screen.getByRole<HTMLButtonElement>("button", { name: "Add to chat" });
}

describe("Diff line comments", () => {
  it("does nothing on a click when no handler is given", () => {
    render(<Diff patch={PATCH} />);
    fireEvent.click(rowOf("after change"));
    expect(box()).toBeNull();
    expect(rowOf("after change").hasAttribute("data-selected")).toBe(false);
  });

  it("opens a box under an added line and hands up the trimmed comment", () => {
    const onAdd = vi.fn();
    render(<Commentable patch={PATCH} onAdd={onAdd} />);
    const row = rowOf("after change");
    fireEvent.click(row);
    expect(row.hasAttribute("data-selected")).toBe(true);
    expect(row.nextElementSibling).toBe(box());
    expect(document.activeElement).toBe(screen.getByRole("textbox"));

    type("  rename this  ");
    fireEvent.click(addButton());
    expect(onAdd).toHaveBeenCalledTimes(1);
    expect(onAdd).toHaveBeenCalledWith(
      { line: expect.objectContaining({ kind: "add", text: "after change", newNo: 2 }), side: "new" },
      "rename this",
    );
    expect(box()).toBeNull();
    expect(rowOf("after change").hasAttribute("data-selected")).toBe(false);
  });

  it("cites the old side for a removed line and the new side for context", () => {
    const onAdd = vi.fn();
    render(<Commentable patch={PATCH} onAdd={onAdd} />);
    fireEvent.click(rowOf("before change"));
    type("gone");
    fireEvent.click(addButton());
    fireEvent.click(rowOf("keep"));
    type("kept");
    fireEvent.click(addButton());
    expect(onAdd.mock.calls).toEqual([
      [{ line: expect.objectContaining({ kind: "del", oldNo: 2 }), side: "old" }, "gone"],
      [{ line: expect.objectContaining({ kind: "context", oldNo: 1, newNo: 1 }), side: "new" }, "kept"],
    ]);
  });

  it("ignores hunk headers", () => {
    render(<Commentable patch={PATCH} onAdd={vi.fn()} />);
    fireEvent.click(rowOf("@@ -1,3 +1,4 @@"));
    expect(box()).toBeNull();
  });

  it("cites the side of the cell tapped in a split diff", () => {
    const onAdd = vi.fn();
    render(<Commentable patch={PATCH} layout="split" onAdd={onAdd} />);
    const row = rowOf("before change");
    fireEvent.click(side(row, "left"));
    type("left");
    fireEvent.click(addButton());
    fireEvent.click(side(rowOf("after change"), "right"));
    type("right");
    fireEvent.click(addButton());
    expect(onAdd.mock.calls).toEqual([
      [{ line: expect.objectContaining({ kind: "del", text: "before change" }), side: "old" }, "left"],
      [{ line: expect.objectContaining({ kind: "add", text: "after change" }), side: "new" }, "right"],
    ]);
  });

  it("does not open on an empty split cell or a hunk row", () => {
    render(<Commentable patch={PATCH} layout="split" onAdd={vi.fn()} />);
    fireEvent.click(side(rowOf("brand new"), "left"));
    fireEvent.click(rowOf("@@ -1,3 +1,4 @@"));
    expect(box()).toBeNull();
  });

  it("keeps Add to chat disabled until there is something to send", () => {
    const onAdd = vi.fn();
    render(<Commentable patch={PATCH} onAdd={onAdd} />);
    fireEvent.click(rowOf("after change"));
    expect(addButton().disabled).toBe(true);
    type("   ");
    expect(addButton().disabled).toBe(true);
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", ctrlKey: true });
    expect(onAdd).not.toHaveBeenCalled();
    type("x");
    expect(addButton().disabled).toBe(false);
  });

  it("closes on Cancel without sending, and the next box starts empty", () => {
    const onAdd = vi.fn();
    render(<Commentable patch={PATCH} onAdd={onAdd} />);
    fireEvent.click(rowOf("after change"));
    type("never mind");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(box()).toBeNull();
    expect(onAdd).not.toHaveBeenCalled();
    fireEvent.click(rowOf("after change"));
    expect(screen.getByRole<HTMLTextAreaElement>("textbox").value).toBe("");
  });

  it("moves the box to another line and keeps what was typed", () => {
    const onAdd = vi.fn();
    render(<Commentable patch={PATCH} onAdd={onAdd} />);
    fireEvent.click(rowOf("after change"));
    type("meant the next one");
    fireEvent.click(rowOf("brand new"));
    expect(rowOf("after change").hasAttribute("data-selected")).toBe(false);
    expect(rowOf("brand new").nextElementSibling).toBe(box());
    expect(screen.getByRole<HTMLTextAreaElement>("textbox").value).toBe("meant the next one");
    fireEvent.click(addButton());
    expect(onAdd).toHaveBeenCalledWith(
      { line: expect.objectContaining({ text: "brand new" }), side: "new" },
      "meant the next one",
    );
  });

  it("does not count clicks inside the box as a tap on a line", () => {
    render(<Commentable patch={PATCH} onAdd={vi.fn()} />);
    fireEvent.click(rowOf("after change"));
    type("draft");
    fireEvent.click(screen.getByRole("textbox"));
    expect(rowOf("after change").hasAttribute("data-selected")).toBe(true);
    expect(screen.getByRole<HTMLTextAreaElement>("textbox").value).toBe("draft");
  });

  it("leaves a click that ends a text selection alone", () => {
    const spy = vi.spyOn(window, "getSelection").mockReturnValue({ toString: () => "after" } as Selection);
    try {
      render(<Commentable patch={PATCH} onAdd={vi.fn()} />);
      fireEvent.click(rowOf("after change"));
      expect(box()).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });

  it("sends on Ctrl+Enter and cancels on Escape", () => {
    const onAdd = vi.fn();
    render(<Commentable patch={PATCH} onAdd={onAdd} />);
    fireEvent.click(rowOf("after change"));
    type("ship it");
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", ctrlKey: true });
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ side: "new" }), "ship it");
    expect(box()).toBeNull();

    fireEvent.click(rowOf("brand new"));
    type("drop it");
    fireEvent.keyDown(screen.getByRole("textbox"), { key: "Escape" });
    expect(box()).toBeNull();
    expect(onAdd).toHaveBeenCalledTimes(1);
  });

  it("drops the box when a re-read loses the selected line", () => {
    const { rerender } = render(<Commentable patch={PATCH} onAdd={vi.fn()} />);
    fireEvent.click(rowOf("brand new"));
    expect(box()).not.toBeNull();
    rerender(wrap(<Commentable patch={PATCH.replace("+brand new", "+brand newer")} onAdd={vi.fn()} />));
    expect(box()).toBeNull();
  });
});
