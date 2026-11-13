// @vitest-environment jsdom
import { fireEvent, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { settingsIndex, type SettingEntry } from "~/lib/settingsSearch";
import type { HarnessMeta, Project } from "~/protocol";
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
  over: {
    at?: SettingsSection;
    projects?: Project[];
    harnesses?: HarnessMeta[];
    hasElevenLabsKey?: boolean;
    onAddProject?: () => void;
  } = {},
) {
  const props = {
    projects: [project("p1", "alpha"), project("p2", "beta")],
    harnesses: [] as HarnessMeta[],
    userConfig: { version: 1 },
    threadCounts: {},
    onSaveUserConfig: vi.fn(async () => {}),
    hasElevenLabsKey: false,
    onSetElevenLabsKey: vi.fn(async () => {}),
    providers: {
      wires: {} as never,
      onOpenTerminal: vi.fn(),
      onRecheck: vi.fn(),
    },
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
    rerender: (p: Partial<typeof props>) =>
      view.rerender(<SettingsScreen {...props} {...p} />),
  };
}

const pane = () => screen.queryByRole("region");
const nav = () =>
  screen.queryByRole("navigation", { name: "Settings sections" });

describe("on a desktop", () => {
  it("opens on General beside the list, and moves between sections", () => {
    open();
    expect(nav()).toBeTruthy();
    expect(pane()?.getAttribute("aria-label")).toBe("General");

    fireEvent.click(screen.getByRole("button", { name: "beta" }));
    expect(pane()?.getAttribute("aria-label")).toBe("beta");
    expect(
      screen.getByRole("button", { name: "beta" }).getAttribute("aria-current"),
    ).toBe("page");
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

it("opens Providers on adding an account for the asked-for agent", () => {
  const codex: HarnessMeta = {
    id: "codex",
    name: "Codex",
    accent: "#000",
    models: [],
    permissionModes: [],
    availability: {
      state: "unavailable",
      remedy: [{ text: "Add account", action: "add_account" }],
    },
    instances: [],
  };
  open({ at: { kind: "providers", addFor: "codex" }, harnesses: [codex] });
  expect(screen.getByText("New Codex account")).toBeTruthy();
});

it("hands New project to the caller", () => {
  const onAddProject = vi.fn();
  open({ onAddProject });
  fireEvent.click(screen.getByRole("button", { name: "New project" }));
  expect(onAddProject).toHaveBeenCalled();
});

describe("searching", () => {
  // The panes fetch voices and push devices on mount; none of that is under test.
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() => new Promise(() => {})),
    );
  });

  /** A mouse and trackpad: the hover query the harness otherwise answers false. */
  const withMouse = () => {
    const base = window.matchMedia;
    vi.stubGlobal("matchMedia", (q: string) =>
      /hover: hover/.test(q) ? { ...base(q), matches: true } : base(q),
    );
  };

  const search = () =>
    screen.getByRole("searchbox", { name: "Search settings" });
  const type = (q: string) =>
    fireEvent.change(search(), { target: { value: q } });
  const results = () =>
    screen.queryByRole("list", { name: "Matching settings" });
  /** A result's button, by the setting's label and the section it is in. */
  const result = (label: string, where: string) => {
    const hit = within(results()!)
      .getAllByRole("button")
      .find((b) => b.textContent === label + where);
    if (!hit) throw new Error(`no result "${label}" in "${where}"`);
    return hit;
  };
  const target = (anchor: string) =>
    [...document.querySelectorAll<HTMLElement>("[data-setting]")].find(
      (e) => e.dataset.setting === anchor,
    );
  const found = () => [
    ...document.querySelectorAll("[data-setting][data-found]"),
  ];

  const gitProject = (id: string, name: string, paths: string[]): Project => ({
    ...project(id, name),
    folders: paths.map((path, i) => ({
      ...project(id, name).folders[0],
      id: `${id}-g${i}`,
      path,
      git: true,
    })),
  });

  const codex: HarnessMeta = {
    id: "codex",
    name: "Codex",
    accent: "#000",
    models: [],
    permissionModes: [],
    availability: { state: "ready" } as HarnessMeta["availability"],
    instances: [
      {
        id: "codex-work",
        driver: "codex",
        displayName: "Zephyr",
        enabled: true,
        availability: { state: "ready" } as HarnessMeta["availability"],
      },
    ],
  };

  it("replaces the section list with what matches, and brings it back when cleared", () => {
    open();
    type("branch");
    expect(results()).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Notifications" })).toBeNull();

    type("   ");
    expect(results()).toBeNull();
    expect(screen.getByRole("button", { name: "Notifications" })).toBeTruthy();
  });

  it("says so when nothing matches", () => {
    open();
    type("zzzqqq");
    expect(results()).toBeNull();
    expect(screen.getByText("No settings match.")).toBeTruthy();
  });

  it("clears with the clear button", () => {
    open();
    type("sound");
    fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
    expect((search() as HTMLInputElement).value).toBe("");
    expect(results()).toBeNull();
  });

  it("opens General on a General result and marks the setting", () => {
    open({ at: { kind: "notifications" } });
    type("branch names");
    fireEvent.click(result("Branch names from issues", "General"));
    expect(pane()?.getAttribute("aria-label")).toBe("General");
    expect(target("branch-format")?.hasAttribute("data-found")).toBe(true);
    expect(found()).toHaveLength(1);
  });

  it("opens the project a result belongs to", () => {
    open({
      projects: [
        gitProject("p1", "alpha", ["/a"]),
        gitProject("p2", "beta", ["/b"]),
      ],
    });
    type("beta base branch");
    fireEvent.click(result("Base branch", "beta"));
    expect(pane()?.getAttribute("aria-label")).toBe("beta");
    expect(target("base-branch-p2-g0")?.hasAttribute("data-found")).toBe(true);
  });

  it("opens Notifications on a Notifications result", () => {
    open();
    type("chime");
    fireEvent.click(result("Play a sound", "Notifications"));
    expect(pane()?.getAttribute("aria-label")).toBe("Notifications");
    expect(target("alert-sound")?.hasAttribute("data-found")).toBe(true);
  });

  it("starts Providers over on its list, where the result is, even from adding an account", () => {
    open({ at: { kind: "providers", addFor: "codex" }, harnesses: [codex] });
    expect(screen.getByText("New Codex account")).toBeTruthy();
    expect(target("recheck")).toBeUndefined();

    type("check again");
    fireEvent.click(result("Check again", "Providers"));
    expect(screen.queryByText("New Codex account")).toBeNull();
    expect(target("recheck")?.hasAttribute("data-found")).toBe(true);
  });

  it("finds a harness by its account's name", () => {
    open({ harnesses: [codex] });
    type("zephyr");
    fireEvent.click(result("Codex", "Providers"));
    expect(pane()?.getAttribute("aria-label")).toBe("Providers");
    expect(target("harness-codex")?.hasAttribute("data-found")).toBe(true);
  });

  it("opens the first result on Enter, and does nothing when there is none", () => {
    open({ at: { kind: "notifications" } });
    type("default model");
    fireEvent.keyDown(search(), { key: "Enter" });
    expect(pane()?.getAttribute("aria-label")).toBe("General");
    expect(target("default-model")?.hasAttribute("data-found")).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
    fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
    type("zzzqqq");
    fireEvent.keyDown(search(), { key: "Enter" });
    expect(pane()?.getAttribute("aria-label")).toBe("Notifications");
  });

  it("marks the setting again when it is picked again", () => {
    open();
    type("chime");
    fireEvent.click(result("Play a sound", "Notifications"));
    target("alert-sound")!.removeAttribute("data-found");
    fireEvent.click(result("Play a sound", "Notifications"));
    expect(target("alert-sound")?.hasAttribute("data-found")).toBe(true);
  });

  it("clears the search on Escape before it closes the dialog", () => {
    const { props } = open();
    type("sound");
    search().focus();
    fireEvent.keyDown(search(), { key: "Escape" });
    expect((search() as HTMLInputElement).value).toBe("");
    expect(props.onClose).not.toHaveBeenCalled();

    fireEvent.keyDown(search(), { key: "Escape" });
    expect(props.onClose).toHaveBeenCalled();
  });

  it("closes on Escape with an empty search", () => {
    const { props } = open();
    search().focus();
    fireEvent.keyDown(search(), { key: "Escape" });
    expect(props.onClose).toHaveBeenCalled();
  });

  it("on a phone, opens the result's section and goes back to the results with the query kept", () => {
    viewport("phone");
    open();
    type("chime");
    fireEvent.click(result("Play a sound", "Notifications"));
    expect(nav()).toBeNull();
    expect(pane()?.getAttribute("aria-label")).toBe("Notifications");
    expect(target("alert-sound")?.hasAttribute("data-found")).toBe(true);

    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(pane()).toBeNull();
    expect((search() as HTMLInputElement).value).toBe("chime");
    expect(result("Play a sound", "Notifications")).toBeTruthy();
  });

  it("puts focus on the setting's control with a mouse", () => {
    withMouse();
    open();
    type("projects folder");
    fireEvent.click(result("Projects folder", "General"));
    const control = target("projects-dir")!.querySelector("input");
    expect(control).toBeTruthy();
    expect(document.activeElement).toBe(control);
  });

  it("leaves focus alone on a touch screen, so no keyboard opens", () => {
    open();
    type("projects folder");
    fireEvent.click(result("Projects folder", "General"));
    expect(target("projects-dir")?.hasAttribute("data-found")).toBe(true);
    expect(target("projects-dir")!.contains(document.activeElement)).toBe(
      false,
    );
  });

  it("lands every setting the index offers somewhere on screen", () => {
    const projects = [
      gitProject("p1", "alpha", ["/src/alpha"]),
      gitProject("p2", "duo", ["/src/duo/web", "/src/duo/api"]),
      project("p3", "plain"),
    ];
    const harnesses = [codex];
    const index = settingsIndex({
      projects,
      harnesses,
      hasElevenLabsKey: true,
    });
    const sectionName = (e: SettingEntry) =>
      e.place.kind === "project"
        ? projects.find(
            (p) => e.place.kind === "project" && p.id === e.place.id,
          )!.name
        : e.place.kind[0].toUpperCase() + e.place.kind.slice(1);

    open({ projects, harnesses, hasElevenLabsKey: true });
    for (const e of index) {
      for (const el of found()) el.removeAttribute("data-found");
      type(e.label);
      fireEvent.click(result(e.label, e.where));
      expect(pane()?.getAttribute("aria-label"), e.anchor).toBe(sectionName(e));
      expect(
        target(e.anchor)?.hasAttribute("data-found"),
        `${e.where}: ${e.anchor}`,
      ).toBe(true);
    }
  });
});
