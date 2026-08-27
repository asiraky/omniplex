// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";

import { render, viewport } from "~/test/harness";
import { NewSession, type NewSessionInput } from "./NewSession";
import type { HarnessMeta, Project, Workspace } from "~/protocol";

const project = {
  id: "p1",
  root: "/tmp/repo",
  config: {
    name: "repo",
    defaults: { harness: "claude", model: "", mode: "", workspace: "managed" },
    workspace: {},
  },
} as unknown as Project;

const harness = {
  id: "claude",
  name: "Claude Code",
  models: [],
  permissionModes: [],
  availability: { state: "ready" },
} as unknown as HarnessMeta;

function open(over: Partial<React.ComponentProps<typeof NewSession>> = {}) {
  render(
    <NewSession
      projects={[project]}
      harnesses={[harness]}
      userConfig={null}
      status="online"
      onCreate={vi.fn()}
      onListWorkspaces={vi.fn(async () => [] as Workspace[])}
      onListIssues={vi.fn(async () => ({ issues: [], issuesError: "" }))}
      onAddProject={vi.fn()}
      onSettings={vi.fn()}
      onRecheck={vi.fn()}
      onClose={vi.fn()}
      {...over}
    />,
  );
}

const surface = () => document.querySelector("[data-slot=dialog-content]")!;

afterEach(() => vi.unstubAllGlobals());

// Radix Select drives its trigger with pointer capture and scrolls the chosen
// item into view — neither of which jsdom implements. No-ops are enough for the
// Base dropdown test to open and pick.
beforeAll(() => {
  const proto = window.HTMLElement.prototype;
  proto.hasPointerCapture ??= () => false;
  proto.setPointerCapture ??= () => {};
  proto.releasePointerCapture ??= () => {};
  proto.scrollIntoView ??= () => {};
});

describe("NewSession", () => {
  it("takes the whole screen on a phone rather than floating as a card", () => {
    viewport("phone");
    open();

    const cls = surface().className;
    expect(cls).toContain("max-md:h-[100dvh]");
    expect(cls).toContain("max-md:w-screen");
    expect(cls).toContain("max-md:rounded-none");
    // A 85dvh cap would fight the full-height rule it sits beside.
    expect(cls).toContain("max-md:max-h-none");
    // The card's width cap has to start where the card does. At `sm` it would
    // still be in force from 640 to 767px, leaving a 448px strip pinned to the
    // left edge by the full-screen inset.
    expect(cls).toContain("md:max-w-md");
    expect(cls).not.toContain("sm:max-w-md");
    // Including the primitive's own default cap, which is dropped rather than
    // overridden for exactly the same reason.
    expect(cls).not.toContain("sm:max-w-lg");
  });

  it("keeps a way out in the corner", () => {
    viewport("phone");
    open();
    expect(screen.getByRole("button", { name: "Close" })).toBeTruthy();
  });

  it("keeps Start reachable without scrolling the form back down", () => {
    viewport("phone");
    open();
    // The form is its own scroll container, so the footer below it stays put.
    const scroller = surface().querySelector(".overflow-y-auto");
    expect(scroller).not.toBeNull();
    expect(surface().querySelector("[data-slot=dialog-footer]")).not.toBeNull();
    expect(scroller!.contains(surface().querySelector("[data-slot=dialog-footer]"))).toBe(false);
  });

  it("matches the project select to the buttons beside it", () => {
    open();
    const row = screen.getByRole("combobox", { name: /Project/ }).parentElement!;
    const buttons = Array.from(row.querySelectorAll("button")).filter(
      (b) => b.getAttribute("data-slot") === "button",
    );
    expect(buttons.length).toBe(2);
    // Inheriting the icon size rather than carrying a one-off override is the
    // whole point: these were 32px next to a 36px select.
    for (const b of buttons) {
      expect(b.className).toContain("size-11");
      expect(b.className).toContain("md:size-9");
      expect(b.className).not.toContain("md:size-8");
    }
  });

  it("starts a bypass session with no confirmation of any kind", async () => {
    // Bypass is a value in a dropdown, not a decision to re-litigate: picking
    // it once (here, as the project default) is the whole opt-in.
    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    const onCreate = vi.fn(async () => {});
    open({
      projects: [
        {
          ...project,
          config: {
            ...project.config,
            // "local" keeps the workspace choice out of it: this test is about
            // the permission mode, and the main checkout is the one choice
            // that needs nothing else named before Start is live.
            defaults: {
              ...project.config.defaults,
              mode: "bypassPermissions",
              workspace: "local",
            },
          },
        } as unknown as Project,
      ],
      harnesses: [
        {
          ...harness,
          permissionModes: [
            { id: "default", label: "Default", default: true },
            { id: "bypassPermissions", label: "Bypass", description: "Skip all permission checks" },
          ],
        } as unknown as HarnessMeta,
      ],
      onCreate,
    });

    const start = await screen.findByRole("button", { name: "Start" });
    // The workspace listing lands a tick later; Start is disabled until it has.
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    await act(async () => {
      fireEvent.click(start);
    });

    expect(confirm).not.toHaveBeenCalled();
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ mode: "bypassPermissions" }),
    );
  });

  it("gives the bypass mode the same plain treatment as every other mode", () => {
    const withModes = {
      ...harness,
      permissionModes: [
        { id: "default", label: "Default", default: true },
        { id: "bypassPermissions", label: "Bypass", description: "Skip all permission checks" },
      ],
    } as unknown as HarnessMeta;
    const withDefault = (mode: string) =>
      ({
        ...project,
        config: { ...project.config, defaults: { ...project.config.defaults, mode } },
      }) as unknown as Project;

    open({ projects: [withDefault("default")], harnesses: [withModes] });
    const plain = screen.getByRole("combobox", { name: /Permissions/ }).className;
    const plainText = surface().textContent ?? "";
    cleanup();

    open({ projects: [withDefault("bypassPermissions")], harnesses: [withModes] });
    const bypass = screen.getByRole("combobox", { name: /Permissions/ });
    // No warning colour, no badge, no extra copy: the control renders the same
    // whichever mode is selected. Only the label and description differ.
    expect(bypass.className).toBe(plain);
    expect(bypass.textContent).toBe("Bypass");
    expect((surface().textContent ?? "").replace("BypassSkip all permission checks", "")).toBe(
      plainText.replace("Default", ""),
    );
  });

  it("writes branch names in the interface font, not a terminal one", async () => {
    open();
    const field = await waitFor(() => screen.getByRole("combobox", { name: /Branch/ }));
    expect(field.className).not.toContain("font-mono");
  });

  it("offers every workspace scenario as its own choice", async () => {
    open();
    await waitFor(() => screen.getByRole("radio", { name: /Main checkout/ }));
    for (const name of [
      /Main checkout/,
      /New worktree from issue or branch name/,
      /Attach to existing worktree/,
    ]) {
      expect(screen.getByRole("radio", { name })).toBeTruthy();
    }
    // Scratch folded into the branch tile: an empty name is the scratch case,
    // so it no longer earns a row of its own.
    expect(screen.queryByRole("radio", { name: /New scratch worktree/ })).toBeNull();
  });

  it("still offers the main checkout when another session is on it", async () => {
    const root = {
      path: "/tmp/repo",
      isRoot: true,
      busy: true,
      busyTitle: "the other one",
    } as Workspace;
    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({
      onCreate,
      onListWorkspaces: vi.fn(async () => [root]),
    });

    const choice = await waitFor(() => screen.getByRole("radio", { name: /Main checkout/ }));
    expect(choice.getAttribute("disabled")).toBeNull();
    fireEvent.click(choice);

    // No warning copy is shown for a busy main checkout — it was removed.
    expect(screen.queryByText(/already on the main checkout/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(confirm).not.toHaveBeenCalled();
    expect(onCreate.mock.calls[0][0]).toMatchObject({ workspace: "local", branch: "" });
  });

  it("creates a scratch worktree by leaving the branch name blank", async () => {
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({ onCreate });

    // The managed default lands on the "New worktree" tile; leaving its name
    // empty is the old scratch behaviour — omniplex makes the name up.
    await waitFor(() =>
      screen.getByRole("radio", { name: /New worktree from issue or branch name/ }),
    );
    const start = screen.getByRole("button", { name: "Start" });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({
      workspace: "managed",
      branch: "",
      workspacePath: "",
    });
  });

  it("sends a per-session base ref picked from the branches on disk", async () => {
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    // The Base dropdown offers the branches already checked out; stacking on
    // one of them is exactly what it exists for.
    const under = {
      path: "/tmp/repo/.worktrees/under",
      branch: "feature/underneath",
    } as Workspace;
    open({ onCreate, onListWorkspaces: vi.fn(async () => [under]) });

    const field = await waitFor(() => screen.getByRole("combobox", { name: /Branch/ }));
    fireEvent.change(field, { target: { value: "issue/9-stack" } });

    const base = screen.getByRole("combobox", { name: "Base" });
    base.focus();
    fireEvent.keyDown(base, { key: "ArrowDown" });
    const option = await waitFor(() => screen.getByRole("option", { name: "feature/underneath" }));
    fireEvent.click(option);

    fireEvent.click(screen.getByRole("button", { name: "Start" }));

    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({
      workspace: "managed",
      branch: "issue/9-stack",
      baseRef: "feature/underneath",
    });
  });

  it("attaches to a worktree another session is already in", async () => {
    const side = {
      path: "/tmp/repo/.worktrees/side",
      branch: "issue/1-side",
      busy: true,
      busyTitle: "the other one",
    } as Workspace;
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({
      onCreate,
      onListWorkspaces: vi.fn(async () => [side]),
    });

    fireEvent.click(
      await waitFor(() => screen.getByRole("radio", { name: /Attach to existing worktree/ })),
    );
    fireEvent.click(screen.getByRole("combobox", { name: /Worktree/ }));
    const row = await waitFor(() => screen.getByRole("option", { name: /issue\/1-side/ }));
    expect(row.hasAttribute("disabled")).toBe(false);
    fireEvent.click(row);

    // No warning copy is shown for a busy worktree — it was removed.
    expect(screen.queryByText(/already in this worktree/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate.mock.calls[0][0]).toMatchObject({
      workspace: "",
      workspacePath: side.path,
    });
  });

  it("will not start before the busy check has come back", async () => {
    let release: (w: Workspace[]) => void = () => {};
    const pending = new Promise<Workspace[]>((r) => {
      release = r;
    });
    open({ onListWorkspaces: vi.fn(() => pending) });

    // The managed default lands on the branch tile with an empty name, so
    // nothing but the outstanding check is holding Start back.
    await waitFor(() =>
      screen.getByRole("radio", { name: /New worktree from issue or branch name/ }),
    );
    expect(screen.getByRole("button", { name: "Start" }).hasAttribute("disabled")).toBe(true);

    release([]);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Start" }).hasAttribute("disabled")).toBe(false),
    );
  });
});

// A signed-out harness is the commonest way a session refuses to start, and
// the fix is the harness's own login. When the server can run that, the alert
// offers it; when it cannot, only "Check again" remains.
describe("a signed-out harness", () => {
  const signedOut = {
    ...harness,
    availability: {
      state: "unavailable",
      reason: "Claude is not signed in.",
      remedy: [{ text: "Sign in", command: "claude auth login", action: "login" }],
    },
    instances: [
      {
        id: "claude",
        driver: "claude",
        displayName: "Claude Code",
        enabled: true,
        availability: {
          state: "unavailable",
          reason: "Claude is not signed in.",
          remedy: [{ text: "Sign in", command: "claude auth login", action: "login" }],
        },
        models: [],
      },
    ],
  } as unknown as HarnessMeta;

  it("offers the harness's own sign-in", async () => {
    const onLogin = vi.fn();
    open({ harnesses: [signedOut], onLogin });
    expect(screen.getByText("Claude is not signed in.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(onLogin).toHaveBeenCalledWith("claude");
  });

  it("does not offer a sign-in the server cannot run", () => {
    open({ harnesses: [signedOut] });
    expect(screen.queryByRole("button", { name: /sign in/i })).toBeNull();
    expect(screen.getByRole("button", { name: /check again/i })).toBeTruthy();
  });
});
