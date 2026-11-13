// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { FileBrowser } from "./FileBrowser";
import { render, wrap } from "~/test/harness";
import type { FileTree } from "~/protocol";

const TREE: FileTree = { root: "/wt", files: ["src/lib/deep.ts", "src/top.ts", "README.md"] };

function browser(selectedPath?: string) {
  return (
    <FileBrowser
      tree={TREE}
      loading={false}
      error=""
      onRefresh={() => {}}
      includeIgnored={false}
      onToggleIgnored={() => {}}
      changedPaths={new Set()}
      selectedPath={selectedPath}
      onSelect={() => {}}
      loadFile={() => new Promise(() => {})}
    />
  );
}

describe("FileBrowser", () => {
  it("opens a newly selected file's folders, and lets the user close them again", () => {
    const { rerender } = render(browser());
    expect(screen.queryByRole("button", { name: "deep.ts" })).toBeNull();

    rerender(wrap(browser("src/lib/deep.ts")));
    expect(screen.getByRole("button", { name: "deep.ts" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "lib" }));
    rerender(wrap(browser("src/lib/deep.ts")));
    expect(screen.queryByRole("button", { name: "deep.ts" })).toBeNull();
  });

  it("opens the folders of a file selected on mount", () => {
    render(browser("src/lib/deep.ts"));
    expect(screen.getByRole("button", { name: "deep.ts" })).toBeTruthy();
  });
});
