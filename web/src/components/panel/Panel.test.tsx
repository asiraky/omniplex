// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { emptyState } from "~/apply";
import { Panel, type PanelProps, type PanelRequest } from "~/components/panel/Panel";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "~/components/ui/dropdown-menu";
import { savePanel } from "~/lib/panel";
import type { ThreadChanges } from "~/protocol";
import { render, wrap } from "~/test/harness";

function renderPanel(onClose: () => void) {
  const never = () => new Promise<never>(() => {});
  render(
    <>
      <Panel
        threadId="s1"
        state={emptyState("s1")}
        command={never}
        open
        onClose={onClose}
        revision="r"
        loadChanges={never}
        loadDiff={never}
        loadTree={never}
        loadFile={never}
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
    state: emptyState("s1"),
    command: never,
    open: true,
    onClose: () => {},
    revision: "r",
    loadChanges: never,
    loadDiff: never,
    loadTree: never,
    loadFile: never,
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
