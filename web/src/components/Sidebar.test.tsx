// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { loadDeleteThreadDialog } from "./loadDeleteThreadDialog";
import { Sidebar } from "./Sidebar";
import { render, viewport } from "~/test/harness";
import type { Project, ThreadMeta } from "~/protocol";

// The dialog loads after the first paint in the app; here it is on hand from
// the first render, so a test can open it and look straight away.
beforeAll(() => loadDeleteThreadDialog());

const thread = (id: string, over: Partial<ThreadMeta> = {}): ThreadMeta =>
  ({
    id,
    title: `Thread ${id}`,
    phase: "idle",
    updatedAt: Date.now(),
    cwd: "/tmp/repo",
    harness: "claude",
    projectId: "p1",
    branch: "main",
    ...over,
  }) as ThreadMeta;

const confirmDelete = (id: string) =>
  fireEvent.click(screen.getByRole("button", { name: `Delete thread Thread ${id}` }));
const checkbox = () => screen.queryByRole("checkbox", { name: /Also delete the worktree/ });

function renderSidebar(over: Partial<React.ComponentProps<typeof Sidebar>> = {}) {
  const props = {
    threads: [thread("a"), thread("b")],
    activeId: null as string | null,
    status: "online" as const,
    open: true,
    onOpenChange: vi.fn(),
    onSelect: vi.fn(),
    onNew: vi.fn(),
    onDelete: vi.fn(),
    onShowAccess: vi.fn(),
    onShowUsage: vi.fn(),
    onShowSkills: vi.fn(),
    onShowSettings: vi.fn(),
    accentOf: () => undefined,
    projects: [] as Project[],
    projectName: () => "repo",
    projectFolders: () => ["/tmp/repo"],
    onSetPinned: vi.fn(),
    onNewProject: vi.fn(),
    onSetUnread: vi.fn(),
    onRename: vi.fn(),
    onReorder: vi.fn(),
    ...over,
  };
  render(<Sidebar {...props} />);
  return props;
}

/**
 * The same sidebar, but with a threads list the test can change the way the
 * server would — which is the only way to watch a delete finish, because a
 * delete finishes when the thread leaves that list.
 */
function renderLive(threads: ThreadMeta[], over: Partial<React.ComponentProps<typeof Sidebar>> = {}) {
  let set: (s: ThreadMeta[]) => void = () => {};
  const props = {
    activeId: null as string | null,
    status: "online" as const,
    open: true,
    onOpenChange: vi.fn(),
    onSelect: vi.fn(),
    onNew: vi.fn(),
    onDelete: vi.fn(() => Promise.resolve()),
    onShowAccess: vi.fn(),
    onShowUsage: vi.fn(),
    onShowSkills: vi.fn(),
    onShowSettings: vi.fn(),
    accentOf: () => undefined,
    projects: [] as Project[],
    projectName: () => "repo",
    projectFolders: () => ["/tmp/repo"],
    onSetPinned: vi.fn(),
    onNewProject: vi.fn(),
    onSetUnread: vi.fn(),
    onRename: vi.fn(),
    onReorder: vi.fn(),
    ...over,
  };
  let setOpen: (open: boolean) => void = () => {};
  function Live() {
    const [list, setList] = useState(threads);
    const [open, setPanelOpen] = useState(true);
    set = setList;
    setOpen = setPanelOpen;
    return <Sidebar {...props} threads={list} open={open} />;
  }
  render(<Live />);
  return {
    props,
    serverSays: (next: ThreadMeta[]) => act(() => set(next)),
    setSidebarOpen: (open: boolean) => act(() => setOpen(open)),
  };
}

// Radix marks the rest of the document aria-hidden while a dialog is open, so
// role queries cannot see the rows behind it. The order of the rows is exactly
// what these tests are about, so they read the DOM directly.
const rowOrder = () =>
  Array.from(document.querySelectorAll('[aria-label^="Delete thread"]')).map((el) =>
    el.getAttribute("aria-label")!.replace("Delete thread ", ""),
  );

afterEach(() => vi.unstubAllGlobals());

describe("Sidebar", () => {
  it("offers no collapse control on a phone with nothing selected", () => {
    viewport("phone");
    renderSidebar({ activeId: null });

    expect(screen.getByRole("button", { name: "New thread" })).toBeTruthy();
    // Nothing behind the panel to collapse back to.
    expect(screen.queryByRole("button", { name: "Hide threads" })).toBeNull();
    // And no second, differently-shaped close control in the same corner.
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("offers the collapse control on a phone once a thread is selected", () => {
    viewport("phone");
    renderSidebar({ activeId: "a" });

    expect(screen.getByRole("button", { name: "Hide threads" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("always offers the collapse control when docked", () => {
    viewport("desktop");
    renderSidebar({ activeId: null });

    expect(screen.getByRole("button", { name: "Hide threads" })).toBeTruthy();
  });

  it("fills the viewport on a phone rather than leaving a sliver behind", () => {
    viewport("phone");
    renderSidebar({ activeId: "a" });

    const panel = document.querySelector("[data-slot=sheet-content]");
    expect(panel?.className).toContain("w-screen");
    expect(panel?.className).toContain("max-w-none");
    // The sheet's own base classes cap it at 24rem from `sm` up, which would
    // leave a 384px panel on a landscape phone — still below `md`, so still
    // inside this branch.
    expect(panel?.className).toContain("sm:max-w-none");
  });

  it("keeps the delete target thumb-sized without moving the glyph", () => {
    viewport("phone");
    renderSidebar({ activeId: "a" });

    const del = screen.getAllByRole("button", { name: /^Delete thread/ })[0];
    // The square stays 32px so it stays aligned with the logo below it; the
    // hit area is grown around it instead. 32 + 2*6 = 44.
    expect(del.className).toContain("size-8");
    expect(del.className).toContain("after:-inset-1.5");
    expect(del.className).toContain("md:after:hidden");
  });

  it("renames a thread in place from the row's pencil", () => {
    const props = renderSidebar();
    fireEvent.click(screen.getByRole("button", { name: "Rename thread Thread a" }));
    const field = screen.getByRole("textbox", { name: "Thread title" });
    fireEvent.change(field, { target: { value: "Release notes" } });
    fireEvent.keyDown(field, { key: "Enter" });
    expect(props.onRename).toHaveBeenCalledWith("a", "Release notes");
    // The row is a row again, not a field.
    expect(screen.queryByRole("textbox", { name: "Thread title" })).toBeNull();
    expect(props.onSelect).not.toHaveBeenCalled();
  });

  describe("reordering", () => {
    const ordered = [
      thread("a", { position: 0 }),
      thread("b", { position: 1 }),
      thread("c", { position: 2 }),
    ];

    const startReordering = async () => {
      fireEvent.pointerDown(screen.getByRole("button", { name: /^Filter threads/ }), {
        button: 0,
        ctrlKey: false,
      });
      fireEvent.click(await screen.findByRole("menuitem", { name: "Reorder threads" }));
    };

    it("swaps the row's controls for a grip, and a tap opens nothing until Done", async () => {
      viewport("phone");
      const props = renderSidebar({ threads: ordered });
      expect(screen.queryByRole("button", { name: /^Move thread/ })).toBeNull();
      await startReordering();

      expect(screen.queryByRole("button", { name: /^Delete thread/ })).toBeNull();
      expect(screen.queryByRole("button", { name: /^Rename thread/ })).toBeNull();
      expect(screen.getAllByRole("button", { name: /^Move thread/ })).toHaveLength(3);
      fireEvent.click(screen.getByText("Thread a"));
      expect(props.onSelect).not.toHaveBeenCalled();

      fireEvent.click(screen.getByRole("button", { name: "Done" }));
      expect(screen.queryByRole("button", { name: /^Move thread/ })).toBeNull();
      fireEvent.click(screen.getByText("Thread a"));
      expect(props.onSelect).toHaveBeenCalledWith("a");
    });

    it("steps a row with the arrow keys on its grip, writing only that row", async () => {
      const props = renderSidebar({ threads: ordered });
      await startReordering();

      fireEvent.keyDown(screen.getByRole("button", { name: "Move thread Thread a" }), {
        key: "ArrowDown",
      });
      expect(props.onReorder).toHaveBeenLastCalledWith("a", 1.5);
      fireEvent.keyDown(screen.getByRole("button", { name: "Move thread Thread a" }), {
        key: "ArrowUp",
      });
      // Already on top: nowhere to go, nothing sent.
      expect(props.onReorder).toHaveBeenCalledTimes(1);
    });
  });

  it("starts a project from the header", () => {
    const props = renderSidebar({ activeId: null });
    fireEvent.click(screen.getByRole("button", { name: "New project" }));
    expect(props.onNewProject).toHaveBeenCalled();
  });

  it("offers the worktree removal as a checkbox, ticked for one omniplex provisioned", () => {
    const managed = thread("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" });
    const props = renderSidebar({ threads: [managed], activeId: "a" });

    confirmDelete("a");
    const box = checkbox();
    expect(box).not.toBeNull();
    expect(box!.getAttribute("data-state")).toBe("checked");
    expect(screen.getByText("/tmp/repo/.worktrees/a")).toBeTruthy();
    // Branches are never deleted, and the copy says so.
    expect(screen.getByText(/is kept either way/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", true);
  });

  it("keeps the worktree when the box is unticked", () => {
    const managed = thread("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" });
    const props = renderSidebar({ threads: [managed], activeId: "a" });

    confirmDelete("a");
    fireEvent.click(checkbox()!);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("defaults a borrowed worktree to staying, and says omniplex did not make it", () => {
    const borrowed = thread("a", { workspaceMode: "borrowed", cwd: "/tmp/elsewhere" });
    const props = renderSidebar({ threads: [borrowed], activeId: "a" });

    confirmDelete("a");
    expect(checkbox()!.getAttribute("data-state")).toBe("unchecked");
    expect(screen.getByText(/omniplex did not create this worktree/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("does not offer removal while another thread is still in the worktree", () => {
    const shared = { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" };
    const props = renderSidebar({
      threads: [thread("a", shared), thread("b", shared)],
      activeId: "a",
    });

    confirmDelete("a");
    expect(checkbox()).toBeNull();
    expect(screen.getByText(/1 other thread/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("tells a main-checkout thread its checkout is untouched, and offers no box", () => {
    const props = renderSidebar({
      threads: [thread("a", { workspaceMode: "local" })],
      activeId: "a",
    });

    confirmDelete("a");
    expect(checkbox()).toBeNull();
    expect(screen.getByText(/checkout is left untouched/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("offers no removal for a managed thread that never got a worktree", () => {
    // Provisioning failed before `git worktree add` ran, so cwd is still the
    // project root — which the server refuses to remove.
    const props = renderSidebar({
      threads: [thread("a", { workspaceMode: "managed", phase: "provision_failed" })],
      activeId: "a",
    });

    confirmDelete("a");
    expect(checkbox()).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("counts a closed thread as still referencing the worktree", () => {
    const shared = { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" };
    const props = renderSidebar({
      threads: [thread("a", shared), thread("b", { ...shared, phase: "closed" })],
      activeId: "a",
    });

    confirmDelete("a");
    // omniplex still knows of b and b still names that path.
    expect(checkbox()).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });
  it("keeps the dialog open, and says so, until the delete actually finishes", () => {
    const { props, serverSays } = renderLive([thread("a"), thread("b")]);

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);

    // The server has only accepted the request; the workspace is still coming
    // down. Closing here would be claiming the thread is already gone.
    expect(screen.getByRole("button", { name: /Deleting/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel" }).hasAttribute("disabled")).toBe(true);

    serverSays([thread("b")]);
    expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
  });

  it("leaves the row where it is when the server bumps it while it cleans up", () => {
    const { serverSays } = renderLive([thread("a"), thread("b"), thread("c")]);
    expect(rowOrder()).toEqual(["Thread a", "Thread b", "Thread c"]);

    confirmDelete("b");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    // Entering "cleaning" restamps the thread, and the list is newest-first,
    // so the server now sends it back at the top. The row does not move.
    serverSays([
      thread("b", { phase: "cleaning", updatedAt: Date.now() + 1000 }),
      thread("a"),
      thread("c"),
    ]);
    expect(rowOrder()).toEqual(["Thread a", "Thread b", "Thread c"]);
  });

  it("holds the row in place for its exit animation, then drops it", () => {
    vi.useFakeTimers();
    try {
      const { serverSays } = renderLive([thread("a"), thread("b"), thread("c")]);

      confirmDelete("b");
      fireEvent.click(screen.getByRole("button", { name: "Delete" }));
      serverSays([thread("a"), thread("c")]);

      // Still there, in its own place, collapsing.
      expect(rowOrder()).toEqual(["Thread a", "Thread b", "Thread c"]);
      const row = document.querySelector('[aria-label="Delete thread Thread b"]')!;
      expect(row.closest(".grid")!.className).toContain("grid-rows-[0fr]");

      act(() => vi.advanceTimersByTime(500));
      expect(rowOrder()).toEqual(["Thread a", "Thread c"]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("stops waiting when the teardown fails, so the force-delete prompt is reachable", () => {
    const { serverSays } = renderLive([thread("a"), thread("b")]);

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    serverSays([thread("a", { phase: "cleanup_failed" }), thread("b")]);

    expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
  });

  it("closes the dialog when the server refuses the delete", async () => {
    const onDelete = vi.fn(() => Promise.reject(new Error("nope")));
    renderLive([thread("a")], { onDelete });

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await act(async () => {});

    expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
    expect(rowOrder()).toEqual(["Thread a"]);
  });

  it("holds the window while the delete runs, and lets go once the wait is abnormal", () => {
    vi.useFakeTimers();
    try {
      renderLive([thread("a"), thread("b")]);
      confirmDelete("a");
      fireEvent.click(screen.getByRole("button", { name: "Delete" }));

      // Escape would otherwise close it over a delete the user cannot see the
      // progress of anywhere else.
      fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
      expect(screen.getByRole("button", { name: /Deleting/ })).toBeTruthy();

      // A deprovision hook can hang forever; the dialog admits it and offers
      // the way out it was withholding.
      act(() => vi.advanceTimersByTime(11_000));
      expect(screen.getByText(/taking longer than usual/)).toBeTruthy();
      fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
      expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("settles the worktree answer once it has been sent", () => {
    const managed = thread("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" });
    renderLive([managed, thread("b")]);

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    // The request has already gone with the box ticked; unticking it now would
    // only make the dialog lie about what is happening on disk.
    expect(checkbox()!.hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: /Deleting worktree/ })).toBeTruthy();
  });

  it("keeps the delete on screen when the sidebar closes under it", () => {
    // On a phone, deleting a row selects it, and selecting closes the sheet —
    // which used to take the dialog, the pinned order and the animation with
    // it, because they lived inside the list.
    viewport("phone");
    const { serverSays, setSidebarOpen } = renderLive([thread("a"), thread("b")]);

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    setSidebarOpen(false);
    expect(screen.getByRole("button", { name: /Deleting/ })).toBeTruthy();

    serverSays([thread("b")]);
    expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
  });

  describe("pinned", () => {
    const projects = [
      { id: "p1", name: "omniplex", defaults: {}, folders: [], createdAt: 1, updatedAt: 1 },
      { id: "p2", name: "worksauce", defaults: {}, folders: [], createdAt: 1, updatedAt: 1 },
    ] as Project[];
    const header = (name: string, count: number) =>
      screen.queryByRole("button", { name: `${name}, ${count} thread${count === 1 ? "" : "s"}` });
    // In the user's order, the way the server sends them.
    const mixed = [
      thread("a", { projectId: "p1", position: 0 }),
      thread("b", { projectId: "p2", position: 1, pinned: true }),
      thread("c", { projectId: "p1", position: 2 }),
      thread("d", { projectId: "p1", position: 3, pinned: true }),
    ];

    afterEach(() => localStorage.clear());

    it("lifts pinned threads out of their groups into a section on top, in the user's order", () => {
      renderSidebar({ threads: mixed, projects });

      expect(header("Pinned", 2)).toBeTruthy();
      // "worksauce" has nothing left once its only thread is pinned.
      expect(header("worksauce", 1)).toBeNull();
      expect(header("omniplex", 2)).toBeNull();
      expect(rowOrder()).toEqual(["Thread b", "Thread d", "Thread a", "Thread c"]);
    });

    it("gives the flat list under Pinned a header of its own", () => {
      renderSidebar({ threads: mixed, projects: [projects[0]] });
      expect(header("Pinned", 2)).toBeTruthy();
      expect(header("Threads", 2)).toBeTruthy();
      expect(rowOrder()).toEqual(["Thread b", "Thread d", "Thread a", "Thread c"]);
    });

    it("has no Pinned section when nothing on screen is pinned", () => {
      localStorage.setItem("omniplex.projectFilter", JSON.stringify(["p2"]));
      renderSidebar({
        threads: [mixed[0], mixed[1], mixed[2]],
        projects,
      });
      expect(header("Pinned", 1)).toBeNull();
      expect(header("Threads", 2)).toBeNull();
      expect(rowOrder()).toEqual(["Thread a", "Thread c"]);
    });

    it("keeps a hidden project's pinned threads hidden", () => {
      localStorage.setItem("omniplex.projectFilter", JSON.stringify(["p2"]));
      renderSidebar({ threads: mixed, projects });
      expect(header("Pinned", 1)).toBeTruthy();
      expect(rowOrder()).toEqual(["Thread d", "Thread a", "Thread c"]);
    });

    it("pins and unpins from the row without opening the thread", () => {
      const props = renderSidebar({ threads: mixed, projects });
      fireEvent.click(screen.getByRole("button", { name: "Pin thread Thread a" }));
      expect(props.onSetPinned).toHaveBeenLastCalledWith("a", true);
      fireEvent.click(screen.getByRole("button", { name: "Unpin thread Thread b" }));
      expect(props.onSetPinned).toHaveBeenLastCalledWith("b", false);
      expect(props.onSelect).not.toHaveBeenCalled();
    });

    it("pins and unpins from the row's context menu", async () => {
      const props = renderSidebar({ threads: mixed, projects });
      fireEvent.contextMenu(screen.getByRole("button", { name: /^Thread a/ }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Pin" }));
      expect(props.onSetPinned).toHaveBeenLastCalledWith("a", true);
      fireEvent.contextMenu(screen.getByRole("button", { name: /^Thread b/ }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Unpin" }));
      expect(props.onSetPinned).toHaveBeenLastCalledWith("b", false);
    });

    it("reorders inside Pinned against the pinned neighbours only", async () => {
      const props = renderSidebar({ threads: mixed, projects });
      fireEvent.pointerDown(screen.getByRole("button", { name: /^Filter threads/ }), {
        button: 0,
        ctrlKey: false,
      });
      fireEvent.click(await screen.findByRole("menuitem", { name: "Reorder threads" }));

      // "c" sits between them in the whole list; it is not in this run.
      fireEvent.keyDown(screen.getByRole("button", { name: "Move thread Thread b" }), {
        key: "ArrowDown",
      });
      expect(props.onReorder).toHaveBeenLastCalledWith("b", 4);
      fireEvent.keyDown(screen.getByRole("button", { name: "Move thread Thread d" }), {
        key: "ArrowUp",
      });
      expect(props.onReorder).toHaveBeenLastCalledWith("d", 0);
    });

    it("folds a departing pinned row away inside Pinned, taking the section with it", () => {
      vi.useFakeTimers();
      try {
        const { serverSays } = renderLive([
          thread("a", { pinned: true }),
          thread("b"),
          thread("c"),
        ]);

        confirmDelete("a");
        fireEvent.click(screen.getByRole("button", { name: "Delete" }));
        serverSays([thread("b"), thread("c")]);

        expect(rowOrder()).toEqual(["Thread a", "Thread b", "Thread c"]);
        const section = header("Pinned", 1)!.closest(".grid")!;
        expect(section.className).toContain("grid-rows-[0fr]");

        act(() => vi.advanceTimersByTime(500));
        expect(rowOrder()).toEqual(["Thread b", "Thread c"]);
        expect(header("Pinned", 1)).toBeNull();
      } finally {
        vi.useRealTimers();
      }
    });
  });
  describe("projects", () => {
    const projects = [
      { id: "p1", name: "omniplex", defaults: {}, folders: [], createdAt: 1, updatedAt: 1 },
      { id: "p2", name: "worksauce", defaults: {}, folders: [], createdAt: 1, updatedAt: 1 },
    ] as Project[];
    // In the user's order, the way the server sends them.
    const mixed = [
      thread("a", { projectId: "p2" }),
      thread("b", { projectId: "p1" }),
      thread("c", { projectId: "p2" }),
    ];
    const header = (name: string, count: number) =>
      screen.queryByRole("button", { name: `${name}, ${count} thread${count === 1 ? "" : "s"}` });

    afterEach(() => localStorage.clear());

    it("groups under headers once two projects have threads on screen", () => {
      renderSidebar({ threads: mixed, projects });

      // "omniplex" leads because it leads the projects, though "worksauce"
      // has the first thread.
      expect(header("worksauce", 2)).toBeTruthy();
      expect(header("omniplex", 1)).toBeTruthy();
      expect(rowOrder()).toEqual(["Thread b", "Thread a", "Thread c"]);
    });

    it("lists every project's threads flat, in the user's order, with grouping off", async () => {
      renderSidebar({ threads: mixed, projects });
      fireEvent.pointerDown(screen.getByRole("button", { name: "Filter threads" }), {
        button: 0,
        ctrlKey: false,
      });
      fireEvent.click(await screen.findByRole("menuitemcheckbox", { name: "Group by project" }));

      expect(header("worksauce", 2)).toBeNull();
      expect(header("omniplex", 1)).toBeNull();
      expect(rowOrder()).toEqual(["Thread a", "Thread b", "Thread c"]);
    });

    it("shows no header when every thread on screen is one project's", () => {
      renderSidebar({ threads: [thread("a"), thread("b")], projects });
      expect(header("omniplex", 2)).toBeNull();
      expect(rowOrder()).toEqual(["Thread a", "Thread b"]);
    });

    it("drops the grouping when the filter leaves one project populated", () => {
      localStorage.setItem("omniplex.projectFilter", JSON.stringify(["p2"]));
      renderSidebar({ threads: mixed, projects });

      expect(rowOrder()).toEqual(["Thread b"]);
      expect(header("omniplex", 1)).toBeNull();
      expect(screen.getByText("1 of 3 threads")).toBeTruthy();
      expect(screen.getByRole("button", { name: "Filter threads, 1 hidden" })).toBeTruthy();
    });

    it("offers no project choices when there is nothing to choose between", async () => {
      renderSidebar({ threads: mixed, projects: [projects[0]] });
      fireEvent.pointerDown(screen.getByRole("button", { name: "Filter threads" }), {
        button: 0,
        ctrlKey: false,
      });
      await screen.findByRole("menu");
      expect(screen.queryByRole("menuitemcheckbox", { name: "All projects" })).toBeNull();
    });

    it("folds a group shut, and remembers it", () => {
      renderSidebar({ threads: mixed, projects });

      fireEvent.click(header("worksauce", 2)!);
      // The header stays — it is the way back — but its rows are gone.
      expect(header("worksauce", 2)).toBeTruthy();
      expect(rowOrder()).toEqual(["Thread b"]);
      expect(localStorage.getItem("omniplex.projectCollapsed")).toBe(JSON.stringify(["p2"]));
    });

    it("comes back folded exactly where it was left", () => {
      localStorage.setItem("omniplex.projectCollapsed", JSON.stringify(["p1"]));
      renderSidebar({ threads: mixed, projects });

      expect(rowOrder()).toEqual(["Thread a", "Thread c"]);
      expect(header("omniplex", 1)!.getAttribute("aria-expanded")).toBe("false");
      expect(header("worksauce", 2)!.getAttribute("aria-expanded")).toBe("true");
    });

    it("ignores a stored id whose project has since been removed", () => {
      localStorage.setItem("omniplex.projectFilter", JSON.stringify(["p2"]));
      renderSidebar({ threads: mixed, projects: [projects[0]] });
      // The orphaned threads come back, grouped after the projects that exist.
      expect(rowOrder()).toEqual(["Thread b", "Thread a", "Thread c"]);
    });
  });
});
