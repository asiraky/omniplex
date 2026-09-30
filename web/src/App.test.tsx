// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { render, viewport } from "~/test/harness";
import type { ClientEvents } from "./client";
import type { ThreadMeta } from "./protocol";

// The socket is the app's only source of truth, so the tests own it: this
// captures the callbacks App hands the client and lets each test decide when
// the thread list arrives — which is the whole subject of these tests.
let events: ClientEvents;
const command = vi.fn(async (_name: string, _args: unknown) => ({}) as any);
const attach = vi.fn();
const detach = vi.fn();
const prime = vi.fn();
const toast = vi.hoisted(() => ({ error: vi.fn(), info: vi.fn(), success: vi.fn() }));

vi.mock("sonner", () => ({ toast }));
const uploadStaged = vi.hoisted(() => vi.fn());
vi.mock("./lib/attachments", async (actual) => ({
  ...(await actual<typeof import("./lib/attachments")>()),
  uploadStaged,
}));

vi.mock("./client", () => ({
  wsURL: () => "ws://test",
  uuid: () => Math.random().toString(36).slice(2),
  Client: class {
    constructor(_url: string, e: ClientEvents) {
      events = e;
    }
    connect() {
      events.onStatus("online");
    }
    close() {}
    detach = detach;
    attach = attach;
    command = command;
    prime = prime;
  },
}));

const { App } = await import("./App");

const project = {
  id: "p1",
  name: "repo",
  defaults: { harness: "claude", harnesses: {}, workspace: "local" },
  folders: [{ id: "f1", path: "/tmp/repo", git: true, copiesDir: ".worktrees", provisionTimeoutSeconds: 1800, deprovisionTimeoutSeconds: 600 }],
} as any;

const harness = {
  id: "claude",
  name: "Claude Code",
  models: [],
  permissionModes: [],
  availability: { state: "ready" },
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
} as any;

const thread = (id: string): ThreadMeta =>
  ({
    id,
    title: `Thread ${id}`,
    phase: "idle",
    updatedAt: Date.now(),
    cwd: "/tmp/repo",
    harness: "claude",
    projectId: "p1",
    branch: "main",
  }) as ThreadMeta;

/** The mobile sidebar is a sheet; its presence in the DOM is "open". */
const sidebarShowing = () => document.querySelector("[data-slot=sheet-content]") !== null;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  Element.prototype.scrollIntoView = vi.fn();
  command.mockClear();
  attach.mockClear();
  detach.mockClear();
  prime.mockClear();
  toast.error.mockClear();
  toast.info.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

const state = (id: string, mode: string): any => ({
  threadId: id,
  seq: 1,
  cwd: "/tmp/repo",
  harness: "claude",
  model: "",
  mode,
  effort: "",
  title: `Thread ${id}`,
  phase: "idle",
  closed: false,
  workspace: { phase: "ready", projectId: "p1", projectRoot: "/tmp/repo" },
  items: [],
  turns: [],
  jobs: [],
  plan: [],
  usage: {},
  pendingPermissions: [],
  pendingElicitations: [],
  queuedPrompts: [],
});

describe("new thread project", () => {
  it("uses the viewed thread's project after switching threads", async () => {
    localStorage.setItem("omniplex.lastProject.v1", "p2");
    render(<App />);
    await act(async () => {
      events.onProjects([project, { ...project, id: "p2", name: "other" }]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a"), { ...thread("b"), projectId: "p2" }]);
    });
    fireEvent.click(screen.getByText("Thread b"));
    fireEvent.click(screen.getByText("Thread a"));
    fireEvent.click(screen.getAllByRole("button", { name: /New thread/ })[0]);
    expect(screen.getByRole("button", { name: /^Project/ }).textContent).toBe("repo");
  });
});

describe("copying a transcript", () => {
  it("copies only the raw user and assistant prose from the thread header", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a")]);
    });
    fireEvent.click(screen.getByText("Thread a"));
    await act(async () =>
      events.onState("a", {
        ...state("a", "default"),
        items: [
          { id: "u1", kind: "message", role: "user", text: "Question" },
          { id: "thought", kind: "message", role: "agent", contentKind: "thought", text: "Private" },
          { id: "tool", kind: "tool", title: "Read" },
          { id: "child", kind: "message", role: "agent", parentId: "tool", text: "Subagent" },
          { id: "a1", kind: "message", role: "agent", contentKind: "text", text: "**Answer**" },
        ],
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Copy transcript" }));

    expect(writeText).toHaveBeenCalledWith(
      "## User\n\nQuestion\n\n## Assistant\n\n**Answer**",
    );
  });
});

describe("thread actions on a phone", () => {
  const openThread = async () => {
    viewport("phone");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a")]);
    });
    await act(async () => {
      fireEvent.click(screen.getByText("Thread a"));
      events.onState("a", state("a", "default"));
    });
  };

  it("puts the header actions in one overflow menu", async () => {
    await openThread();
    await act(async () =>
      events.onLabels([
        { id: "label-1", name: "Parked", color: "#f59e0b", position: 0, createdAt: 1 },
      ]),
    );

    expect(screen.queryByRole("button", { name: "Copy transcript" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Label this thread" })).toBeNull();
    fireEvent.pointerDown(screen.getByRole("button", { name: "More thread actions" }), {
      button: 0,
      ctrlKey: false,
    });

    expect(screen.getByRole("menuitem", { name: "Open panel" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Copy transcript" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Label thread" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: /diff/i })).toBeNull();
  });

  it("opens the whole panel directly, with terminal available from its surface menu", async () => {
    await openThread();

    fireEvent.pointerDown(screen.getByRole("button", { name: "More thread actions" }), {
      button: 0,
      ctrlKey: false,
    });
    fireEvent.click(screen.getByRole("menuitem", { name: "Open panel" }));

    const panel = await screen.findByRole("dialog", { name: "Thread panel" });
    fireEvent.pointerDown(within(panel).getByRole("button", { name: "Open a surface" }), {
      button: 0,
      ctrlKey: false,
    });
    expect(await screen.findByRole("menuitem", { name: "Terminal" })).toBeTruthy();
  });
});

describe("a bypass thread is just a thread", () => {
  it("opens with no confirmation, banner, or acknowledgement", async () => {
    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    localStorage.setItem("omniplex.lastThread", "a");
    viewport("desktop");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses(
        [
          {
            ...harness,
            permissionModes: [
              { id: "default", label: "Default", default: true },
              {
                id: "bypassPermissions",
                label: "Bypass",
                description: "Skip all permission checks",
              },
            ],
          },
        ],
      );
      events.onThreads([thread("a")]);
      events.onState("a", state("a", "bypassPermissions"));
    });

    expect(confirm).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toMatch(
      /are you sure|without asking you first|acknowledge|proceed with caution/i,
    );
  });
});

describe("landing on a phone", () => {
  it("lands on the thread list when there is nothing to restore", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));

    expect(sidebarShowing()).toBe(true);
  });

  it("still lands on the thread list when there are no threads at all", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => events.onThreads([]));

    expect(sidebarShowing()).toBe(true);
    expect(screen.getByText("All caught up")).toBeTruthy();
  });

  it("restores straight into the last thread without flashing the list open", async () => {
    localStorage.setItem("omniplex.lastThread", "a");
    viewport("phone");
    render(<App />);

    // Before the list lands we do not yet know whether "a" still exists, so
    // the sidebar must not be shown only to be shut a frame later.
    expect(sidebarShowing()).toBe(false);

    await act(async () => events.onThreads([thread("a")]));
    expect(attach).toHaveBeenCalledWith("a");
    expect(sidebarShowing()).toBe(false);
  });

  it("falls back to the list when the stored thread is gone", async () => {
    localStorage.setItem("omniplex.lastThread", "gone");
    viewport("phone");
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));

    expect(attach).not.toHaveBeenCalled();
    await waitFor(() => expect(sidebarShowing()).toBe(true));
  });

  it("says nothing while it is still deciding", async () => {
    localStorage.setItem("omniplex.lastThread", "a");
    viewport("phone");
    render(<App />);

    // Neither empty-state message: both would be contradicted a moment later.
    expect(screen.queryByText("All caught up")).toBeNull();
    expect(screen.queryByText("Nothing open")).toBeNull();
    expect(screen.getByText("Reopening your last thread…")).toBeTruthy();
  });
});

describe("the empty content column", () => {
  it("points at the list when there are threads to pick from", async () => {
    viewport("desktop");
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));

    expect(screen.getByText("Nothing open")).toBeTruthy();
    // The action is still offered, but quietly: no oversized call to action
    // competing with the list of threads beside it.
    const cta = screen
      .getAllByRole("button", { name: /New thread/ })
      .find((b) => b.textContent?.includes("New thread"))!;
    expect(cta.getAttribute("data-size")).toBe("sm");
    expect(cta.getAttribute("data-variant")).toBe("outline");
  });

  it("claims nothing before the list has arrived", () => {
    // No stored thread, so nothing to restore — but also no grounds yet for
    // telling someone with six live threads that they are all caught up.
    viewport("desktop");
    render(<App />);

    expect(screen.queryByText("All caught up")).toBeNull();
    expect(screen.queryByText("Nothing open")).toBeNull();
  });

  it("congratulates you when there is nothing at all", async () => {
    viewport("desktop");
    render(<App />);
    await act(async () => events.onThreads([]));

    expect(screen.getByText("All caught up")).toBeTruthy();
    expect(
      screen.getByText("Nothing is running. Put your feet up — or start something new."),
    ).toBeTruthy();
  });
});

describe("composer drafts", () => {
  const boot = async (kind: "phone" | "desktop" = "desktop") => {
    viewport(kind);
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a"), thread("b")]);
    });
  };

  const composer = () => screen.getByLabelText("Message") as HTMLTextAreaElement;

  const open = async (id: string) => {
    await act(async () => {
      fireEvent.click(screen.getByText(`Thread ${id}`));
      events.onState(id, state(id, "default"));
    });
  };

  const catalogue = [
    {
      id: "skill:alpha",
      name: "alpha",
      description: "Run alpha workflow",
      kind: "skill",
      trigger: "$",
      insertText: "$alpha",
      behavior: "prompt",
      origin: "project",
    },
    {
      id: "skill:beta",
      name: "beta",
      description: "Run beta workflow",
      kind: "skill",
      trigger: "$",
      insertText: "$beta",
      behavior: "prompt",
      origin: "user",
    },
  ];

  const useCatalogue = () => {
    command.mockImplementation(async (name: string) =>
      name === "list_composer_items" ? { items: catalogue } : ({} as any),
    );
  };

  it("reports a stop request the server could not deliver", async () => {
    command.mockImplementation(async (name: string) => {
      if (name === "cancel") throw new Error("bridge unavailable");
      return {} as any;
    });
    await boot();
    await open("a");
    await act(async () => events.onState("a", { ...state("a", "default"), phase: "turn" }));

    fireEvent.click(screen.getByRole("button", { name: "Interrupt the running turn" }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Could not stop the turn", {
        description: "bridge unavailable",
      }),
    );
  });

  it("keeps a half-typed message when you switch away and come back", async () => {
    await boot();
    await open("a");

    await act(async () => {
      fireEvent.change(composer(), { target: { value: "draft for a" } });
    });
    expect(composer().value).toBe("draft for a");

    // Switching threads unmounts the whole content subtree, Composer included.
    await open("b");
    expect(composer().value).toBe("");

    await open("a");
    expect(composer().value).toBe("draft for a");
  });

  it("clears the draft once the message is sent", async () => {
    await boot();
    await open("a");

    await act(async () => {
      fireEvent.change(composer(), { target: { value: "hello" } });
    });
    await act(async () => {
      fireEvent.keyDown(composer(), { key: "Enter" });
    });

    expect(command).toHaveBeenCalledWith("prompt", { threadId: "a", text: "hello" });
    expect(composer().value).toBe("");

    // Coming back to the thread shows the cleared field, not the sent text.
    await open("b");
    await open("a");
    expect(composer().value).toBe("");
  });

  it("opens the model picker for /model without sending it to the harness", async () => {
    command.mockImplementation(async () => ({} as any));
    await boot();
    await open("a");

    await act(async () => {
      fireEvent.focus(composer());
      fireEvent.change(composer(), { target: { value: "/model", selectionStart: 6 } });
      fireEvent.keyDown(composer(), { key: "Enter" });
    });

    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
    expect(composer().value).toBe("");
    await waitFor(() =>
      expect(screen.getByLabelText("Harness and model").getAttribute("aria-expanded")).toBe("true"),
    );
  });

  it("handles Codex status locally and routes native commands without prompting", async () => {
    const commands = [
      {
        id: "command:status",
        name: "status",
        kind: "command",
        trigger: "/",
        insertText: "/status",
        behavior: "client-action",
        action: "status",
      },
      {
        id: "command:compact",
        name: "compact",
        kind: "command",
        trigger: "/",
        insertText: "/compact",
        behavior: "adapter-action",
        action: "compact",
      },
      {
        id: "command:review",
        name: "review",
        kind: "command",
        trigger: "/",
        insertText: "/review",
        behavior: "adapter-action",
        action: "review",
      },
    ];
    command.mockImplementation(async (name: string) =>
      name === "list_composer_items" ? { items: commands } : ({} as any),
    );
    await boot();
    await open("a");
    await act(async () =>
      events.onState("a", {
        ...state("a", "on-request"),
        model: "gpt-test",
        usage: { contextUsed: 12_345 },
      }),
    );
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("list_composer_items", { threadId: "a" }),
    );

    fireEvent.change(composer(), { target: { value: "/status", selectionStart: 7 } });
    fireEvent.keyDown(composer(), { key: "Enter" });
    await waitFor(() =>
      expect(toast.info).toHaveBeenCalledWith("Thread status", {
        description: "gpt-test · on-request · 12,345 context tokens",
      }),
    );

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "/comp", selectionStart: 5 } });
    fireEvent.keyDown(composer(), { key: "Enter" });
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("run_composer_action", {
        threadId: "a",
        action: "compact",
        args: "",
        invocation: "/compact",
      }),
    );

    fireEvent.change(composer(), {
      target: { value: "/review focus on races", selectionStart: 22 },
    });
    fireEvent.keyDown(composer(), { key: "Enter" });
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("run_composer_action", {
        threadId: "a",
        action: "review",
        args: "focus on races",
        invocation: "/review focus on races",
      }),
    );
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
  });

  it("does not leak slash actions while the provider catalogue is still unknown", async () => {
    let resolveCatalogue!: (value: any) => void;
    const pendingCatalogue = new Promise((resolve) => {
      resolveCatalogue = resolve;
    });
    command.mockImplementation(async (name: string) =>
      name === "list_composer_items" ? pendingCatalogue : ({} as any),
    );
    await boot();
    await open("a");

    fireEvent.change(composer(), { target: { value: "/compact", selectionStart: 8 } });
    fireEvent.click(screen.getByRole("button", { name: "Send" }));
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
    expect(composer().value).toBe("/compact");

    await act(async () => resolveCatalogue({ items: [] }));
  });

  it("preserves text typed while a native action response is pending", async () => {
    let resolveAction!: (value: any) => void;
    const pendingAction = new Promise((resolve) => {
      resolveAction = resolve;
    });
    const review = {
      id: "command:review",
      name: "review",
      kind: "command",
      trigger: "/",
      insertText: "/review",
      behavior: "adapter-action",
      action: "review",
    };
    command.mockImplementation(async (name: string) => {
      if (name === "list_composer_items") return { items: [review] };
      if (name === "run_composer_action") return pendingAction;
      return {} as any;
    });
    await boot();
    await open("a");
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("list_composer_items", { threadId: "a" }),
    );

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "/review", selectionStart: 7 } });
    fireEvent.keyDown(composer(), { key: "Enter" });
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("run_composer_action", expect.anything()),
    );
    fireEvent.change(composer(), { target: { value: "my next message", selectionStart: 15 } });
    await act(async () => resolveAction({}));

    expect(composer().value).toBe("my next message");
  });

  it("inserts a provider-native skill completion without executing it", async () => {
    command.mockImplementation(async (name: string) =>
      name === "list_composer_items"
        ? {
            items: [
              {
                id: "skill:review",
                name: "review",
                kind: "skill",
                trigger: "$",
                insertText: "$review",
                behavior: "prompt",
                origin: "project",
              },
            ],
          }
        : ({} as any),
    );
    await boot();
    await open("a");
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("list_composer_items", { threadId: "a" }),
    );

    await act(async () => {
      fireEvent.focus(composer());
      fireEvent.change(composer(), { target: { value: "$rev", selectionStart: 4 } });
      fireEvent.keyDown(composer(), { key: "Enter" });
    });

    expect(composer().value).toBe("$review ");
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
  });

  it("refreshes the provider catalogue when the attached adapter invalidates it", async () => {
    useCatalogue();
    await boot();
    await open("a");
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("list_composer_items", { threadId: "a" }),
    );
    const loadsBefore = command.mock.calls.filter(([name]) => name === "list_composer_items").length;

    await act(async () => events.onComposerItemsChanged("a"));

    await waitFor(() =>
      expect(command.mock.calls.filter(([name]) => name === "list_composer_items").length).toBeGreaterThan(
        loadsBefore,
      ),
    );
  });

  it("supports arrow selection and Tab completion without sending", async () => {
    useCatalogue();
    await boot();
    await open("a");
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("list_composer_items", { threadId: "a" }),
    );

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "$", selectionStart: 1 } });
    fireEvent.keyDown(composer(), { key: "ArrowDown" });
    fireEvent.keyDown(composer(), { key: "Enter" });
    expect(composer().value).toBe("$beta ");

    fireEvent.change(composer(), { target: { value: "$al", selectionStart: 3 } });
    fireEvent.keyDown(composer(), { key: "Tab" });
    expect(composer().value).toBe("$alpha ");
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
  });

  it("does not send on Enter when completion has no matches or input is composing", async () => {
    useCatalogue();
    await boot();
    await open("a");

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "$zzz", selectionStart: 4 } });
    fireEvent.keyDown(composer(), { key: "Enter" });
    expect(composer().value).toBe("$zzz");

    fireEvent.change(composer(), { target: { value: "$al", selectionStart: 3 } });
    fireEvent.keyDown(composer(), { key: "Enter", keyCode: 229 });
    expect(composer().value).toBe("$al");
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
  });

  it("dismisses completion with Escape and shows it as a bottom sheet on a phone", async () => {
    useCatalogue();
    await boot("phone");
    await open("a");

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "$al", selectionStart: 3 } });
    const sheet = await screen.findByRole("dialog");
    expect(within(sheet).getByText("Commands")).toBeTruthy();
    // Scoped to the sheet: an empty transcript is also offering this command
    // as a recent, so the bare text is no longer unique to the completion.
    expect(within(sheet).getByText("Run alpha workflow")).toBeTruthy();

    fireEvent.keyDown(composer(), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(composer().value).toBe("$al");
  });

  it("keeps a new thread's draft while the list has not caught up with it", async () => {
    await boot();

    // Create a thread: it is attached, and can be typed into, before the
    // broadcast listing it arrives.
    await act(async () => {
      fireEvent.click(screen.getAllByRole("button", { name: /New thread/ })[0]);
    });
    command.mockImplementation(async (name: string) =>
      name === "create_thread" ? { threadId: "fresh" } : ({} as any),
    );
    fireEvent.change(document.querySelector("textarea")!, { target: { value: "go" } });
    await act(async () => {
      fireEvent.click(await screen.findByRole("button", { name: "Send" }));
    });
    await act(async () => events.onState("fresh", state("fresh", "default")));
    await act(async () => {
      fireEvent.change(composer(), { target: { value: "draft for fresh" } });
    });

    // Switch away, then a list lands that still predates the new thread. The
    // draft must not be pruned as if the thread were gone.
    await open("a");
    await act(async () => events.onThreads([thread("a"), thread("b")]));

    // The broadcast finally carries it; returning shows the draft intact.
    await act(async () => events.onThreads([thread("a"), thread("b"), thread("fresh")]));
    await open("fresh");
    expect(composer().value).toBe("draft for fresh");
  });
});

describe("losing the attached thread", () => {
  it("lets go even if the thread never sent a first snapshot", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => events.onThreads([thread("a"), thread("b")]));

    // Selecting clears state and waits for the server; deleting a row that is
    // not the open one goes through exactly this path, so a delete landing
    // before the first snapshot used to leave the app attached to nothing and
    // stuck on "Attaching…".
    await act(async () => {
      fireEvent.click(screen.getByText("Thread b"));
    });
    expect(attach).toHaveBeenCalledWith("b");

    await act(async () => events.onThreads([thread("a")]));

    expect(detach).toHaveBeenCalled();
    // On a phone that leaves nothing behind the sidebar, so it returns.
    expect(sidebarShowing()).toBe(true);
  });

  it("does not let go of a thread the list has not caught up with yet", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => {
      events.onThreads([thread("a")]);
      events.onProjects([project]);
      events.onHarnesses([harness]);
    });

    await act(async () => {
      fireEvent.click(screen.getAllByRole("button", { name: /New thread/ })[0]);
    });
    command.mockImplementation(async (name: string) =>
      name === "create_thread" ? { threadId: "fresh" } : ({} as any),
    );
    fireEvent.change(document.querySelector("textarea")!, { target: { value: "go" } });
    await act(async () => {
      fireEvent.click(await screen.findByRole("button", { name: "Send" }));
    });
    expect(attach).toHaveBeenCalledWith("fresh");

    // Creating attaches before the broadcast carrying the new thread
    // arrives, so for a moment the attached id is in no list at all. A list
    // that predates it must not read as "it is gone".
    await act(async () => events.onThreads([thread("a")]));
    expect(detach).not.toHaveBeenCalled();
    expect(sidebarShowing()).toBe(false);
  });
});

describe("resuming after a tab discard", () => {
  const seed = (id: string) => {
    localStorage.setItem("omniplex.lastThread", id);
    sessionStorage.setItem(
      "omniplex.resume",
      JSON.stringify({ build: "dev", state: state(id, "default"), scrollTop: 120, atBottom: false }),
    );
  };

  it("paints the cached thread immediately, with no Attaching…", async () => {
    viewport("phone");
    seed("a");
    render(<App />);

    // Before any frame from the server: the transcript is up, the header
    // names the thread, and nothing says "Attaching…".
    expect(screen.queryByText("Attaching…")).toBeNull();
    expect(screen.getByText("Thread a")).toBeTruthy();
    expect(sidebarShowing()).toBe(false);

    // The client was handed the cached state so its first attach carries a
    // cursor and the server replays only the gap.
    expect(prime).toHaveBeenCalledWith(expect.objectContaining({ threadId: "a", seq: 1 }));

    // The list confirming the thread exists changes nothing.
    await act(async () => events.onThreads([thread("a")]));
    expect(detach).not.toHaveBeenCalled();
    expect(screen.getByText("Thread a")).toBeTruthy();
  });

  it("lets go when the list reveals the thread is gone", async () => {
    viewport("phone");
    seed("a");
    render(<App />);
    expect(screen.getByText("Thread a")).toBeTruthy();

    // Deleted from elsewhere while the page was dead: released like a live
    // delete, and the phone lands back on the sidebar.
    await act(async () => events.onThreads([thread("b")]));
    expect(detach).toHaveBeenCalled();
    expect(sidebarShowing()).toBe(true);
  });

  it("ignores a cache written by a different bundle", async () => {
    viewport("phone");
    localStorage.setItem("omniplex.lastThread", "a");
    sessionStorage.setItem(
      "omniplex.resume",
      JSON.stringify({ build: "other", state: state("a", "default"), scrollTop: 0, atBottom: true }),
    );
    render(<App />);
    expect(prime).not.toHaveBeenCalled();
    // The cold path instead: restore once the list arrives.
    await act(async () => events.onThreads([thread("a")]));
    expect(attach).toHaveBeenCalledWith("a");
  });
});

describe("transcript scroll position", () => {
  // jsdom lays nothing out, so the scroller's geometry is stated outright:
  // a 1000px transcript in a 400px window, which is all `atBottom` reads.
  const CONTENT = 1000;
  const VIEWPORT = 400;

  beforeEach(() => {
    Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
      value: CONTENT,
      configurable: true,
    });
    Object.defineProperty(HTMLElement.prototype, "clientHeight", {
      value: VIEWPORT,
      configurable: true,
    });
  });
  afterEach(() => {
    delete (HTMLElement.prototype as any).scrollHeight;
    delete (HTMLElement.prototype as any).clientHeight;
  });

  const boot = async () => {
    viewport("desktop");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a"), thread("b")]);
    });
  };

  const open = async (id: string) => {
    await act(async () => {
      fireEvent.click(screen.getByText(`Thread ${id}`));
      events.onState(id, state(id, "default"));
    });
  };

  const scroller = () =>
    document.querySelector("main .overflow-y-auto.overscroll-contain") as HTMLElement;

  const scrollTo = async (top: number) => {
    await act(async () => {
      scroller().scrollTop = top;
      scroller().dispatchEvent(new Event("scroll"));
    });
  };

  it("comes back to where you were reading after a switch away", async () => {
    await boot();
    await open("a");
    await scrollTo(300);

    // Switching unmounts the transcript, so the position has to have been
    // kept somewhere outside it.
    await open("b");
    expect(scroller().scrollTop).not.toBe(300);

    await open("a");
    expect(scroller().scrollTop).toBe(300);
  });

  it("leaves a thread that was read to the tail following the tail", async () => {
    await boot();
    await open("a");
    await scrollTo(CONTENT - VIEWPORT);

    await open("b");
    await open("a");
    // At the bottom means pinned, not parked on an offset: no jump-to-bottom
    // button, because there is nowhere to jump to.
    expect(screen.queryByLabelText("Scroll to bottom")).toBeNull();
  });

  it("forgets a resumed position when the list says that thread is gone", async () => {
    // The cache hydrates a position for "a" before any list exists, so the
    // prune that follows the list has never seen the id — it has to be
    // dropped here or an id coming back would inherit a dead offset.
    localStorage.setItem("omniplex.lastThread", "a");
    sessionStorage.setItem(
      "omniplex.resume",
      JSON.stringify({ build: "dev", state: state("a", "default"), scrollTop: 300, atBottom: false }),
    );
    viewport("desktop");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("b")]);
    });
    await act(async () => events.onThreads([thread("a"), thread("b")]));
    await open("a");
    expect(scroller().scrollTop).not.toBe(300);
  });

  it("forgets the position of a thread that goes away", async () => {
    await boot();
    await open("a");
    await scrollTo(300);
    await act(async () => events.onThreads([thread("b")]));
    // The id coming back is a different thread wearing an old name; it must
    // not inherit a stranger's place in the transcript.
    await act(async () => events.onThreads([thread("a"), thread("b")]));
    await open("a");
    expect(scroller().scrollTop).not.toBe(300);
  });
});

// The empty transcript's nudge, end to end: what it offers comes from the
// thread's own catalogue and from what this project reached for before, and
// picking one writes into the composer rather than sending anything.
describe("recent skills on an empty transcript", () => {
  const catalogue = [
    {
      id: "skill:alpha",
      name: "alpha",
      description: "Run alpha workflow",
      kind: "skill",
      trigger: "/",
      insertText: "/alpha",
      behavior: "prompt",
      origin: "project",
    },
    {
      id: "skill:beta",
      name: "beta",
      description: "Run beta workflow",
      kind: "skill",
      trigger: "/",
      insertText: "/beta",
      behavior: "prompt",
      origin: "user",
    },
  ];

  const boot = async (kind: "phone" | "desktop" = "desktop") => {
    viewport(kind);
    command.mockImplementation(async (name: string) =>
      name === "list_composer_items" ? { items: catalogue } : ({} as any),
    );
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a")]);
    });
    await act(async () => {
      fireEvent.click(screen.getByText("Thread a"));
      events.onState("a", state("a", "default"));
    });
  };

  const composer = () => screen.getByLabelText("Message") as HTMLTextAreaElement;

  it("writes the token and a space into the composer, without sending", async () => {
    await boot();
    await act(async () => fireEvent.click(await screen.findByText("/beta")));

    expect(composer().value).toBe("/beta ");
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
  });

  it("takes the cursor with it on a desktop", async () => {
    await boot();
    await act(async () => fireEvent.click(await screen.findByText("/beta")));
    await waitFor(() => expect(document.activeElement).toBe(composer()));
  });

  it("leaves the keyboard down on a phone", async () => {
    await boot("phone");
    await act(async () => fireEvent.click(await screen.findByText("/beta")));

    expect(composer().value).toBe("/beta ");
    // Focusing here would raise the keyboard over the button just tapped.
    expect(document.activeElement).not.toBe(composer());
  });

  it("remembers what was sent, per project, and offers it first next time", async () => {
    await boot();
    await act(async () => {
      fireEvent.change(composer(), { target: { value: "/beta go" } });
      fireEvent.keyDown(composer(), { key: "Enter" });
    });

    expect(JSON.parse(localStorage.getItem("hy.recentSkills.v1:p1")!)).toEqual(["/beta"]);
    expect(localStorage.getItem("hy.recentSkills.v1:other")).toBeNull();
  });

  it("waits for a newly provisioned thread to be ready before loading skills", async () => {
    viewport("desktop");
    let catalogueRequests = 0;
    command.mockImplementation(async (name: string) => {
      if (name !== "list_composer_items") return {} as any;
      catalogueRequests += 1;
      return { items: catalogueRequests === 1 ? [] : catalogue };
    });
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("fresh")]);
    });
    fireEvent.click(screen.getByText("Thread fresh"));

    await act(async () =>
      events.onState("fresh", {
        ...state("fresh", "default"),
        phase: "provisioning",
        workspace: { phase: "provisioning", projectId: "p1", projectRoot: "/tmp/repo" },
      }),
    );
    expect(screen.queryByText("/alpha")).toBeNull();
    expect(catalogueRequests).toBe(1);

    await act(async () => events.onState("fresh", state("fresh", "default")));
    expect(await screen.findByText("/alpha")).toBeTruthy();
    expect(catalogueRequests).toBeGreaterThan(1);
    expect(command).toHaveBeenCalledWith("list_composer_items", { threadId: "fresh" });
  });
});

describe("attaching to a thread", () => {
  it("shows a centered loading state instead of the empty-thread action", async () => {
    viewport("desktop");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a")]);
    });

    fireEvent.click(screen.getByText("Thread a"));

    expect(screen.getByText("Attaching to thread…")).toBeTruthy();
    expect(within(document.querySelector("main")!).queryByRole("button", { name: "New thread" })).toBeNull();
    expect(screen.getByText("Attaching to thread…").parentElement?.getAttribute("aria-busy")).toBe(
      "true",
    );
  });
});

describe("a new thread's first message", () => {
  const start = async () => {
    viewport("desktop");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a")]);
    });
    await act(async () => {
      fireEvent.click(screen.getAllByRole("button", { name: /New thread/ })[0]);
    });
    command.mockImplementation(async (name: string) =>
      name === "create_thread" ? { threadId: "fresh" } : ({} as any),
    );
    fireEvent.change(document.querySelector("textarea")!, { target: { value: "read this" } });
  };
  const pick = (file: File) =>
    act(async () => {
      fireEvent.change(document.querySelector('input[type="file"]')!, {
        target: { files: [file] },
      });
    });
  const report = new File(["numbers"], "report.txt", { type: "text/plain" });

  beforeEach(() => {
    uploadStaged.mockReset();
    command.mockReset();
    command.mockImplementation(async () => ({}) as any);
  });

  it("starts the thread empty, uploads the files to it, then sends the message with them", async () => {
    uploadStaged.mockResolvedValue({
      kind: "file",
      status: "ready",
      artefactId: "art-1",
      progress: 1,
    });
    await start();
    await pick(report);
    expect(uploadStaged).not.toHaveBeenCalled();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });

    expect(command).toHaveBeenCalledWith("create_thread", expect.objectContaining({ text: "" }));
    expect(uploadStaged).toHaveBeenCalledWith("fresh", report, expect.anything());
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("prompt", {
        threadId: "fresh",
        text: "read this",
        files: [{ artefactId: "art-1" }],
      }),
    );
  });

  it("leaves the message in the new thread when a file fails to upload", async () => {
    uploadStaged.mockRejectedValue(new Error("disk full"));
    await start();
    await pick(report);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });
    await act(async () => events.onState("fresh", state("fresh", "default")));

    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
    expect((screen.getByLabelText("Message") as HTMLTextAreaElement).value).toBe("read this");
  });

  it("sends a plain message with the thread", async () => {
    await start();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });
    expect(command).toHaveBeenCalledWith(
      "create_thread",
      expect.objectContaining({ text: "read this" }),
    );
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
  });

  it("schedules: starts the thread empty and opens the schedule with the message", async () => {
    await start();
    fireEvent.pointerDown(screen.getByRole("button", { name: "More send options" }), {
      button: 0,
      ctrlKey: false,
    });
    await act(async () => {
      fireEvent.click(await screen.findByRole("menuitem", { name: /Schedule send/ }));
    });
    await act(async () => events.onState("fresh", state("fresh", "default")));

    expect(command).toHaveBeenCalledWith("create_thread", expect.objectContaining({ text: "" }));
    expect(await screen.findByRole("dialog", { name: "Schedule message" })).toBeTruthy();
    expect(command).not.toHaveBeenCalledWith("prompt", expect.anything());
  });
});

describe("the setup page", () => {
  const setupReport = {
    platform: "darwin",
    ready: false,
    checks: [
      { id: "git", name: "Git", kind: "tool", availability: { state: "ready" } },
      {
        id: "claude",
        name: "Claude Code",
        kind: "harness",
        availability: {
          state: "unavailable",
          reason: "Claude is not signed in.",
          remedy: [{ text: "Sign in", command: "claude auth login", action: "login" }],
        },
      },
    ],
  };

  beforeEach(() => {
    window.history.replaceState(null, "", "/setup");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url !== "/api/setup") throw new Error(`unexpected fetch ${url}`);
        return new Response(JSON.stringify(setupReport));
      }),
    );
  });
  afterEach(() => window.history.replaceState(null, "", "/"));

  it("opens at /setup and leaves for the app on continue", async () => {
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));
    await screen.findByRole("article", { name: "Claude Code" });
    expect(screen.queryByText("Thread a")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(window.location.pathname).toBe("/");
    expect(screen.getByText("Thread a")).toBeTruthy();
  });

  it("signs in through the app's own sign-in flow", async () => {
    render(<App />);
    await act(async () =>
      events.onHarnesses([
        { ...harness, instances: [{ ...harness.instances[0], id: "claude-work", auth: "flows" }] },
      ]),
    );
    const card = await screen.findByRole("article", { name: "Claude Code" });

    fireEvent.click(within(card).getByRole("button"));

    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("provider_auth_overview", { instanceId: "claude-work" }),
    );
  });
});
