// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { Project } from "~/protocol";
import { render, viewport } from "~/test/harness";
import { SettingsScreen, type SettingsSection } from "./SettingsScreen";

const project = (id: string, name: string) =>
  ({
    id,
    name,
    defaults: { harness: "claude", workspace: "local" },
    folders: [
      {
        id: `${id}-f`,
        path: `/tmp/${name}`,
        git: false,
        copiesDir: ".worktrees",
        provisionTimeoutSeconds: 1800,
        deprovisionTimeoutSeconds: 600,
      },
    ],
    createdAt: 0,
    updatedAt: 0,
  }) satisfies Project;

function open(
  over: { at?: SettingsSection; projects?: Project[]; onAddProject?: () => void } = {},
) {
  const props = {
    projects: [project("p1", "alpha"), project("p2", "beta")],
    harnesses: [],
    userConfig: { version: 1 },
    threadCounts: {},
    onSaveUserConfig: vi.fn(async () => {}),
    providers: { wires: {} as never, onOpenTerminal: vi.fn(), onRecheck: vi.fn() },
    project: {
      onSave: vi.fn(async () => {}),
      onAddFolder: vi.fn(),
      onRemoveFolder: vi.fn(),
      listRepos: vi.fn(async () => []),
      onDelete: vi.fn(async () => {}),
    },
    onAddProject: vi.fn(),
    onClose: vi.fn(),
    ...over,
  };
  const view = render(<SettingsScreen {...props} />);
  return {
    props,
    rerender: (p: Partial<typeof props>) => view.rerender(<SettingsScreen {...props} {...p} />),
  };
}

const pane = () => screen.queryByRole("region");
const nav = () => screen.queryByRole("navigation", { name: "Settings sections" });

describe("on a desktop", () => {
  it("opens on General beside the list, and moves between sections", () => {
    open();
    expect(nav()).toBeTruthy();
    expect(pane()?.getAttribute("aria-label")).toBe("General");

    fireEvent.click(screen.getByRole("button", { name: "beta" }));
    expect(pane()?.getAttribute("aria-label")).toBe("beta");
    expect(screen.getByRole("button", { name: "beta" }).getAttribute("aria-current")).toBe("page");
  });

  it("opens where it was asked to", () => {
    open({ at: { kind: "project", id: "p1" } });
    expect(pane()?.getAttribute("aria-label")).toBe("alpha");
  });

  it("falls back to General when the open project goes away", () => {
    const { rerender } = open({ at: { kind: "project", id: "p2" } });
    rerender({ projects: [project("p1", "alpha")] });
    expect(pane()?.getAttribute("aria-label")).toBe("General");
  });
});

describe("on a phone", () => {
  it("shows the list first, then one section with a way back", () => {
    viewport("phone");
    open();
    expect(pane()).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "alpha" }));
    expect(nav()).toBeNull();
    expect(pane()?.getAttribute("aria-label")).toBe("alpha");

    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(nav()).toBeTruthy();
    expect(pane()).toBeNull();
  });

  it("opens straight into the asked-for section", () => {
    viewport("phone");
    open({ at: { kind: "providers" } });
    expect(nav()).toBeNull();
    expect(pane()?.getAttribute("aria-label")).toBe("Providers");
  });
});

it("hands New project to the caller", () => {
  const onAddProject = vi.fn();
  open({ onAddProject });
  fireEvent.click(screen.getByRole("button", { name: "New project" }));
  expect(onAddProject).toHaveBeenCalled();
});
