// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { Sidebar } from "./Sidebar";
import { render, viewport } from "~/test/harness";
import type { Label, Project, SessionMeta } from "~/protocol";

const session = (id: string, over: Partial<SessionMeta> = {}): SessionMeta =>
  ({
    id,
    title: `Session ${id}`,
    phase: "idle",
    updatedAt: Date.now(),
    cwd: "/tmp/repo",
    harness: "claude",
    projectId: "p1",
    branch: "main",
    ...over,
  }) as SessionMeta;

const confirmDelete = (id: string) =>
  fireEvent.click(screen.getByRole("button", { name: `Delete session Session ${id}` }));
const checkbox = () => screen.queryByRole("checkbox", { name: /Also delete the worktree/ });

function renderSidebar(over: Partial<React.ComponentProps<typeof Sidebar>> = {}) {
  const props = {
    sessions: [session("a"), session("b")],
    activeId: null as string | null,
    status: "online" as const,
    open: true,
    onOpenChange: vi.fn(),
    onSelect: vi.fn(),
    onNew: vi.fn(),
    onDelete: vi.fn(),
    onShowAccess: vi.fn(),
    accentOf: () => undefined,
    projects: [] as Project[],
    projectName: () => "repo",
    projectRoot: () => "/tmp/repo",
    labels: [],
    onSetLabel: vi.fn(),
    onManageLabels: vi.fn(),
    ...over,
  };
  render(<Sidebar {...props} />);
  return props;
}

/**
 * The same sidebar, but with a sessions list the test can change the way the
 * server would — which is the only way to watch a delete finish, because a
 * delete finishes when the session leaves that list.
 */
function renderLive(sessions: SessionMeta[], over: Partial<React.ComponentProps<typeof Sidebar>> = {}) {
  let set: (s: SessionMeta[]) => void = () => {};
  const props = {
    activeId: null as string | null,
    status: "online" as const,
    open: true,
    onOpenChange: vi.fn(),
    onSelect: vi.fn(),
    onNew: vi.fn(),
    onDelete: vi.fn(() => Promise.resolve()),
    onShowAccess: vi.fn(),
    accentOf: () => undefined,
    projects: [] as Project[],
    projectName: () => "repo",
    projectRoot: () => "/tmp/repo",
    labels: [],
    onSetLabel: vi.fn(),
    onManageLabels: vi.fn(),
    ...over,
  };
  let setOpen: (open: boolean) => void = () => {};
  function Live() {
    const [list, setList] = useState(sessions);
    const [open, setPanelOpen] = useState(true);
    set = setList;
    setOpen = setPanelOpen;
    return <Sidebar {...props} sessions={list} open={open} />;
  }
  render(<Live />);
  return {
    props,
    serverSays: (next: SessionMeta[]) => act(() => set(next)),
    setSidebarOpen: (open: boolean) => act(() => setOpen(open)),
  };
}

// Radix marks the rest of the document aria-hidden while a dialog is open, so
// role queries cannot see the rows behind it. The order of the rows is exactly
// what these tests are about, so they read the DOM directly.
const rowOrder = () =>
  Array.from(document.querySelectorAll('[aria-label^="Delete session"]')).map((el) =>
    el.getAttribute("aria-label")!.replace("Delete session ", ""),
  );

afterEach(() => vi.unstubAllGlobals());

describe("Sidebar", () => {
  it("offers no collapse control on a phone with nothing selected", () => {
    viewport("phone");
    renderSidebar({ activeId: null });

    expect(screen.getByRole("button", { name: "New session" })).toBeTruthy();
    // Nothing behind the panel to collapse back to.
    expect(screen.queryByRole("button", { name: "Hide sessions" })).toBeNull();
    // And no second, differently-shaped close control in the same corner.
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("offers the collapse control on a phone once a session is selected", () => {
    viewport("phone");
    renderSidebar({ activeId: "a" });

    expect(screen.getByRole("button", { name: "Hide sessions" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
  });

  it("always offers the collapse control when docked", () => {
    viewport("desktop");
    renderSidebar({ activeId: null });

    expect(screen.getByRole("button", { name: "Hide sessions" })).toBeTruthy();
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

    const del = screen.getAllByRole("button", { name: /^Delete session/ })[0];
    // The square stays 32px so it stays aligned with the logo below it; the
    // hit area is grown around it instead. 32 + 2*6 = 44.
    expect(del.className).toContain("size-8");
    expect(del.className).toContain("after:-inset-1.5");
    expect(del.className).toContain("md:after:hidden");
  });

  it("puts the collapse control right after the new-session button, as when docked", () => {
    viewport("phone");
    renderSidebar({ activeId: "a" });
    const names = Array.from(document.querySelectorAll("[data-slot=sheet-content] button"))
      .map((b) => b.getAttribute("aria-label"))
      .filter(Boolean);
    // Adjacent, in that order — the docked panel's arrangement, not a lone X
    // floating in the corner above it.
    expect(names.indexOf("Hide sessions")).toBe(names.indexOf("New session") + 1);
  });

  it("offers the worktree removal as a checkbox, ticked for one omniplex provisioned", () => {
    const managed = session("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" });
    const props = renderSidebar({ sessions: [managed], activeId: "a" });

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
    const managed = session("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" });
    const props = renderSidebar({ sessions: [managed], activeId: "a" });

    confirmDelete("a");
    fireEvent.click(checkbox()!);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("defaults a borrowed worktree to staying, and says omniplex did not make it", () => {
    const borrowed = session("a", { workspaceMode: "borrowed", cwd: "/tmp/elsewhere" });
    const props = renderSidebar({ sessions: [borrowed], activeId: "a" });

    confirmDelete("a");
    expect(checkbox()!.getAttribute("data-state")).toBe("unchecked");
    expect(screen.getByText(/omniplex did not create this worktree/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("does not offer removal while another session is still in the worktree", () => {
    const shared = { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" };
    const props = renderSidebar({
      sessions: [session("a", shared), session("b", shared)],
      activeId: "a",
    });

    confirmDelete("a");
    expect(checkbox()).toBeNull();
    expect(screen.getByText(/1 other session/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("tells a main-checkout session its checkout is untouched, and offers no box", () => {
    const props = renderSidebar({
      sessions: [session("a", { workspaceMode: "local" })],
      activeId: "a",
    });

    confirmDelete("a");
    expect(checkbox()).toBeNull();
    expect(screen.getByText(/checkout is left untouched/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("offers no removal for a managed session that never got a worktree", () => {
    // Provisioning failed before `git worktree add` ran, so cwd is still the
    // project root — which the server refuses to remove.
    const props = renderSidebar({
      sessions: [session("a", { workspaceMode: "managed", phase: "provision_failed" })],
      activeId: "a",
    });

    confirmDelete("a");
    expect(checkbox()).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });

  it("counts a closed session as still referencing the worktree", () => {
    const shared = { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" };
    const props = renderSidebar({
      sessions: [session("a", shared), session("b", { ...shared, phase: "closed" })],
      activeId: "a",
    });

    confirmDelete("a");
    // omniplex still knows of b and b still names that path.
    expect(checkbox()).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);
  });
  it("keeps the dialog open, and says so, until the delete actually finishes", () => {
    const { props, serverSays } = renderLive([session("a"), session("b")]);

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith("a", false);

    // The server has only accepted the request; the workspace is still coming
    // down. Closing here would be claiming the session is already gone.
    expect(screen.getByRole("button", { name: /Deleting/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Cancel" }).hasAttribute("disabled")).toBe(true);

    serverSays([session("b")]);
    expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
  });

  it("leaves the row where it is when the server bumps it while it cleans up", () => {
    const { serverSays } = renderLive([session("a"), session("b"), session("c")]);
    expect(rowOrder()).toEqual(["Session a", "Session b", "Session c"]);

    confirmDelete("b");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    // Entering "cleaning" restamps the session, and the list is newest-first,
    // so the server now sends it back at the top. The row does not move.
    serverSays([
      session("b", { phase: "cleaning", updatedAt: Date.now() + 1000 }),
      session("a"),
      session("c"),
    ]);
    expect(rowOrder()).toEqual(["Session a", "Session b", "Session c"]);
  });

  it("holds the row in place for its exit animation, then drops it", () => {
    vi.useFakeTimers();
    try {
      const { serverSays } = renderLive([session("a"), session("b"), session("c")]);

      confirmDelete("b");
      fireEvent.click(screen.getByRole("button", { name: "Delete" }));
      serverSays([session("a"), session("c")]);

      // Still there, in its own place, collapsing.
      expect(rowOrder()).toEqual(["Session a", "Session b", "Session c"]);
      const row = document.querySelector('[aria-label="Delete session Session b"]')!;
      expect(row.closest(".grid")!.className).toContain("grid-rows-[0fr]");

      act(() => vi.advanceTimersByTime(500));
      expect(rowOrder()).toEqual(["Session a", "Session c"]);
    } finally {
      vi.useRealTimers();
    }
  });

  it("stops waiting when the teardown fails, so the force-delete prompt is reachable", () => {
    const { serverSays } = renderLive([session("a"), session("b")]);

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    serverSays([session("a", { phase: "cleanup_failed" }), session("b")]);

    expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete" })).toBeNull();
  });

  it("closes the dialog when the server refuses the delete", async () => {
    const onDelete = vi.fn(() => Promise.reject(new Error("nope")));
    renderLive([session("a")], { onDelete });

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await act(async () => {});

    expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
    expect(rowOrder()).toEqual(["Session a"]);
  });

  it("holds the window while the delete runs, and lets go once the wait is abnormal", () => {
    vi.useFakeTimers();
    try {
      renderLive([session("a"), session("b")]);
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
    const managed = session("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" });
    renderLive([managed, session("b")]);

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
    const { serverSays, setSidebarOpen } = renderLive([session("a"), session("b")]);

    confirmDelete("a");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    setSidebarOpen(false);
    expect(screen.getByRole("button", { name: /Deleting/ })).toBeTruthy();

    serverSays([session("b")]);
    expect(screen.queryByRole("button", { name: /Deleting/ })).toBeNull();
  });

  describe("labels", () => {
    const labels: Label[] = [
      { id: "l1", name: "Parked", color: "#8d8d8d", position: 0, createdAt: 1 },
      { id: "l2", name: "In progress", color: "#0091ff", position: 1, createdAt: 2 },
    ];
    const filed = [session("a", { labelId: "l1" }), session("b", { labelId: "l2" }), session("c")];

    afterEach(() => localStorage.clear());

    it("names the label on its dot, and offers filing on an unlabelled row", () => {
      // The name is not in the row any more — it is the accessible name of the
      // dot, which is also what the tooltip says on hover.
      renderSidebar({ sessions: filed, labels });
      expect(screen.getByRole("button", { name: "Labelled Parked — change label" })).toBeTruthy();
      expect(screen.getByRole("button", { name: "Label session Session c" })).toBeTruthy();
    });

    it("carries one label control in the header, and it is the filter", () => {
      renderSidebar({ sessions: filed, labels });
      expect(screen.getByRole("button", { name: "Filter by label" })).toBeTruthy();
      // The old second button — a bare "Labels" that opened the manager — is
      // gone; the manager is an item inside the filter menu now.
      expect(screen.queryByRole("button", { name: "Labels" })).toBeNull();
    });

    it("hides the sessions under a switched-off label, and says so in the footer", () => {
      localStorage.setItem("omniplex.labelFilter", JSON.stringify(["l1"]));
      renderSidebar({ sessions: filed, labels });

      expect(rowOrder()).toEqual(["Session b", "Session c"]);
      expect(screen.getByText("2 of 3 sessions")).toBeTruthy();
      expect(screen.getByRole("button", { name: "Filter by label — 1 hidden" })).toBeTruthy();
    });

    it("switches unlabelled sessions off on their own", () => {
      localStorage.setItem("omniplex.labelFilter", JSON.stringify(["none"]));
      renderSidebar({ sessions: filed, labels });
      expect(rowOrder()).toEqual(["Session a", "Session b"]);
    });

    it("offers the way back when the filter has hidden everything", () => {
      // Filtered to nothing is not "no sessions yet": the sessions are there,
      // and with no groups left on screen the message is the only thing that
      // can say so.
      localStorage.setItem("omniplex.labelFilter", JSON.stringify(["l1", "l2", "none"]));
      renderSidebar({ sessions: filed, labels });
      expect(screen.getByText("3 sessions hidden by the filters.")).toBeTruthy();

      fireEvent.click(screen.getByRole("button", { name: "Show all" }));
      expect(rowOrder()).toEqual(["Session a", "Session b", "Session c"]);
      expect(localStorage.getItem("omniplex.labelFilter")).toBe("[]");
    });

    it("ignores a stored id whose label has since been deleted", () => {
      localStorage.setItem("omniplex.labelFilter", JSON.stringify(["l2"]));
      renderSidebar({ sessions: filed, labels: [labels[0]] });
      // "b" was filed under the deleted label, so it is unlabelled now — and
      // unlabelled is showing.
      expect(rowOrder()).toEqual(["Session a", "Session b", "Session c"]);
      expect(screen.getByRole("button", { name: "Filter by label" })).toBeTruthy();
    });
  });
  describe("projects", () => {
    const projects = [
      { id: "p1", root: "/src/omniplex", config: { name: "omniplex" }, createdAt: 1, updatedAt: 1 },
      { id: "p2", root: "/src/worksauce", config: { name: "worksauce" }, createdAt: 1, updatedAt: 1 },
    ] as Project[];
    // Most recently updated first, the way the server sends them.
    const mixed = [
      session("a", { projectId: "p2" }),
      session("b", { projectId: "p1" }),
      session("c", { projectId: "p2" }),
    ];
    const header = (name: string, count: number) =>
      screen.queryByRole("button", { name: `${name}, ${count} session${count === 1 ? "" : "s"}` });

    afterEach(() => localStorage.clear());

    it("groups under headers once two projects have sessions on screen", () => {
      renderSidebar({ sessions: mixed, projects });

      // "worksauce" leads because its newest session is the newest session.
      expect(header("worksauce", 2)).toBeTruthy();
      expect(header("omniplex", 1)).toBeTruthy();
      expect(rowOrder()).toEqual(["Session a", "Session c", "Session b"]);
    });

    it("shows no header when every session on screen is one project's", () => {
      renderSidebar({ sessions: [session("a"), session("b")], projects });
      expect(header("omniplex", 2)).toBeNull();
      expect(rowOrder()).toEqual(["Session a", "Session b"]);
    });

    it("drops the grouping when the filter leaves one project populated", () => {
      localStorage.setItem("omniplex.projectFilter", JSON.stringify(["p2"]));
      renderSidebar({ sessions: mixed, projects });

      expect(rowOrder()).toEqual(["Session b"]);
      expect(header("omniplex", 1)).toBeNull();
      expect(screen.getByText("1 of 3 sessions")).toBeTruthy();
      expect(screen.getByRole("button", { name: "Filter by project — 1 hidden" })).toBeTruthy();
    });

    it("offers no project filter when there is nothing to choose between", () => {
      renderSidebar({ sessions: mixed, projects: [projects[0]] });
      expect(screen.queryByRole("button", { name: /Filter by project/ })).toBeNull();
    });

    it("folds a group shut, and remembers it", () => {
      renderSidebar({ sessions: mixed, projects });

      fireEvent.click(header("worksauce", 2)!);
      // The header stays — it is the way back — but its rows are gone.
      expect(header("worksauce", 2)).toBeTruthy();
      expect(rowOrder()).toEqual(["Session b"]);
      expect(localStorage.getItem("omniplex.projectCollapsed")).toBe(JSON.stringify(["p2"]));
    });

    it("comes back folded exactly where it was left", () => {
      localStorage.setItem("omniplex.projectCollapsed", JSON.stringify(["p1"]));
      renderSidebar({ sessions: mixed, projects });

      expect(rowOrder()).toEqual(["Session a", "Session c"]);
      expect(header("omniplex", 1)!.getAttribute("aria-expanded")).toBe("false");
      expect(header("worksauce", 2)!.getAttribute("aria-expanded")).toBe("true");
    });

    it("ignores a stored id whose project has since been removed", () => {
      localStorage.setItem("omniplex.projectFilter", JSON.stringify(["p2"]));
      renderSidebar({ sessions: mixed, projects: [projects[0]] });
      expect(rowOrder()).toEqual(["Session a", "Session c", "Session b"]);
    });
  });
});
