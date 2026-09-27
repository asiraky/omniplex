// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { DeleteThreadDialog, useDeleteThread } from "./DeleteThreadDialog";
import { render, wrap } from "~/test/harness";
import type { ThreadMeta } from "~/protocol";

const thread = (id: string, extra: Partial<ThreadMeta> = {}): ThreadMeta => ({
  id,
  cwd: "/tmp/repo",
  harness: "claude",
  title: `Thread ${id}`,
  createdAt: 1,
  updatedAt: 1,
  headSeq: 1,
  phase: "idle",
  projectId: "p1",
  ...extra,
});

/**
 * The transcript's caller, with no sidebar and no list anywhere in the tree:
 * the prompt at the foot of a transcript opens the confirmation directly.
 * If the guards ever moved back into the sidebar, this would stop compiling.
 */
function Standalone({
  threads,
  target,
  onDelete,
}: {
  threads: ThreadMeta[];
  target: ThreadMeta;
  onDelete: (id: string, removeWorktree: boolean) => void;
}) {
  const flow = useDeleteThread({
    threads,
    onDelete,
    projectRoot: () => "/tmp/repo",
  });
  return (
    <>
      <button onClick={() => flow.ask(target)}>Finish</button>
      <DeleteThreadDialog flow={flow} />
    </>
  );
}

function open(threads: ThreadMeta[], target = threads[0]) {
  const onDelete = vi.fn();
  render(<Standalone threads={threads} target={target} onDelete={onDelete} />);
  fireEvent.click(screen.getByRole("button", { name: "Finish" }));
  return onDelete;
}

const checkbox = () => document.querySelector("#delete-remove-worktree");

describe("the delete confirmation, opened from outside the sidebar", () => {
  it("carries the worktree guards with it, ticked for one omniplex provisioned", () => {
    const onDelete = open([
      thread("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" }),
    ]);

    expect(checkbox()!.getAttribute("data-state")).toBe("checked");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(onDelete).toHaveBeenCalledWith("a", true);
  });

  it("refuses to offer a removal the server would not perform", () => {
    // The thread is sitting in the project's own checkout, not a worktree.
    open([thread("a", { workspaceMode: "managed", cwd: "/tmp/repo" })]);

    expect(checkbox()).toBeNull();
    expect(screen.getByText(/permanently deletes the thread/)).toBeTruthy();
  });

  it("still counts other threads in the same checkout", () => {
    const shared = { workspaceMode: "managed" as const, cwd: "/tmp/repo/.worktrees/a" };
    const onDelete = open([thread("a", shared), thread("b", shared)]);

    expect(checkbox()).toBeNull();
    expect(screen.getByText(/1 other thread/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    // The worktree stays: it is not this thread's alone to take.
    expect(onDelete).toHaveBeenCalledWith("a", false);
  });

  // The regression this pair guards: the wait used to end only in the sidebar,
  // which watched its own list for the row leaving. A caller with no list —
  // the transcript's "this landed" prompt — was never told the delete had
  // finished, so it spun forever and sat over whatever thread was opened
  // next. The hook owns the whole wait now, list or no list.
  it("stops waiting once the thread has left the list", () => {
    const target = thread("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" });
    const { rerender } = render(
      <Standalone threads={[target]} target={target} onDelete={() => Promise.resolve()} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Finish" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(screen.getByText("Deleting worktree…")).toBeTruthy();

    // The server finished the teardown and the thread is gone.
    rerender(wrap(<Standalone threads={[]} target={target} onDelete={() => Promise.resolve()} />));

    expect(screen.queryByText("Deleting worktree…")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("stops waiting when teardown fails and the thread stays", () => {
    const target = thread("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" });
    const props = { target, onDelete: () => Promise.resolve() };
    const { rerender } = render(<Standalone threads={[target]} {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Finish" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    // The row is staying, and something else is already asking what to do
    // about it — so the dialog has to get out of the way rather than go on
    // claiming a delete is still running.
    rerender(wrap(<Standalone threads={[{ ...target, phase: "cleanup_failed" }]} {...props} />));

    expect(screen.queryByText("Deleting worktree…")).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("waits for the delete rather than claiming it is done", () => {
    const threads = [thread("a", { workspaceMode: "managed", cwd: "/tmp/repo/.worktrees/a" })];
    render(
      <Standalone threads={threads} target={threads[0]} onDelete={() => new Promise(() => {})} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Finish" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    // The thread has not left the list, so the dialog is still honest about
    // what is happening — exactly as it behaves from the sidebar.
    expect(screen.getByText("Deleting worktree…")).toBeTruthy();
  });
});
