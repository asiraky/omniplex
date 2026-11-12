// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { Folder } from "~/protocol";
import { render } from "~/test/harness";

import { GitChip } from "./GitChip";
import type { WorkspaceChoiceState } from "./useWorkspaceChoice";

// The popover's code never arrives: the dropped fetch a flaky link gives.
vi.mock("./GitChoice", () => {
  throw new Error("Failed to fetch dynamically imported module");
});

const folder = { path: "/repo" } as Folder;
const git = { label: "Main checkout" } as WorkspaceChoiceState;

describe("GitChip", () => {
  it("says so, and keeps the app up, when the popover's code will not load", async () => {
    render(<GitChip folder={folder} git={git} userConfig={null} />);
    fireEvent.click(screen.getByRole("button", { name: /Main checkout/ }));
    expect(await screen.findByText(/Couldn't load/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /Main checkout/ })).toBeTruthy();
  });
});
