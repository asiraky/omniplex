// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { forgetVoices } from "~/lib/voices";
import { render } from "~/test/harness";
import { ProjectSettings } from "./ProjectSettings";
import type { Project, Voice } from "~/protocol";

const project = {
  id: "p1",
  name: "wrong-path",
  defaults: { harness: "claude", workspace: "local" },
  folders: [
    {
      id: "f1",
      path: "/tmp/wrong-path",
      git: true,
      copiesDir: ".worktrees",
      provisionTimeoutSeconds: 1800,
      deprovisionTimeoutSeconds: 600,
    },
  ],
  createdAt: 0,
  updatedAt: 0,
} satisfies Project;

function open(over: Partial<React.ComponentProps<typeof ProjectSettings>> = {}) {
  const props = {
    project,
    harnesses: [],
    onAddFolder: vi.fn(async () => project),
    onRemoveFolder: vi.fn(async () => project),
    listRepos: vi.fn(async () => []),
    onSave: vi.fn(async () => {}),
    onDelete: vi.fn(async () => {}),
    threadCount: 0,
    onDeleted: vi.fn(),
    ...over,
  } satisfies React.ComponentProps<typeof ProjectSettings>;
  render(<ProjectSettings {...props} />);
  return props;
}

afterEach(() => vi.unstubAllGlobals());

describe("removing a project", () => {
  // The whole point: a project added with the wrong path has to be removable
  // from the screen the user is already on.
  it("deletes after a confirmation and says so", async () => {
    const props = open();

    fireEvent.click(screen.getByRole("button", { name: /remove project/i }));
    expect(screen.getByText(/remove “wrong-path”\?/i)).toBeTruthy();
    // Still nothing sent: opening the confirmation is not the answer to it.
    expect(props.onDelete).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /^remove$/i }));
    await waitFor(() => expect(props.onDelete).toHaveBeenCalledWith("p1"));
    await waitFor(() => expect(props.onDeleted).toHaveBeenCalled());
  });

  // The button is not the only place this is enforced — the server refuses it
  // too — but being told before pressing beats an error afterwards.
  it("refuses while the project still owns threads, and says how many", () => {
    const props = open({ threadCount: 2 });

    const button = screen.getByRole("button", { name: /remove project/i }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(screen.getByText(/2 threads still belong to this project/i)).toBeTruthy();

    fireEvent.click(button);
    expect(props.onDelete).not.toHaveBeenCalled();
  });

  // A failed delete leaves the project where it is, so the screen must stay
  // open and say what went wrong rather than close as if it worked.
  it("keeps the dialog open and shows the reason when the server refuses", async () => {
    const props = open({
      onDelete: vi.fn(async () => {
        throw new Error("project still has threads: delete its 1 thread first");
      }),
    });

    fireEvent.click(screen.getByRole("button", { name: /remove project/i }));
    fireEvent.click(screen.getByRole("button", { name: /^remove$/i }));

    await waitFor(() => expect(screen.getByText(/delete its 1 thread first/i)).toBeTruthy());
    expect(props.onDeleted).not.toHaveBeenCalled();
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
    await waitFor(() => expect(props.onDeleted).toHaveBeenCalled());
  });
});

describe("folders", () => {
  const second = { ...project.folders[0], id: "f2", path: "/tmp/notes", git: false };
  const two = { ...project, folders: [project.folders[0], second] } satisfies Project;

  // With one folder there is nothing to remove it in favour of; the project
  // is removed instead.
  it("offers no remove for a project's only folder", () => {
    open();
    expect(screen.queryByRole("button", { name: /remove \/tmp\/wrong-path/i })).toBeNull();
  });

  it("removes a folder at once and keeps unsaved edits to the others", async () => {
    const props = open({ project: two, onRemoveFolder: vi.fn(async () => project) });
    fireEvent.change(screen.getByLabelText("Base branch"), { target: { value: "develop" } });

    fireEvent.click(screen.getByRole("button", { name: "Remove /tmp/notes" }));
    await waitFor(() => expect(props.onRemoveFolder).toHaveBeenCalledWith("p1", "f2"));
    await waitFor(() => expect(screen.queryByText("/tmp/notes")).toBeNull());

    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() => expect(props.onSave).toHaveBeenCalled());
    const folders = vi.mocked(props.onSave).mock.calls[0][3];
    expect(folders.map((f) => [f.id, f.baseBranch])).toEqual([["f1", "develop"]]);
  });

  it("adds a new folder by name and shows the server's answer", async () => {
    const props = open({ onAddFolder: vi.fn(async () => two) });
    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
    fireEvent.change(screen.getByLabelText("New folder name"), { target: { value: "notes" } });
    fireEvent.click(screen.getByRole("button", { name: /^add$/i }));

    await waitFor(() => expect(props.onAddFolder).toHaveBeenCalledWith("p1", { name: "notes" }));
    await waitFor(() => expect(screen.getByText("/tmp/notes")).toBeTruthy());
  });

  it("shows why an add was refused and stays open", async () => {
    const props = open({
      onAddFolder: vi.fn(async () => {
        throw new Error("/tmp/wrong-path/docs is inside /tmp/wrong-path, a git folder of this project");
      }),
    });
    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
    fireEvent.change(screen.getByLabelText("New folder name"), { target: { value: "docs" } });
    fireEvent.click(screen.getByRole("button", { name: /^add$/i }));

    await waitFor(() => expect(screen.getByText(/a git folder of this project/)).toBeTruthy());
    expect(props.onDeleted).not.toHaveBeenCalled();
  });
});

describe("voice", () => {
  const voices: Voice[] = [
    { id: "v1", name: "Alice", category: "premade" },
    { id: "v2", name: "Bryn", category: "premade", previewUrl: "https://cdn.example/bryn.mp3" },
  ];

  function withVoices() {
    forgetVoices();
    Element.prototype.scrollIntoView = () => {};
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(JSON.stringify({ voices }), { status: 200 })),
    );
  }

  async function openPicker() {
    const picker = await screen.findByRole("combobox", { name: "Voice" });
    await waitFor(() => expect((picker as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(picker);
  }

  it("is not offered without an ElevenLabs key", () => {
    withVoices();
    open({ elevenLabsKey: false });
    expect(screen.queryByRole("combobox", { name: "Voice" })).toBeNull();
  });

  it("overrides the user's voice for this project", async () => {
    withVoices();
    const props = open({ elevenLabsKey: true, defaultVoice: "v1" });
    await openPicker();
    fireEvent.click(await screen.findByRole("option", { name: /Bryn/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(props.onSave).toHaveBeenCalled());
    expect(vi.mocked(props.onSave).mock.calls[0][2]).toMatchObject({ voice: "v2" });
  });

  it("goes back to the user's voice with Use default", async () => {
    withVoices();
    const props = open({
      project: { ...project, defaults: { ...project.defaults, voice: "v2" } },
      elevenLabsKey: true,
      defaultVoice: "v1",
    });
    await openPicker();
    // The default is named, so the choice says what it falls back to.
    fireEvent.click(await screen.findByRole("option", { name: /Use default \(Alice\)/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(props.onSave).toHaveBeenCalled());
    expect(vi.mocked(props.onSave).mock.calls[0][2].voice).toBe("");
  });

  it("previews a voice without choosing it", async () => {
    withVoices();
    const play = vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue();
    // jsdom plays nothing; closing the picker stops the preview through these.
    const pause = vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    const load = vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => {});
    const props = open({ elevenLabsKey: true, defaultVoice: "v1" });
    await openPicker();
    fireEvent.click(await screen.findByRole("button", { name: "Preview Bryn" }));
    expect(play).toHaveBeenCalled();
    // Still open, and nothing chosen.
    expect(screen.getByRole("option", { name: /Bryn/ })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(props.onSave).toHaveBeenCalled());
    expect(vi.mocked(props.onSave).mock.calls[0][2].voice).toBeUndefined();
    cleanup();
    for (const spy of [play, pause, load]) spy.mockRestore();
  });
});
