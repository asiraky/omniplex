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
    defaults: { harness: "claude", harnesses: {}, workspace: "managed" },
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
// The dialog now remembers what a session started with, so a test that starts
// one leaves that behind for the next. Every test gets a browser that has
// never opened this dialog before unless it says otherwise.
afterEach(() => localStorage.clear());

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
              harnesses: { claude: { mode: "bypassPermissions" } },
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

  it("restores this project's settings for the harness selected", async () => {
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    const ready = { state: "ready" } as const;
    const claude = {
      ...harness,
      permissionModes: [
        { id: "default", label: "Manual", default: true },
        { id: "bypassPermissions", label: "Bypass" },
      ],
      instances: [
        {
          id: "claude",
          driver: "claude",
          displayName: "Claude Code",
          enabled: true,
          availability: ready,
          models: [{ id: "opus", label: "Opus", default: true }],
        },
      ],
    } as unknown as HarnessMeta;
    const codex = {
      id: "codex",
      name: "Codex",
      availability: ready,
      models: [],
      permissionModes: [
        { id: "on-request", label: "Ask when needed", default: true },
        { id: "full-access", label: "Bypass" },
      ],
      instances: [
        {
          id: "codex",
          driver: "codex",
          displayName: "Codex",
          enabled: true,
          availability: ready,
          models: [
            {
              id: "gpt-5.6-sol",
              label: "GPT-5.6-Sol",
              default: true,
              efforts: ["high", "xhigh"],
            },
          ],
        },
      ],
    } as unknown as HarnessMeta;
    open({
      projects: [
        {
          ...project,
          config: {
            ...project.config,
            defaults: {
              ...project.config.defaults,
              harness: "claude",
              harnesses: {
                claude: { mode: "bypassPermissions" },
                codex: { model: "gpt-5.6-sol", mode: "full-access", effort: "xhigh" },
              },
              workspace: "local",
            },
          },
        } as unknown as Project,
      ],
      harnesses: [claude, codex],
      onCreate,
    });

    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.change(screen.getByPlaceholderText(/Search models and accounts/), {
      target: { value: "gpt" },
    });
    const model = await waitFor(() => screen.getByText("GPT-5.6-Sol"));
    fireEvent.click(model.closest("[data-slot='command-item']")!);

    expect(screen.getByRole("combobox", { name: /Permissions/ }).textContent).toBe("Bypass");
    const start = screen.getByRole("button", { name: "Start" });
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ harness: "codex", mode: "full-access", effort: "xhigh" }),
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
        config: {
          ...project.config,
          defaults: {
            ...project.config.defaults,
            harnesses: { claude: { mode } },
          },
        },
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
describe("the remembered project", () => {
  const other = {
    ...project,
    id: "p2",
    config: { ...project.config, name: "other" },
  } as unknown as Project;

  afterEach(() => localStorage.clear());

  it("opens on the project the last session was started from", () => {
    localStorage.setItem("omniplex.lastProject.v1", "p2");
    open({ projects: [project, other] });
    expect(screen.getByLabelText("Project").textContent).toBe("other");
  });

  it("opens on the first project when the remembered one is gone", () => {
    localStorage.setItem("omniplex.lastProject.v1", "deleted");
    open({ projects: [project, other] });
    expect(screen.getByLabelText("Project").textContent).toBe("repo");
  });

  it("remembers only a session that actually started", async () => {
    const onCreate = vi.fn(async (_input: NewSessionInput) => {
      throw new Error("no");
    });
    open({ projects: [project, other], onCreate });

    const start = await waitFor(() => screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect((start as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(start);
    await waitFor(() => expect(screen.getByText("no")).toBeTruthy());
    expect(localStorage.getItem("omniplex.lastProject.v1")).toBeNull();

    cleanup();
    const ok = vi.fn(async (_input: NewSessionInput) => {});
    open({ projects: [project, other], onCreate: ok });
    const go = await waitFor(() => screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect((go as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(go);
    await waitFor(() => expect(ok).toHaveBeenCalled());
    expect(localStorage.getItem("omniplex.lastProject.v1")).toBe("p1");
  });
});

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

describe("an interactive-login harness", () => {
  it("offers sign-in again even when the health check says ready", () => {
    const onLogin = vi.fn();
    const ready = {
      ...harness,
      instances: [
        {
          id: "claude",
          driver: "claude",
          displayName: "Claude Code",
          enabled: true,
          canLogin: true,
          availability: { state: "ready" },
          models: [],
        },
      ],
    } as HarnessMeta;

    open({ harnesses: [ready], onLogin });
    fireEvent.click(screen.getByRole("button", { name: /sign in again/i }));
    expect(onLogin).toHaveBeenCalledWith("claude");
  });
});

// The settings a session starts with are remembered from the last one rather
// than configured on the project screen — per project, and per harness, so
// switching harness restores that harness's own names instead of resetting.
describe("the remembered session settings", () => {
  const ready = { state: "ready" } as const;
  const claude = {
    ...harness,
    permissionModes: [
      { id: "default", label: "Manual", default: true },
      { id: "bypassPermissions", label: "Bypass" },
    ],
    instances: [
      {
        id: "claude",
        driver: "claude",
        displayName: "Claude Code",
        enabled: true,
        availability: ready,
        models: [
          { id: "opus", label: "Opus", default: true },
          { id: "sonnet", label: "Sonnet" },
        ],
      },
    ],
  } as unknown as HarnessMeta;
  const codex = {
    id: "codex",
    name: "Codex",
    availability: ready,
    models: [],
    permissionModes: [
      { id: "on-request", label: "Ask when needed", default: true },
      { id: "full-access", label: "Bypass" },
    ],
    instances: [
      {
        id: "codex",
        driver: "codex",
        displayName: "Codex",
        enabled: true,
        availability: ready,
        models: [
          { id: "gpt-5.6-sol", label: "GPT-5.6-Sol", default: true, efforts: ["high", "xhigh"] },
        ],
      },
    ],
  } as unknown as HarnessMeta;

  // "local" keeps the workspace out of it: the main checkout is the one choice
  // that needs nothing else named before Start goes live.
  const local = (over: Record<string, unknown> = {}) =>
    ({
      ...project,
      config: {
        ...project.config,
        defaults: { ...project.config.defaults, workspace: "local", harnesses: {}, ...over },
      },
    }) as unknown as Project;

  const remember = (prefs: unknown) =>
    localStorage.setItem("omniplex.sessionPrefs.v1", JSON.stringify({ p1: prefs }));

  const start = async () => {
    const button = await waitFor(() => screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
    await act(async () => {
      fireEvent.click(button);
    });
  };

  const pickCodex = async () => {
    fireEvent.click(screen.getByRole("combobox", { name: "Harness and model" }));
    fireEvent.change(screen.getByPlaceholderText(/Search models and accounts/), {
      target: { value: "gpt" },
    });
    const model = await waitFor(() => screen.getByText("GPT-5.6-Sol"));
    fireEvent.click(model.closest("[data-slot='command-item']")!);
  };

  afterEach(() => localStorage.clear());

  it("opens on the harness, model and mode the last session used", async () => {
    remember({
      harness: "claude",
      workspace: "local",
      byHarness: { claude: { model: "sonnet", mode: "bypassPermissions" } },
    });
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({ projects: [local()], harnesses: [claude, codex], onCreate });

    expect(screen.getByRole("combobox", { name: /Permissions/ }).textContent).toBe("Bypass");
    await start();
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ harness: "claude", model: "sonnet", mode: "bypassPermissions" }),
    );
  });

  // The point of keying by harness: Claude's bypass must not follow you to
  // Codex, and Codex's own memory must come back instead.
  it("restores the other harness's own settings when the harness is switched", async () => {
    remember({
      harness: "claude",
      workspace: "local",
      byHarness: {
        claude: { model: "sonnet", mode: "bypassPermissions" },
        codex: { model: "gpt-5.6-sol", mode: "on-request", effort: "xhigh" },
      },
    });
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({ projects: [local()], harnesses: [claude, codex], onCreate });

    expect(screen.getByRole("combobox", { name: /Permissions/ }).textContent).toBe("Bypass");
    await pickCodex();

    // Codex's remembered mode, not Claude's — and not Codex's own "Bypass"
    // either, which is a different id that happens to share the label.
    expect(screen.getByRole("combobox", { name: /Permissions/ }).textContent).toBe(
      "Ask when needed",
    );
    await start();
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ harness: "codex", mode: "on-request", effort: "xhigh" }),
    );
  });

  it("falls back to the project.json seed when nothing has been started here", async () => {
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({
      projects: [
        local({ harness: "claude", harnesses: { claude: { model: "sonnet", mode: "bypassPermissions" } } }),
      ],
      harnesses: [claude, codex],
      onCreate,
    });

    await start();
    expect(onCreate).toHaveBeenCalledWith(
      expect.objectContaining({ model: "sonnet", mode: "bypassPermissions" }),
    );
  });

  // Accounts drop models. A remembered name the instance has stopped serving
  // is not sent — it falls through to the seed, then to the harness default.
  it("ignores a remembered model the instance no longer lists", async () => {
    remember({ harness: "claude", workspace: "local", byHarness: { claude: { model: "opus-3" } } });
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({
      projects: [local({ harnesses: { claude: { model: "sonnet" } } })],
      harnesses: [claude],
      onCreate,
    });

    await start();
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ model: "sonnet" }));
  });

  it("ignores a remembered mode the harness no longer has", async () => {
    remember({ harness: "claude", workspace: "local", byHarness: { claude: { mode: "gone" } } });
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({ projects: [local()], harnesses: [claude], onCreate });

    await start();
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ mode: "" }));
  });

  it("ignores a remembered harness that is no longer installed", async () => {
    remember({ harness: "gemini", workspace: "local", byHarness: {} });
    const onCreate = vi.fn(async (_input: NewSessionInput) => {});
    open({ projects: [local({ harness: "claude" })], harnesses: [claude, codex], onCreate });

    await start();
    expect(onCreate).toHaveBeenCalledWith(expect.objectContaining({ harness: "claude" }));
  });

  it("remembers the workspace kind the last session used", async () => {
    // The project seeds "local"; the memory says a worktree, and wins.
    remember({ harness: "claude", workspace: "managed", byHarness: {} });
    open({ projects: [local()], harnesses: [claude] });

    await waitFor(() =>
      expect(
        screen
          .getByRole("radio", { name: /New worktree from issue or branch name/ })
          .getAttribute("aria-checked"),
      ).toBe("true"),
    );
  });

  it("writes back what the session actually started with, per harness", async () => {
    remember({
      harness: "claude",
      workspace: "local",
      byHarness: { claude: { model: "sonnet", mode: "bypassPermissions" } },
    });
    open({
      projects: [local()],
      harnesses: [claude, codex],
      onCreate: vi.fn(async (_input: NewSessionInput) => {}),
    });

    await pickCodex();
    await start();

    const stored = JSON.parse(localStorage.getItem("omniplex.sessionPrefs.v1")!);
    expect(stored.p1.harness).toBe("codex");
    expect(stored.p1.workspace).toBe("local");
    expect(stored.p1.byHarness.codex.model).toBe("gpt-5.6-sol");
    // Claude's entry is untouched by a session started on Codex.
    expect(stored.p1.byHarness.claude).toEqual({ model: "sonnet", mode: "bypassPermissions" });
  });

  it("remembers nothing from a start that errored", async () => {
    open({
      projects: [local()],
      harnesses: [claude],
      onCreate: vi.fn(async (_input: NewSessionInput) => {
        throw new Error("no");
      }),
    });
    await start();
    await waitFor(() => expect(screen.getByText("no")).toBeTruthy());
    expect(localStorage.getItem("omniplex.sessionPrefs.v1")).toBeNull();
  });
});
