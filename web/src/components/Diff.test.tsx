// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Diff } from "./Diff";
import { render } from "~/test/harness";

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
