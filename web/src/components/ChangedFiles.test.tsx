// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";
import type { TurnDiff } from "~/protocol";
import { ChangedFiles } from "./ChangedFiles";

// Four files over four areas: more than the preview shows, so a collapsed
// card has a "Show all" chip as well as the three it picks.
const diff: TurnDiff = {
  turnId: "t2",
  files: [
    { path: "web/src/app.ts", status: "modified", additions: 3, deletions: 1 },
    { path: "internal/server/run.go", status: "added", additions: 40, deletions: 0 },
    { path: "docs/notes.md", status: "modified", additions: 2, deletions: 2 },
    { path: "README.md", status: "modified", additions: 1, deletions: 0 },
  ],
  additions: 46,
  deletions: 3,
};

const summary = () => screen.getByRole("button", { name: /4 files changed/ });
// A file row and a preview chip both carry the file's full path as a title,
// so this finds whichever of the two the card is drawing.
const fileButton = (path: string) => screen.queryByTitle(path);

describe("ChangedFiles", () => {
  it("opens the newest turn's card on its tree, and leaves an older one collapsed", () => {
    const { unmount } = render(<ChangedFiles diff={diff} turnNumber={2} latest onOpenDiff={() => {}} />);
    expect(summary().getAttribute("aria-expanded")).toBe("true");
    expect(fileButton("README.md")).toBeTruthy();
    unmount();

    render(<ChangedFiles diff={diff} turnNumber={2} latest={false} onOpenDiff={() => {}} />);
    expect(summary().getAttribute("aria-expanded")).toBe("false");
    // Nor does an older card show the preview chips.
    expect(fileButton("web/src/app.ts")).toBeNull();
  });

  it("opens the diff on this turn, at the file clicked or the first file", () => {
    const onOpenDiff = vi.fn();
    render(<ChangedFiles diff={diff} turnNumber={2} latest onOpenDiff={onOpenDiff} />);

    fireEvent.click(fileButton("internal/server/run.go")!);
    expect(onOpenDiff).toHaveBeenLastCalledWith("internal/server/run.go", expect.objectContaining({ id: "t2", number: 2 }));

    fireEvent.click(screen.getByRole("button", { name: "Open diff" }));
    expect(onOpenDiff).toHaveBeenLastCalledWith("web/src/app.ts", expect.objectContaining({ id: "t2", number: 2 }));
  });

  it("collapses the newest card to its preview chips, which still open this turn", () => {
    const onOpenDiff = vi.fn();
    render(<ChangedFiles diff={diff} turnNumber={2} latest onOpenDiff={onOpenDiff} />);

    fireEvent.click(summary());
    expect(summary().getAttribute("aria-expanded")).toBe("false");
    // README.md is the fourth area's file, one past the three chips.
    expect(fileButton("README.md")).toBeNull();

    fireEvent.click(fileButton("docs/notes.md")!);
    expect(onOpenDiff).toHaveBeenLastCalledWith("docs/notes.md", expect.objectContaining({ id: "t2", number: 2 }));

    fireEvent.click(screen.getByRole("button", { name: "Show all 4 files" }));
    expect(summary().getAttribute("aria-expanded")).toBe("true");
  });

  it("closes once a newer turn lands, unless the reader opened or closed it", () => {
    const untouched = render(<ChangedFiles diff={diff} turnNumber={2} latest onOpenDiff={() => {}} />);
    untouched.rerender(wrap(<ChangedFiles diff={diff} turnNumber={2} latest={false} onOpenDiff={() => {}} />));
    expect(summary().getAttribute("aria-expanded")).toBe("false");
    untouched.unmount();

    // Collapsed by hand and then reopened: the reader asked for it open, and a
    // newer turn landing does not take that back.
    const touched = render(<ChangedFiles diff={diff} turnNumber={2} latest onOpenDiff={() => {}} />);
    fireEvent.click(summary());
    fireEvent.click(summary());
    touched.rerender(wrap(<ChangedFiles diff={diff} turnNumber={2} latest={false} onOpenDiff={() => {}} />));
    expect(summary().getAttribute("aria-expanded")).toBe("true");
  });
});
