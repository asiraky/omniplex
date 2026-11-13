// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { Panel, type PanelProps, type PanelRequest } from "~/components/panel/Panel";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "~/components/ui/dropdown-menu";
import { emptyState } from "~/apply";
import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import { usePanel, type PanelControls } from "~/app/usePanel";
import { savePanel } from "~/lib/panel";
import type { Job, ThreadChanges } from "~/protocol";
import { render, viewport, wrap } from "~/test/harness";

function renderPanel(onClose: () => void) {
  const never = () => new Promise<never>(() => {});
  render(
    <>
      <Panel
        threadId="s1"
        command={never}
        open
        onClose={onClose}
        revision="r"
        loadChanges={never}
        loadDiff={never}
        loadTree={never}
        loadFile={never}
        onRequestRouted={() => {}}
      />
      <DropdownMenu>
        <DropdownMenuTrigger>Menu</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </>,
  );
}

describe("Panel", () => {
  it("closes on Escape", () => {
    const onClose = vi.fn();
    renderPanel(onClose);
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("leaves Escape to an open menu, which closes alone", async () => {
    const onClose = vi.fn();
    renderPanel(onClose);
    fireEvent.pointerDown(screen.getByRole("button", { name: "Menu" }), { button: 0, ctrlKey: false });
    const item = await screen.findByRole("menuitem", { name: "Item" });
    fireEvent.keyDown(item, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("menuitem")).toBeNull());
    expect(onClose).not.toHaveBeenCalled();
  });
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function changesWith(...paths: string[]): ThreadChanges {
  return {
    root: "/wt",
    mode: "uncommitted",
    additions: 1,
    deletions: 0,
    files: paths.map((path) => ({ path, status: "modified" as const, additions: 1, deletions: 0 })),
  };
}

function props(overrides: Partial<PanelProps> = {}): PanelProps {
  const never = () => new Promise<never>(() => {});
  return {
    threadId: "s1",
    command: never,
    open: true,
    onClose: () => {},
    revision: "r",
    loadChanges: never,
    loadDiff: never,
    loadTree: never,
    loadFile: never,
    onRequestRouted: () => {},
    ...overrides,
  };
}

function selectedTab() {
  return screen.getAllByRole("tab").find((t) => t.getAttribute("aria-selected") === "true");
}

describe("Panel requests", () => {
  beforeEach(() => {
    localStorage.clear();
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("holds a path request until the change list lands, then opens a changed path as its diff", async () => {
    const changes = deferred<ThreadChanges>();
    const loadDiff = vi.fn(() => new Promise<never>(() => {}));
    savePanel("s1", { surfaces: [{ id: "jobs", kind: "jobs" }], active: "jobs" });
    const request: PanelRequest = { kind: "path", path: "src/a.ts", nonce: 1 };
    render(<Panel {...props({ loadChanges: () => changes.promise, loadDiff, request })} />);

    expect(selectedTab()?.textContent).toBe("Jobs");
    await act(async () => changes.resolve(changesWith("src/a.ts")));

    expect(selectedTab()?.textContent).toBe("Diff");
    await waitFor(() => expect(loadDiff).toHaveBeenCalledWith("src/a.ts", expect.anything()));
  });

  it("opens a path outside the change list as the file, and routes each nonce once", async () => {
    const loadFile = vi.fn(() => new Promise<never>(() => {}));
    const base = props({ loadChanges: () => Promise.resolve(changesWith()), loadFile });
    const { rerender } = render(<Panel {...base} request={{ kind: "path", path: "src/b.ts", nonce: 1 }} />);

    await waitFor(() => expect(selectedTab()?.textContent).toBe("b.ts"));
    expect(loadFile).toHaveBeenCalledWith("src/b.ts");

    // Moving away and re-rendering with the same request must not drag the
    // user back to the file.
    fireEvent.click(screen.getByRole("tab", { name: /Diff/ }));
    rerender(wrap(<Panel {...base} request={{ kind: "path", path: "src/b.ts", nonce: 1 }} />));
    expect(selectedTab()?.textContent).toBe("Diff");
  });

  it("reads the tree once when gitignored files are toggled, even when React replays updaters", async () => {
    savePanel("s1", { surfaces: [{ id: "files", kind: "files" }], active: "files" });
    const loadTree = vi.fn((_ignored: boolean) => Promise.resolve({ root: "/wt", files: ["a.ts"] }));
    render(
      <StrictMode>
        <Panel {...props({ loadTree })} />
      </StrictMode>,
    );
    await screen.findByText("a.ts");
    loadTree.mockClear();

    fireEvent.click(screen.getByRole("button", { name: "Show gitignored files" }));
    await screen.findByRole("button", { name: "Hide gitignored files" });

    expect(loadTree.mock.calls).toEqual([[true]]);
  });
});

describe("Panel requests across remounts", () => {
  beforeEach(() => {
    localStorage.clear();
    Element.prototype.scrollIntoView = vi.fn();
    // Below the docking width the panel is a sheet, whose body unmounts
    // when it closes.
    viewport("phone");
  });

  // The panel as the app wires it: usePanel owns the request, the panel
  // mounts and unmounts under it.
  function renderOwned() {
    let controls!: PanelControls;
    const threadStore = createThreadStore(null);
    function Host() {
      const panel = usePanel(threadStore);
      controls = panel;
      return (
        <Panel
          {...props({ loadChanges: () => Promise.resolve(changesWith()) })}
          open={panel.open}
          onClose={panel.close}
          request={panel.request}
          onRequestRouted={panel.requestRouted}
        />
      );
    }
    render(<Host />);
    return () => controls;
  }

  it("routes a request made while the panel is unmounted on the mount it opens", async () => {
    savePanel("s1", { surfaces: [{ id: "jobs", kind: "jobs" }], active: "jobs" });
    const panel = renderOwned();
    expect(screen.queryByRole("tablist")).toBeNull();

    act(() => panel().openPath("src/b.ts"));
    await waitFor(() => expect(selectedTab()?.textContent).toBe("b.ts"));
  });

  it("does not reopen a closed file tab when the panel remounts, and still routes the next request", async () => {
    savePanel("s1", { surfaces: [{ id: "jobs", kind: "jobs" }], active: "jobs" });
    const panel = renderOwned();
    act(() => panel().openPath("src/b.ts"));
    await waitFor(() => expect(selectedTab()?.textContent).toBe("b.ts"));

    fireEvent.click(screen.getByRole("button", { name: "Close b.ts" }));
    fireEvent.click(screen.getByRole("button", { name: "Close the panel" }));
    await waitFor(() => expect(screen.queryByRole("tablist")).toBeNull());
    act(() => panel().show());

    await screen.findByRole("tablist");
    // Give a replayed path request the change list it waits for.
    await act(async () => {});
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual(["Jobs"]);

    // Asking for the same file again is a new request, and is routed.
    act(() => panel().openPath("src/b.ts"));
    await waitFor(() => expect(selectedTab()?.textContent).toBe("b.ts"));
  });
});

describe("Panel jobs request", () => {
  beforeEach(() => {
    localStorage.clear();
    Element.prototype.scrollIntoView = vi.fn();
  });

  it("opens the jobs tab on the job a request names", () => {
    const agent: Job = { id: "j1", toolCallId: "task", depth: 0, kind: "agent", name: "Explore", status: "running", usage: {} };
    const threadStore = createThreadStore({
      ...emptyState("s1"),
      jobs: [agent],
      items: [{ id: "task", kind: "tool" }, { id: "m1", kind: "message", role: "agent", parentId: "task", text: "exploring" }],
    });
    savePanel("s1", { surfaces: [{ id: "diff", kind: "diff" }], active: "diff" });
    let controls!: PanelControls;
    function Host() {
      const panel = usePanel(threadStore);
      controls = panel;
      return (
        <Panel {...props()} open={panel.open} request={panel.request} onRequestRouted={panel.requestRouted} />
      );
    }
    render(
      <ThreadStoreContext.Provider value={threadStore}>
        <Host />
      </ThreadStoreContext.Provider>,
    );

    act(() => controls.openJobs("j1"));
    expect(selectedTab()?.textContent).toMatch(/^Jobs/);
    expect(screen.getByText("exploring")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Back to jobs" })).toBeTruthy();

    // Leaving the tab spends the focus: coming back shows the roster.
    fireEvent.click(screen.getByRole("tab", { name: /Diff/ }));
    fireEvent.click(screen.getByRole("tab", { name: /Jobs/ }));
    expect(screen.queryByRole("button", { name: "Back to jobs" })).toBeNull();
    expect(screen.getByRole("button", { name: "Stop Explore" })).toBeTruthy();
  });

  it("goes back to the roster when the strip asks after a row opened an agent", () => {
    const agent: Job = {
      id: "j1",
      toolCallId: "task",
      depth: 0,
      kind: "agent",
      name: "Explore",
      status: "running",
      usage: {},
    };
    const threadStore = createThreadStore({
      ...emptyState("s1"),
      jobs: [agent],
      items: [{ id: "task", kind: "tool" }],
    });
    savePanel("s1", {
      surfaces: [{ id: "diff", kind: "diff" }],
      active: "diff",
    });
    let controls!: PanelControls;
    function Host() {
      const panel = usePanel(threadStore);
      controls = panel;
      return (
        <Panel
          {...props()}
          open={panel.open}
          request={panel.request}
          onRequestRouted={panel.requestRouted}
        />
      );
    }
    render(
      <ThreadStoreContext.Provider value={threadStore}>
        <Host />
      </ThreadStoreContext.Provider>,
    );

    act(() => controls.openJobs("j1"));
    expect(screen.getByRole("button", { name: "Back to jobs" })).toBeTruthy();
    act(() => controls.openJobs());
    expect(screen.queryByRole("button", { name: "Back to jobs" })).toBeNull();
    expect(screen.getByRole("button", { name: "Stop Explore" })).toBeTruthy();
  });
});

describe("Panel turn diff", () => {
  beforeEach(() => {
    localStorage.clear();
    Element.prototype.scrollIntoView = vi.fn();
  });

  const KEY = "omniplex.diffComparison:s1";
  const trigger = () => screen.getByRole("combobox", { name: "Diff comparison" });

  it("shows a card's turn, and switching away neither forgets it nor stores it", async () => {
    const loadChanges = vi.fn((_c: string, _t?: string) => Promise.resolve(changesWith("src/a.ts")));
    const loadDiff = vi.fn(() => new Promise<never>(() => {}));
    const request: PanelRequest = { kind: "diff", path: "src/a.ts", turn: { id: "t2", number: 2 }, nonce: 1 };
    render(<Panel {...props({ loadChanges, loadDiff, request })} />);

    await waitFor(() => expect(loadDiff).toHaveBeenCalledWith("src/a.ts", expect.anything()));
    expect(loadChanges.mock.calls).toEqual([["turn", "t2"]]);
    expect(trigger().textContent).toBe("Turn 2");
    expect(localStorage.getItem(KEY)).toBeNull();

    fireEvent.click(trigger());
    fireEvent.click(await screen.findByRole("option", { name: "Uncommitted changes" }));
    await waitFor(() => expect(loadChanges).toHaveBeenLastCalledWith("uncommitted", undefined));
    expect(localStorage.getItem(KEY)).toBe("uncommitted");

    // The way back is still on offer, and takes the same turn.
    fireEvent.click(trigger());
    fireEvent.click(await screen.findByRole("option", { name: "Turn 2" }));
    await waitFor(() => expect(loadChanges).toHaveBeenLastCalledWith("turn", "t2"));
    expect(localStorage.getItem(KEY)).toBe("uncommitted");
  });

  const cardDiff = {
    turnId: "t2",
    files: [{ path: "src/card.ts", status: "added" as const, additions: 3, deletions: 0 }],
    additions: 3,
    deletions: 0,
  };

  it("lists the card's files before the server answers, then takes the server's list", async () => {
    const changes = deferred<ThreadChanges>();
    const loadChanges = vi.fn((_c: string, _t?: string) => changes.promise);
    const request: PanelRequest = { kind: "diff", turn: { id: "t2", number: 2, diff: cardDiff }, nonce: 1 };
    render(<Panel {...props({ loadChanges, request })} />);

    expect(await screen.findByText("card.ts")).toBeTruthy();
    expect(screen.queryByText("Reading the worktree…")).toBeNull();

    await act(async () => changes.resolve({ ...changesWith("src/server.ts"), mode: "turn", turnId: "t2" }));
    expect(screen.getByText("server.ts")).toBeTruthy();
    expect(screen.queryByText("card.ts")).toBeNull();
  });

  it("retries a failed turn read when the same card asks again", async () => {
    const loadChanges = vi
      .fn((_c: string, _t?: string) => Promise.resolve(changesWith("src/a.ts")))
      .mockRejectedValueOnce(new Error("socket closed"));
    const turn = { id: "t2", number: 2, diff: cardDiff };
    const { rerender } = render(<Panel {...props({ loadChanges })} request={{ kind: "diff", turn, nonce: 1 }} />);
    expect(await screen.findByText("socket closed")).toBeTruthy();

    rerender(wrap(<Panel {...props({ loadChanges })} request={{ kind: "diff", turn, nonce: 2 }} />));
    await waitFor(() => expect(screen.queryByText("socket closed")).toBeNull());
    expect(loadChanges.mock.calls).toEqual([["turn", "t2"], ["turn", "t2"]]);
  });

  it("does not re-read a turn when a later turn ends", async () => {
    const loadChanges = vi.fn((_c: string, _t?: string) => Promise.resolve(changesWith("src/a.ts")));
    const request: PanelRequest = { kind: "diff", turn: { id: "t2", number: 2 }, nonce: 1 };
    const { rerender } = render(<Panel {...props({ loadChanges, request })} />);
    await waitFor(() => expect(loadChanges).toHaveBeenCalledOnce());

    rerender(wrap(<Panel {...props({ loadChanges, request, revision: "r2" })} />));
    fireEvent.click(trigger());
    fireEvent.click(await screen.findByRole("option", { name: "Uncommitted changes" }));
    await waitFor(() => expect(loadChanges).toHaveBeenLastCalledWith("uncommitted", undefined));
    expect(loadChanges).toHaveBeenCalledTimes(2);
  });

  it("leaves the comparison alone for a diff request that names no turn", async () => {
    localStorage.setItem(KEY, "branch");
    const loadChanges = vi.fn((_c: string, _t?: string) => Promise.resolve(changesWith()));
    render(<Panel {...props({ loadChanges, request: { kind: "diff", nonce: 1 } })} />);

    await waitFor(() => expect(loadChanges).toHaveBeenCalled());
    expect(loadChanges.mock.calls).toEqual([["branch", undefined]]);
    expect(trigger().textContent).toBe("Branch changes");
  });
});

describe("Panel width", () => {
  beforeEach(() => localStorage.clear());

  it("remembers a dragged width for the next mount", () => {
    const first = render(<Panel {...props()} />);
    fireEvent.pointerDown(screen.getByRole("separator", { name: "Resize the panel" }));
    fireEvent.pointerMove(window, { clientX: window.innerWidth - 500 });
    fireEvent.pointerUp(window);
    first.unmount();

    render(<Panel {...props()} />);
    expect(screen.getByRole("complementary", { name: "Thread panel" }).style.width).toBe("500px");
  });
});
