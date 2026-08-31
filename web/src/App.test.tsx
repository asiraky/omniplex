// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { render, viewport } from "~/test/harness";
import type { ClientEvents } from "./client";
import type { SessionMeta } from "./protocol";

// The socket is the app's only source of truth, so the tests own it: this
// captures the callbacks App hands the client and lets each test decide when
// the session list arrives — which is the whole subject of these tests.
let events: ClientEvents;
const command = vi.fn(async (_name: string, _args: unknown) => ({}) as any);
const attach = vi.fn();
const detach = vi.fn();
const prime = vi.fn();
const toast = vi.hoisted(() => ({ error: vi.fn(), info: vi.fn() }));

vi.mock("sonner", () => ({ toast }));

vi.mock("./client", () => ({
  wsURL: () => "ws://test",
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
  root: "/tmp/repo",
  config: {
    name: "repo",
    defaults: { harness: "claude", harnesses: {}, workspace: "local" },
    workspace: {},
  },
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

const session = (id: string): SessionMeta =>
  ({
    id,
    title: `Session ${id}`,
    phase: "idle",
    updatedAt: Date.now(),
    cwd: "/tmp/repo",
    harness: "claude",
    projectId: "p1",
    branch: "main",
  }) as SessionMeta;

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
  sessionId: id,
  seq: 1,
  cwd: "/tmp/repo",
  harness: "claude",
  model: "",
  mode,
  effort: "",
  title: `Session ${id}`,
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

describe("copying a transcript", () => {
  it("copies only the raw user and assistant prose from the session header", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("a")]);
    });
    fireEvent.click(screen.getByText("Session a"));
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

describe("session actions on a phone", () => {
  const openSession = async () => {
    viewport("phone");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("a")]);
    });
    await act(async () => {
      fireEvent.click(screen.getByText("Session a"));
      events.onState("a", state("a", "default"));
    });
  };

  it("puts the header actions in one overflow menu", async () => {
    await openSession();
    await act(async () =>
      events.onLabels([
        { id: "label-1", name: "Parked", color: "#f59e0b", position: 0, createdAt: 1 },
      ]),
    );

    expect(screen.queryByRole("button", { name: "Copy transcript" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Summarise this session" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Label this session" })).toBeNull();
    fireEvent.pointerDown(screen.getByRole("button", { name: "More session actions" }), {
      button: 0,
      ctrlKey: false,
    });

    expect(screen.getByRole("menuitem", { name: "Open panel" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Summarise session" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Copy transcript" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Sign in again to Claude Code" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "repo settings" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Label session" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: /diff/i })).toBeNull();
  });

  it("keeps provider sign-in available while the session is attaching", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("a")]);
    });

    fireEvent.click(screen.getByText("Session a"));

    expect(screen.getByText("Attaching…")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Sign in again to Claude Code" })).toBeTruthy();
  });

  it("opens the whole panel directly, with terminal available from its surface menu", async () => {
    await openSession();

    fireEvent.pointerDown(screen.getByRole("button", { name: "More session actions" }), {
      button: 0,
      ctrlKey: false,
    });
    fireEvent.click(screen.getByRole("menuitem", { name: "Open panel" }));

    const panel = await screen.findByRole("dialog", { name: "Session panel" });
    fireEvent.pointerDown(within(panel).getByRole("button", { name: "Open a surface" }), {
      button: 0,
      ctrlKey: false,
    });
    expect(await screen.findByRole("menuitem", { name: "Terminal" })).toBeTruthy();
  });
});

describe("a bypass session is just a session", () => {
  it("opens with no confirmation, banner, or acknowledgement", async () => {
    const confirm = vi.fn(() => true);
    vi.stubGlobal("confirm", confirm);
    localStorage.setItem("omniplex.lastSession", "a");
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
        "/tmp/repo",
      );
      events.onSessions([session("a")]);
      events.onState("a", state("a", "bypassPermissions"));
    });

    expect(confirm).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toMatch(
      /are you sure|without asking you first|acknowledge|proceed with caution/i,
    );
  });
});

describe("landing on a phone", () => {
  it("lands on the session list when there is nothing to restore", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => events.onSessions([session("a")]));

    expect(sidebarShowing()).toBe(true);
  });

  it("still lands on the session list when there are no sessions at all", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => events.onSessions([]));

    expect(sidebarShowing()).toBe(true);
    expect(screen.getByText("All caught up")).toBeTruthy();
  });

  it("restores straight into the last session without flashing the list open", async () => {
    localStorage.setItem("omniplex.lastSession", "a");
    viewport("phone");
    render(<App />);

    // Before the list lands we do not yet know whether "a" still exists, so
    // the sidebar must not be shown only to be shut a frame later.
    expect(sidebarShowing()).toBe(false);

    await act(async () => events.onSessions([session("a")]));
    expect(attach).toHaveBeenCalledWith("a");
    expect(sidebarShowing()).toBe(false);
  });

  it("falls back to the list when the stored session is gone", async () => {
    localStorage.setItem("omniplex.lastSession", "gone");
    viewport("phone");
    render(<App />);
    await act(async () => events.onSessions([session("a")]));

    expect(attach).not.toHaveBeenCalled();
    await waitFor(() => expect(sidebarShowing()).toBe(true));
  });

  it("says nothing while it is still deciding", async () => {
    localStorage.setItem("omniplex.lastSession", "a");
    viewport("phone");
    render(<App />);

    // Neither empty-state message: both would be contradicted a moment later.
    expect(screen.queryByText("All caught up")).toBeNull();
    expect(screen.queryByText("Nothing open")).toBeNull();
    expect(screen.getByText("Reopening your last session…")).toBeTruthy();
  });
});

describe("the empty content column", () => {
  it("points at the list when there are sessions to pick from", async () => {
    viewport("desktop");
    render(<App />);
    await act(async () => events.onSessions([session("a")]));

    expect(screen.getByText("Nothing open")).toBeTruthy();
    // The action is still offered, but quietly: no oversized call to action
    // competing with the list of sessions beside it.
    const cta = screen
      .getAllByRole("button", { name: /New session/ })
      .find((b) => b.textContent?.includes("New session"))!;
    expect(cta.getAttribute("data-size")).toBe("sm");
    expect(cta.getAttribute("data-variant")).toBe("outline");
  });

  it("claims nothing before the list has arrived", () => {
    // No stored session, so nothing to restore — but also no grounds yet for
    // telling someone with six live sessions that they are all caught up.
    viewport("desktop");
    render(<App />);

    expect(screen.queryByText("All caught up")).toBeNull();
    expect(screen.queryByText("Nothing open")).toBeNull();
  });

  it("congratulates you when there is nothing at all", async () => {
    viewport("desktop");
    render(<App />);
    await act(async () => events.onSessions([]));

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
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("a"), session("b")]);
    });
  };

  const composer = () => screen.getByLabelText("Message") as HTMLTextAreaElement;

  const open = async (id: string) => {
    await act(async () => {
      fireEvent.click(screen.getByText(`Session ${id}`));
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

    // Switching sessions unmounts the whole content subtree, Composer included.
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

    expect(command).toHaveBeenCalledWith("prompt", { sessionId: "a", text: "hello" });
    expect(composer().value).toBe("");

    // Coming back to the session shows the cleared field, not the sent text.
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
      expect(command).toHaveBeenCalledWith("list_composer_items", { sessionId: "a" }),
    );

    fireEvent.change(composer(), { target: { value: "/status", selectionStart: 7 } });
    fireEvent.keyDown(composer(), { key: "Enter" });
    expect(toast.info).toHaveBeenCalledWith("Session status", {
      description: "gpt-test · on-request · 12,345 context tokens",
    });

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "/comp", selectionStart: 5 } });
    fireEvent.keyDown(composer(), { key: "Enter" });
    await waitFor(() =>
      expect(command).toHaveBeenCalledWith("run_composer_action", {
        sessionId: "a",
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
        sessionId: "a",
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
      expect(command).toHaveBeenCalledWith("list_composer_items", { sessionId: "a" }),
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
      expect(command).toHaveBeenCalledWith("list_composer_items", { sessionId: "a" }),
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
      expect(command).toHaveBeenCalledWith("list_composer_items", { sessionId: "a" }),
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
      expect(command).toHaveBeenCalledWith("list_composer_items", { sessionId: "a" }),
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

  it("keeps a new session's draft while the list has not caught up with it", async () => {
    await boot();

    // Create a session: it is attached, and can be typed into, before the
    // broadcast listing it arrives.
    await act(async () => {
      fireEvent.click(screen.getAllByRole("button", { name: /New session/ })[0]);
    });
    command.mockImplementation(async (name: string) =>
      name === "create_session" ? { sessionId: "fresh" } : ({} as any),
    );
    await act(async () => {
      fireEvent.click(await screen.findByRole("button", { name: "Start" }));
    });
    await act(async () => events.onState("fresh", state("fresh", "default")));
    await act(async () => {
      fireEvent.change(composer(), { target: { value: "draft for fresh" } });
    });

    // Switch away, then a list lands that still predates the new session. The
    // draft must not be pruned as if the session were gone.
    await open("a");
    await act(async () => events.onSessions([session("a"), session("b")]));

    // The broadcast finally carries it; returning shows the draft intact.
    await act(async () => events.onSessions([session("a"), session("b"), session("fresh")]));
    await open("fresh");
    expect(composer().value).toBe("draft for fresh");
  });
});

describe("losing the attached session", () => {
  it("lets go even if the session never sent a first snapshot", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => events.onSessions([session("a"), session("b")]));

    // Selecting clears state and waits for the server; deleting a row that is
    // not the open one goes through exactly this path, so a delete landing
    // before the first snapshot used to leave the app attached to nothing and
    // stuck on "Attaching…".
    await act(async () => {
      fireEvent.click(screen.getByText("Session b"));
    });
    expect(attach).toHaveBeenCalledWith("b");

    await act(async () => events.onSessions([session("a")]));

    expect(detach).toHaveBeenCalled();
    // On a phone that leaves nothing behind the sidebar, so it returns.
    expect(sidebarShowing()).toBe(true);
  });

  it("does not let go of a session the list has not caught up with yet", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => {
      events.onSessions([session("a")]);
      events.onProjects([project]);
      events.onHarnesses([harness], "/tmp/repo");
    });

    await act(async () => {
      fireEvent.click(screen.getAllByRole("button", { name: /New session/ })[0]);
    });
    command.mockImplementation(async (name: string) =>
      name === "create_session" ? { sessionId: "fresh" } : ({} as any),
    );
    await act(async () => {
      fireEvent.click(await screen.findByRole("button", { name: "Start" }));
    });
    expect(attach).toHaveBeenCalledWith("fresh");

    // Creating attaches before the broadcast carrying the new session
    // arrives, so for a moment the attached id is in no list at all. A list
    // that predates it must not read as "it is gone".
    await act(async () => events.onSessions([session("a")]));
    expect(detach).not.toHaveBeenCalled();
    expect(sidebarShowing()).toBe(false);
  });
});

describe("resuming after a tab discard", () => {
  const seed = (id: string) => {
    localStorage.setItem("omniplex.lastSession", id);
    sessionStorage.setItem(
      "omniplex.resume",
      JSON.stringify({ build: "dev", state: state(id, "default"), scrollTop: 120, atBottom: false }),
    );
  };

  it("paints the cached session immediately, with no Attaching…", async () => {
    viewport("phone");
    seed("a");
    render(<App />);

    // Before any frame from the server: the transcript is up, the header
    // names the session, and nothing says "Attaching…".
    expect(screen.queryByText("Attaching…")).toBeNull();
    expect(screen.getByText("Session a")).toBeTruthy();
    expect(sidebarShowing()).toBe(false);

    // The client was handed the cached state so its first attach carries a
    // cursor and the server replays only the gap.
    expect(prime).toHaveBeenCalledWith(expect.objectContaining({ sessionId: "a", seq: 1 }));

    // The list confirming the session exists changes nothing.
    await act(async () => events.onSessions([session("a")]));
    expect(detach).not.toHaveBeenCalled();
    expect(screen.getByText("Session a")).toBeTruthy();
  });

  it("lets go when the list reveals the session is gone", async () => {
    viewport("phone");
    seed("a");
    render(<App />);
    expect(screen.getByText("Session a")).toBeTruthy();

    // Deleted from elsewhere while the page was dead: released like a live
    // delete, and the phone lands back on the sidebar.
    await act(async () => events.onSessions([session("b")]));
    expect(detach).toHaveBeenCalled();
    expect(sidebarShowing()).toBe(true);
  });

  it("ignores a cache written by a different bundle", async () => {
    viewport("phone");
    localStorage.setItem("omniplex.lastSession", "a");
    sessionStorage.setItem(
      "omniplex.resume",
      JSON.stringify({ build: "other", state: state("a", "default"), scrollTop: 0, atBottom: true }),
    );
    render(<App />);
    expect(prime).not.toHaveBeenCalled();
    // The cold path instead: restore once the list arrives.
    await act(async () => events.onSessions([session("a")]));
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
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("a"), session("b")]);
    });
  };

  const open = async (id: string) => {
    await act(async () => {
      fireEvent.click(screen.getByText(`Session ${id}`));
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

  it("leaves a session that was read to the tail following the tail", async () => {
    await boot();
    await open("a");
    await scrollTo(CONTENT - VIEWPORT);

    await open("b");
    await open("a");
    // At the bottom means pinned, not parked on an offset: no jump-to-bottom
    // button, because there is nowhere to jump to.
    expect(screen.queryByLabelText("Scroll to bottom")).toBeNull();
  });

  it("forgets a resumed position when the list says that session is gone", async () => {
    // The cache hydrates a position for "a" before any list exists, so the
    // prune that follows the list has never seen the id — it has to be
    // dropped here or an id coming back would inherit a dead offset.
    localStorage.setItem("omniplex.lastSession", "a");
    sessionStorage.setItem(
      "omniplex.resume",
      JSON.stringify({ build: "dev", state: state("a", "default"), scrollTop: 300, atBottom: false }),
    );
    viewport("desktop");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("b")]);
    });
    await act(async () => events.onSessions([session("a"), session("b")]));
    await open("a");
    expect(scroller().scrollTop).not.toBe(300);
  });

  it("forgets the position of a session that goes away", async () => {
    await boot();
    await open("a");
    await scrollTo(300);
    await act(async () => events.onSessions([session("b")]));
    // The id coming back is a different session wearing an old name; it must
    // not inherit a stranger's place in the transcript.
    await act(async () => events.onSessions([session("a"), session("b")]));
    await open("a");
    expect(scroller().scrollTop).not.toBe(300);
  });
});

// The empty transcript's nudge, end to end: what it offers comes from the
// session's own catalogue and from what this project reached for before, and
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
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("a")]);
    });
    await act(async () => {
      fireEvent.click(screen.getByText("Session a"));
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

  it("waits for a newly provisioned session to be ready before loading skills", async () => {
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
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("fresh")]);
    });
    fireEvent.click(screen.getByText("Session fresh"));

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
    expect(command).toHaveBeenCalledWith("list_composer_items", { sessionId: "fresh" });
  });
});

describe("attaching to a session", () => {
  it("shows a centered loading state instead of the empty-session action", async () => {
    viewport("desktop");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness], "/tmp/repo");
      events.onSessions([session("a")]);
    });

    fireEvent.click(screen.getByText("Session a"));

    expect(screen.getByText("Attaching to session…")).toBeTruthy();
    expect(within(document.querySelector("main")!).queryByRole("button", { name: "New session" })).toBeNull();
    expect(screen.getByText("Attaching to session…").parentElement?.getAttribute("aria-busy")).toBe(
      "true",
    );
  });
});
