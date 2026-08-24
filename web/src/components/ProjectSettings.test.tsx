// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import { ProjectSettings } from "./ProjectSettings";
import type { Project } from "~/protocol";

const project = {
  id: "p1",
  root: "/tmp/wrong-path",
  config: {
    version: 1,
    name: "wrong-path",
    defaults: { harness: "claude", workspace: "local" },
    workspace: {},
  },
  createdAt: 0,
  updatedAt: 0,
} as unknown as Project;

function open(over: Partial<React.ComponentProps<typeof ProjectSettings>> = {}) {
  const props = {
    project,
    defaultRoot: "/tmp",
    harnesses: [],
    userConfig: null,
    onAdd: vi.fn(async () => {}),
    onSave: vi.fn(async () => {}),
    onDelete: vi.fn(async () => {}),
    sessionCount: 0,
    onSaveUserConfig: vi.fn(async () => {}),
    onClose: vi.fn(),
    ...over,
  } satisfies React.ComponentProps<typeof ProjectSettings>;
  render(<ProjectSettings {...props} />);
  return props;
}

afterEach(() => vi.unstubAllGlobals());

describe("removing a project", () => {
  // The whole point: a project added with the wrong path has to be removable
  // from the screen the user is already on.
  it("deletes after a confirmation and closes", async () => {
    const props = open();

    fireEvent.click(screen.getByRole("button", { name: /remove project/i }));
    expect(screen.getByText(/remove “wrong-path”\?/i)).toBeTruthy();
    // Still nothing sent: opening the confirmation is not the answer to it.
    expect(props.onDelete).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /^remove$/i }));
    await waitFor(() => expect(props.onDelete).toHaveBeenCalledWith("p1"));
    await waitFor(() => expect(props.onClose).toHaveBeenCalled());
  });

  // The button is not the only place this is enforced — the server refuses it
  // too — but being told before pressing beats an error afterwards.
  it("refuses while the project still owns sessions, and says how many", () => {
    const props = open({ sessionCount: 2 });

    const button = screen.getByRole("button", { name: /remove project/i }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(screen.getByText(/2 sessions still belong to this project/i)).toBeTruthy();

    fireEvent.click(button);
    expect(props.onDelete).not.toHaveBeenCalled();
  });

  // A failed delete leaves the project where it is, so the screen must stay
  // open and say what went wrong rather than close as if it worked.
  it("keeps the dialog open and shows the reason when the server refuses", async () => {
    const props = open({
      onDelete: vi.fn(async () => {
        throw new Error("project still has sessions: delete its 1 session first");
      }),
    });

    fireEvent.click(screen.getByRole("button", { name: /remove project/i }));
    fireEvent.click(screen.getByRole("button", { name: /^remove$/i }));

    await waitFor(() => expect(screen.getByText(/delete its 1 session first/i)).toBeTruthy());
    expect(props.onClose).not.toHaveBeenCalled();
    // And it is back to offering the action, not stuck mid-confirmation.
    expect(screen.getByRole("button", { name: /remove project/i })).toBeTruthy();
  });

  // A save landing after the delete commits writes the project straight back,
  // so Save has to be dead for as long as the delete is in flight.
  it("takes Save out of reach while the delete is running", async () => {
    let release: () => void = () => {};
    const props = open({
      onDelete: vi.fn(() => new Promise<void>((r) => (release = r))),
    });

    fireEvent.click(screen.getByRole("button", { name: /remove project/i }));
    fireEvent.click(screen.getByRole("button", { name: /^remove$/i }));

    await waitFor(() =>
      expect((screen.getByRole("button", { name: /^save$/i }) as HTMLButtonElement).disabled).toBe(
        true,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    expect(props.onSave).not.toHaveBeenCalled();

    release();
    await waitFor(() => expect(props.onClose).toHaveBeenCalled());
  });

  // Adding a project is the same dialog with no project behind it. There is
  // nothing to remove yet, and offering it would be offering a no-op.
  it("is not offered while adding a project", () => {
    open({ project: null });
    expect(screen.queryByRole("button", { name: /remove project/i })).toBeNull();
  });
});
