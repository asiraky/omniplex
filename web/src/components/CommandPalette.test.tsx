// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { render, wrap } from "~/test/harness";
import type { Folder, Project, ThreadMeta, Workspace } from "~/protocol";

import { CommandPalette, type CommandPaletteProps, type PaletteAction } from "./CommandPalette";

beforeAll(() => {
  // cmdk scrolls the highlighted row into view; jsdom has no layout to scroll.
  Element.prototype.scrollIntoView = () => {};
});

/**
 * A screen of either width, with or without a hovering pointer. The harness
 * answers "no" to the hover probe, which hides every key hint; the tests about
 * hints need to say yes.
 */
function screenAs(kind: "phone" | "desktop", hover: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: /min-width/.test(query) ? kind === "desktop" : /hover: hover/.test(query) ? hover : false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
}

const folder = (id: string, path: string, git: boolean): Folder => ({
  id,
  path,
  git,
  copiesDir: ".worktrees",
  provisionTimeoutSeconds: 0,
  deprovisionTimeoutSeconds: 0,
});

const project = (id: string, name: string, folders: Folder[]): Project => ({
  id,
  name,
  defaults: {} as Project["defaults"],
  folders,
  createdAt: 0,
  updatedAt: 0,
});

const projects = [
  project("solo", "Solo", [folder("solo-f", "/code/solo", true)]),
  project("omni", "Omniplex", [folder("web", "/code/omni/web", true), folder("docs", "/code/omni/docs", false)]),
  project("notes", "Notes", [folder("notes-f", "/notes", false)]),
];

const workspaces: Workspace[] = [
  { path: "/code/omni/web", branch: "main", isRoot: true },
  { path: "/code/omni/.worktrees/feat-x", branch: "feat-x" },
  { path: "/code/omni/.worktrees/busy", branch: "busy-branch", busy: true, busyTitle: "Other thread" },
];

const thread = (id: string, title: string, updatedAt: number): ThreadMeta =>
  ({ id, title, updatedAt, createdAt: 0, cwd: "/", harness: "claude", headSeq: 0, phase: "", position: 0 }) as ThreadMeta;

// t0 is the oldest; the active thread is the most recent of all.
const threads = [
  ...Array.from({ length: 12 }, (_, i) => thread(`t${i}`, `Thread number ${i}`, 100 + i)),
  thread("active", "The open one", 1000),
];

function setup(over: Partial<CommandPaletteProps> = {}) {
  const props: CommandPaletteProps = {
    open: true,
    initialPage: "root",
    onOpenChange: vi.fn(),
    threads,
    activeId: "active",
    projects,
    defaultProjectId: "omni",
    actions: [],
    onSelectThread: vi.fn(),
    onNewThread: vi.fn(),
    onListWorkspaces: vi.fn(async () => workspaces),
    ...over,
  };
  const view = render(<CommandPalette {...props} />);
  return { ...view, props };
}

const search = () => screen.getByRole("combobox");
const type = (text: string) => fireEvent.change(search(), { target: { value: text } });
const press = (key: string) => fireEvent.keyDown(search(), { key });
const rowNames = () => screen.queryAllByRole("option").map((o) => o.textContent);

describe("CommandPalette: new thread in…", () => {
  it("goes project, folder, existing worktree from the keyboard alone", async () => {
    const { props } = setup();

    press("Enter"); // "New thread in…" leads the empty root
    expect(search().getAttribute("placeholder")).toBe("Which project?");
    // The default project is listed first, so it is the one Enter takes.
    press("Enter");
    expect(search().getAttribute("placeholder")).toBe("Which folder?");
    type("web");
    press("Enter");
    expect(search().getAttribute("placeholder")).toBe("Which checkout?");
    // Each step's field takes the keyboard, so typing carries straight on.
    expect(document.activeElement).toBe(search());
    expect(props.onListWorkspaces).toHaveBeenCalledWith("omni", "web");

    await screen.findByText("feat-x");
    // The project root is the main checkout, already offered once.
    expect(screen.queryByText("main")).toBeNull();
    expect(screen.getByText("in use: Other thread")).toBeTruthy();

    type("feat");
    press("Enter");
    expect(props.onNewThread).toHaveBeenCalledWith({
      projectId: "omni",
      folderId: "web",
      kind: "attach",
      attachPath: "/code/omni/.worktrees/feat-x",
      focus: true,
    });
    expect(props.onOpenChange).toHaveBeenCalledWith(false);
  });

  it("lets a busy worktree be picked anyway", async () => {
    const { props } = setup({ initialPage: "newThread" });
    type("omni");
    press("Enter");
    type("web");
    press("Enter");
    await screen.findByText("busy-branch");
    type("busy");
    press("Enter");
    expect(props.onNewThread).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "attach", attachPath: "/code/omni/.worktrees/busy" }),
    );
  });

  it("skips the folder step for a project with one folder", () => {
    const { props } = setup({ initialPage: "newThread" });
    type("solo");
    press("Enter");
    expect(search().getAttribute("placeholder")).toBe("Which checkout?");
    expect(props.onListWorkspaces).toHaveBeenCalledWith("solo", "solo-f");

    press("ArrowDown"); // Main checkout → New worktree
    press("Enter");
    expect(props.onNewThread).toHaveBeenCalledWith({
      projectId: "solo",
      folderId: "solo-f",
      kind: "branch",
      focus: true,
    });
  });

  it("finishes without a workspace step when the folder is not git", () => {
    const { props } = setup({ initialPage: "newThread" });
    type("notes");
    press("Enter");
    expect(props.onNewThread).toHaveBeenCalledWith({
      projectId: "notes",
      folderId: "notes-f",
      kind: "main",
      focus: true,
    });
    expect(props.onListWorkspaces).not.toHaveBeenCalled();
  });

  it("treats the whole project as no git question", () => {
    const { props } = setup({ initialPage: "newThread" });
    press("Enter"); // Omniplex, the default
    press("Enter"); // Everything, listed first
    expect(props.onNewThread).toHaveBeenCalledWith({
      projectId: "omni",
      folderId: "",
      kind: "main",
      focus: true,
    });
  });

  it("steps back one question per Backspace in an empty field", () => {
    setup({ initialPage: "newThread" });
    type("solo");
    press("Enter");
    expect(search().getAttribute("placeholder")).toBe("Which checkout?");

    // Typed text is edited, not a step back.
    type("x");
    press("Backspace");
    expect(search().getAttribute("placeholder")).toBe("Which checkout?");
    type("");

    // The folder was never asked, so back from the workspace is the project.
    press("Backspace");
    expect(search().getAttribute("placeholder")).toBe("Which project?");
    // Back from the first step is the root, even when it opened on this page.
    press("Backspace");
    expect(search().getAttribute("placeholder")).toBe("Search threads and actions…");
  });

  it("goes back from a worktree to the folder when there was a folder to pick", () => {
    setup({ initialPage: "newThread" });
    press("Enter"); // Omniplex
    type("web");
    press("Enter");
    expect(search().getAttribute("placeholder")).toBe("Which checkout?");
    press("Backspace");
    expect(search().getAttribute("placeholder")).toBe("Which folder?");
    press("Backspace");
    expect(search().getAttribute("placeholder")).toBe("Which project?");
  });

  it("says when the worktrees cannot be listed, and still offers the rest", async () => {
    const { props } = setup({
      initialPage: "newThread",
      onListWorkspaces: vi.fn(async () => {
        throw new Error("git exploded");
      }),
    });
    type("solo");
    press("Enter");
    expect((await screen.findByRole("alert")).textContent).toBe("Could not list worktrees: git exploded");
    press("Enter");
    expect(props.onNewThread).toHaveBeenCalledWith(expect.objectContaining({ kind: "main" }));
  });
});

describe("CommandPalette: root", () => {
  it("lists recent threads first, without the open one", () => {
    setup();
    const names = rowNames();
    expect(names[0]).toContain("New thread in…");
    const listed = names.filter((n) => n?.startsWith("Thread number"));
    // The eight most recent, newest first.
    expect(listed).toEqual(Array.from({ length: 8 }, (_, i) => `Thread number ${11 - i}`));
    expect(names.some((n) => n?.includes("The open one"))).toBe(false);
  });

  it("finds an older thread by title and opens it with Enter", () => {
    const { props } = setup();
    type("number 2");
    expect(rowNames()[0]).toBe("Thread number 2");
    press("Enter");
    expect(props.onSelectThread).toHaveBeenCalledWith("t2");
    expect(props.onOpenChange).toHaveBeenCalledWith(false);
  });

  it("runs an action found by a keyword, then closes", () => {
    const run = vi.fn();
    const other = vi.fn();
    const actions: PaletteAction[] = [
      { id: "settings", label: "Settings", group: "App", command: "settings", run: other },
      { id: "copy", label: "Copy transcript", group: "Thread", keywords: ["clipboard"], run },
    ];
    const { props } = setup({ actions });
    type("clipboard");
    expect(rowNames()).toEqual(["Copy transcript"]);
    press("Enter");
    expect(run).toHaveBeenCalledOnce();
    expect(other).not.toHaveBeenCalled();
    expect(props.onOpenChange).toHaveBeenCalledWith(false);
  });

  it("renames the open thread from its own page", () => {
    const onRename = vi.fn();
    const { props } = setup({ onRename });
    type("rename");
    press("Enter");
    const field = screen.getByLabelText("Thread title") as HTMLInputElement;
    expect(field.value).toBe("The open one");
    fireEvent.change(field, { target: { value: "  A better   name " } });
    fireEvent.keyDown(field, { key: "Enter" });
    expect(onRename).toHaveBeenCalledWith("A better name");
    expect(props.onOpenChange).toHaveBeenCalledWith(false);
  });

  it("leaves a rename with Escape without saving or closing", () => {
    const onRename = vi.fn();
    const { props } = setup({ onRename });
    type("rename");
    press("Enter");
    fireEvent.keyDown(screen.getByLabelText("Thread title"), { key: "Escape" });
    expect(search().getAttribute("placeholder")).toBe("Search threads and actions…");
    expect(onRename).not.toHaveBeenCalled();
    expect(props.onOpenChange).not.toHaveBeenCalled();
  });

  it("offers no rename without a handler", () => {
    setup();
    type("rename");
    expect(rowNames()).toEqual([]);
  });

  it("starts over on its initial page each time it opens", () => {
    const { props, rerender } = setup();
    press("Enter");
    press("Enter");
    expect(search().getAttribute("placeholder")).toBe("Which folder?");

    rerender(wrap(<CommandPalette {...props} open={false} />));
    rerender(wrap(<CommandPalette {...props} open />));
    expect(search().getAttribute("placeholder")).toBe("Search threads and actions…");
    expect((search() as HTMLInputElement).value).toBe("");

    rerender(wrap(<CommandPalette {...props} open={false} />));
    rerender(wrap(<CommandPalette {...props} initialPage="newThread" open />));
    expect(search().getAttribute("placeholder")).toBe("Which project?");
  });
});

describe("CommandPalette: layout", () => {
  const actions: PaletteAction[] = [
    { id: "settings", label: "Settings", group: "App", command: "settings", run: () => {} },
  ];

  it("shows key chips on a desktop with a pointer", () => {
    screenAs("desktop", true);
    setup({ actions });
    const dialog = screen.getByRole("dialog", { name: "Command palette" });
    expect(dialog.getAttribute("data-slot")).toBe("dialog-content");
    expect(dialog.querySelectorAll("kbd").length).toBeGreaterThan(0);
  });

  it("is a sheet with no key chips on a phone", () => {
    screenAs("phone", true);
    setup({ actions });
    const dialog = screen.getByRole("dialog", { name: "Command palette" });
    expect(dialog.getAttribute("data-slot")).toBe("sheet-content");
    expect(dialog.querySelectorAll("kbd")).toHaveLength(0);
  });

  it("has a back button for the pointer on later steps", () => {
    screenAs("phone", false);
    setup({ initialPage: "newThread" });
    type("omni");
    press("Enter");
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(search().getAttribute("placeholder")).toBe("Which project?");
  });
});
