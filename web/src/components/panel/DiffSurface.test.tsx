// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { DiffSurface } from "./DiffSurface";
import { render } from "~/test/harness";
import type { FileDiff, SessionChanges } from "~/protocol";

// Long enough that no container holds it: this is the line the wrap toggle exists for.
const LONG = "x".repeat(400);
const TEXT = `const a = "${LONG}";`;

const CHANGES: SessionChanges = {
  root: "/tmp/wt",
  branch: "feature/wrap",
  baseRef: "main",
  additions: 1,
  deletions: 0,
  files: [{ path: "web/src/long.ts", status: "modified", additions: 1, deletions: 0 }],
};

const DIFF: FileDiff = {
  path: "web/src/long.ts",
  status: "modified",
  patch: `@@ -1 +1 @@\n+${TEXT}\n`,
};

function renderSurface() {
  return render(
    <DiffSurface
      changes={CHANGES}
      loading={false}
      error=""
      onRefresh={() => {}}
      loadDiff={() => Promise.resolve(DIFF)}
    />,
  );
}

/** The diff line's row, whose classes say whether it wraps. */
async function openDiffRow(): Promise<HTMLElement> {
  fireEvent.click(screen.getByRole("button", { name: /long\.ts/ }));
  const line = await screen.findByText(TEXT);
  return line.parentElement as HTMLElement;
}

function checkbox() {
  return screen.getByRole("checkbox", { name: /wrap text/i });
}

describe("DiffSurface wrap toggle", () => {
  beforeEach(() => localStorage.clear());

  it("starts unwrapped, with rows sized to their longest line so the container can scroll", async () => {
    renderSurface();
    const row = await openDiffRow();

    expect(row.className).toContain("whitespace-pre");
    expect(row.className).not.toContain("whitespace-pre-wrap");
    // Without this the line spills out of a container-width box and cannot be scrolled to.
    expect(row.className).toContain("w-max");
    expect(checkbox().getAttribute("data-state")).toBe("unchecked");
  });

  it("wraps once ticked, and remembers the choice for the next mount", async () => {
    const first = renderSurface();
    fireEvent.click(checkbox());
    expect((await openDiffRow()).className).toContain("whitespace-pre-wrap");
    first.unmount();

    renderSurface();
    expect(checkbox().getAttribute("data-state")).toBe("checked");
    expect((await openDiffRow()).className).toContain("whitespace-pre-wrap");
  });
});
