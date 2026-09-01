// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { render, viewport } from "~/test/harness";
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

// Radix Select drives its trigger with pointer capture and scrolls the chosen
// item into view; jsdom implements neither. No-ops are enough to open one.
beforeAll(() => {
  const proto = window.HTMLElement.prototype;
  proto.hasPointerCapture ??= () => false;
  proto.setPointerCapture ??= () => {};
  proto.releasePointerCapture ??= () => {};
  proto.scrollIntoView ??= () => {};
});

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

// The agent settings that used to live on this screen are remembered from the
// last session instead (see sessionPrefs). Nothing here edits them any more.
describe("the settings this screen still owns", () => {
  it("keeps the workspace, base branch, hooks and branch format", () => {
    open();
    expect(screen.getByRole("combobox", { name: "Default workspace" })).toBeTruthy();
    expect(screen.getByLabelText("Base branch")).toBeTruthy();
    expect(screen.getByLabelText("Provision")).toBeTruthy();
    expect(screen.getByLabelText("Deprovision")).toBeTruthy();
    expect(screen.getByText(/Branch names from issues/)).toBeTruthy();
    expect(screen.getByLabelText("Project name")).toBeTruthy();
  });

  it("no longer offers agent defaults to edit", () => {
    open();
    for (const name of [
      "Default harness",
      "Harness settings",
      "Default model",
      "Default effort",
      "Default permission mode",
    ]) {
      expect(screen.queryByRole("combobox", { name })).toBeNull();
    }
    expect(screen.queryByText(/Agent defaults/)).toBeNull();
  });

  it("still saves the default workspace", async () => {
    const props = open();
    const select = screen.getByRole("combobox", { name: "Default workspace" });
    select.focus();
    fireEvent.keyDown(select, { key: "ArrowDown" });
    fireEvent.click(await screen.findByRole("option", { name: "Worktree" }));
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(props.onSave).toHaveBeenCalled());
    expect((props.onSave as ReturnType<typeof vi.fn>).mock.calls[0][1].defaults.workspace).toBe(
      "managed",
    );
  });
});

describe("adding a project by cloning", () => {
  // defaultRoot is the directory the server was started in, which is itself a
  // checkout — so the clone belongs beside it, not inside it.
  const addMode = (over: Partial<React.ComponentProps<typeof ProjectSettings>> = {}) =>
    open({ project: null, defaultRoot: "/home/aaron/code/hy", ...over });

  const chooseClone = () =>
    fireEvent.click(screen.getByRole("radio", { name: /Clone from Git/ }));

  it("keeps the existing-folder path exactly as it was", async () => {
    const props = addMode();
    // No source has to be chosen to get the old behaviour: it is the default.
    fireEvent.change(screen.getByLabelText("Project directory"), {
      target: { value: "/home/aaron/code/thing" },
    });
    fireEvent.click(screen.getByRole("button", { name: /add project/i }));
    await waitFor(() => expect(props.onAdd).toHaveBeenCalledWith("/home/aaron/code/thing"));
  });

  it("prefills the destination from the repository as it is typed", () => {
    addMode();
    chooseClone();
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "git@github.com:asiraky/omniplex.git" },
    });
    expect((screen.getByLabelText("Clone into") as HTMLInputElement).value).toBe(
      "/home/aaron/code/omniplex",
    );
  });

  it("prefills under the directory the last clone landed in", () => {
    addMode({ userConfig: { version: 1, projectsDirectory: "/srv/checkouts" } });
    chooseClone();
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "asiraky/omniplex" },
    });
    expect((screen.getByLabelText("Clone into") as HTMLInputElement).value).toBe(
      "/srv/checkouts/omniplex",
    );
  });

  it("stops tracking the URL once the destination is edited by hand", () => {
    addMode();
    chooseClone();
    const repo = screen.getByLabelText("Repository");
    fireEvent.change(repo, { target: { value: "asiraky/omniplex" } });
    fireEvent.change(screen.getByLabelText("Clone into"), {
      target: { value: "/elsewhere/mine" },
    });
    fireEvent.change(repo, { target: { value: "asiraky/other" } });
    expect((screen.getByLabelText("Clone into") as HTMLInputElement).value).toBe("/elsewhere/mine");
  });

  it("sends the url with the destination, then remembers the parent directory", async () => {
    const props = addMode();
    chooseClone();
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "https://github.com/asiraky/omniplex.git" },
    });
    fireEvent.click(screen.getByRole("button", { name: /clone and add/i }));

    await waitFor(() =>
      expect(props.onAdd).toHaveBeenCalledWith(
        "/home/aaron/code/omniplex",
        "https://github.com/asiraky/omniplex.git",
      ),
    );
    await waitFor(() =>
      expect(props.onSaveUserConfig).toHaveBeenCalledWith(
        expect.objectContaining({ projectsDirectory: "/home/aaron/code" }),
      ),
    );
    expect(props.onClose).toHaveBeenCalled();
  });

  // A clone is minutes of network, not a save: it has to look busy and it must
  // not be startable twice.
  it("says it is cloning and takes the button out of reach while it runs", async () => {
    let release: () => void = () => {};
    const onAdd = vi.fn(() => new Promise<void>((r) => (release = r)));
    const props = addMode({ onAdd });
    chooseClone();
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "asiraky/omniplex" },
    });
    fireEvent.click(screen.getByRole("button", { name: /clone and add/i }));

    const button = await screen.findByRole("button", { name: /cloning/i });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(button);
    expect(onAdd).toHaveBeenCalledTimes(1);

    release();
    await waitFor(() => expect(props.onClose).toHaveBeenCalled());
  });

  it("shows the server's reason inline and stays open when the clone fails", async () => {
    const props = addMode({
      onAdd: vi.fn(async () => {
        throw new Error("clone failed: repository not found");
      }),
    });
    chooseClone();
    fireEvent.change(screen.getByLabelText("Repository"), {
      target: { value: "asiraky/nope" },
    });
    fireEvent.click(screen.getByRole("button", { name: /clone and add/i }));

    await waitFor(() => expect(screen.getByText(/repository not found/)).toBeTruthy());
    expect(props.onClose).not.toHaveBeenCalled();
    expect(props.onSaveUserConfig).not.toHaveBeenCalled();
    // And it is offering the action again rather than stuck on "Cloning…".
    expect(screen.getByRole("button", { name: /clone and add/i })).toBeTruthy();
  });

  it("will not clone until there is a repository to clone", () => {
    addMode();
    chooseClone();
    expect(
      (screen.getByRole("button", { name: /clone and add/i }) as HTMLButtonElement).disabled,
    ).toBe(true);
  });
});

// Half of this app is used from a phone, and adding a project by pasting a
// clone URL is very much a phone thing to do.
describe("the add form on a phone", () => {
  it("stacks the source choice and keeps every target thumb-sized", () => {
    viewport("phone");
    open({ project: null });

    const choices = screen.getAllByRole("radio");
    expect(choices.length).toBe(2);
    for (const choice of choices) {
      // Single column until sm, so neither label is squeezed at 375px.
      expect(choice.parentElement!.className).toContain("sm:grid-cols-2");
      expect(choice.className).toContain("min-h-11");
    }

    // The dialog itself is the whole screen, and the footer does not scroll
    // away from under the form.
    const surface = document.querySelector("[data-slot=dialog-content]")!;
    expect(surface.className).toContain("max-md:h-[100dvh]");
    const scroller = surface.querySelector(".overflow-y-auto")!;
    expect(scroller.contains(surface.querySelector("[data-slot=dialog-footer]"))).toBe(false);
  });

  it("does not make the path fields shout in a monospace that overflows", () => {
    viewport("phone");
    open({ project: null });
    fireEvent.click(screen.getByRole("radio", { name: /Clone from Git/ }));
    for (const label of ["Repository", "Clone into"]) {
      // 12px mono on desktop, but the phone keeps the 16px base that stops
      // iOS zooming the viewport on focus.
      expect(screen.getByLabelText(label).className).toContain("md:text-[12px]");
    }
  });
});
