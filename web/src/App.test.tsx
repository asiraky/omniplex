// @vitest-environment jsdom
import { act, cleanup, fireEvent, render as rtlRender, screen, waitFor, within } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { render, viewport, wrap } from "~/test/harness";
import type { ClientEvents } from "./client";
import type { ThreadMeta } from "./protocol";

// The socket is the app's only source of truth, so the tests own it: this
// captures the callbacks App hands the client and lets each test decide when
// the thread list arrives — which is the whole subject of these tests.
let events: ClientEvents;
const command = vi.fn(async (_name: string, _args: unknown, _commandId?: string) => ({}) as any);
// Every prompt the app sent, each under the outbox's command id.
const promptsSent = () => command.mock.calls.filter(([name]) => name === "prompt");
const attach = vi.fn();
const detach = vi.fn();
const prime = vi.fn();
const setPresence = vi.fn();
const toast = vi.hoisted(() => ({ error: vi.fn(), info: vi.fn(), success: vi.fn(), warning: vi.fn() }));

vi.mock("sonner", () => ({ toast }));
const uploadStaged = vi.hoisted(() => vi.fn());
vi.mock("./lib/attachments", async (actual) => ({
  ...(await actual<typeof import("./lib/attachments")>()),
  uploadStaged,
}));

// How many times the app and the transcript have drawn: each probe is a hook
// the one calls on every render and nothing else does, counted and then run
// for real.
const renders = vi.hoisted(() => ({ app: 0, transcript: 0 }));
vi.mock("./app/useThreadTitle", async (actual) => {
  const real = await actual<typeof import("./app/useThreadTitle")>();
  return {
    useThreadTitle: (...args: Parameters<typeof real.useThreadTitle>) => {
      renders.app++;
      return real.useThreadTitle(...args);
    },
  };
});
vi.mock("./components/transcript/useTranscriptModel", async (actual) => {
  const real = await actual<typeof import("./components/transcript/useTranscriptModel")>();
  return {
    ...real,
    useTranscriptModel: (...args: Parameters<typeof real.useTranscriptModel>) => {
      renders.transcript++;
      return real.useTranscriptModel(...args);
    },
  };
});

const { CommandTimeout } = vi.hoisted(() => ({
  CommandTimeout: class extends Error {
    constructor(
      readonly command: string,
      readonly commandId: string,
    ) {
      super("No answer from the server. Check the connection and try again.");
    }
  },
}));

vi.mock("./client", () => ({
  CommandTimeout,
  wsURL: () => "ws://test",
  uuid: () => Math.random().toString(36).slice(2),
  Client: class {
    constructor(_url: string, e: ClientEvents) {
      events = e;
    }
    connect() {
      events.onStatus("online");
    }
    closed = false;
    close() {
      this.closed = true;
    }
    detach = detach;
    attach = attach;
    command = command;
    prime = prime;
    setPresence = setPresence;
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
  // Picking a thread names it in the URL, and the page reads its URL at boot.
  window.history.replaceState(null, "", "/");
  localStorage.clear();
  sessionStorage.clear();
  Element.prototype.scrollIntoView = vi.fn();
  command.mockClear();
  attach.mockClear();
  detach.mockClear();
  prime.mockClear();
  setPresence.mockClear();
  toast.error.mockClear();
  toast.info.mockClear();
  toast.warning.mockClear();
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
  serviceTier: "",
  title: `Thread ${id}`,
  phase: "idle",
  closed: false,
  workspace: { phase: "ready", projectId: "p1", projectRoot: "/tmp/repo" },
  items: [],
  turns: [],
  jobs: [],
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
    // Shift-click: straight to a draft, past the "New thread in…" step.
    fireEvent.click(screen.getAllByRole("button", { name: /New thread/ })[0], { shiftKey: true });
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

describe("pinning", () => {
  const rows = () =>
    Array.from(document.querySelectorAll('[aria-label^="Delete thread"]')).map((el) =>
      el.getAttribute("aria-label")!.replace("Delete thread ", ""),
    );
  const load = async () => {
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([
        { ...thread("a"), position: 0 },
        { ...thread("b"), position: 1 },
        { ...thread("c"), position: 2 },
      ]);
    });
  };

  it("moves a pinned row to the top at once, and tells the server", async () => {
    await load();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Pin thread Thread c" }));
    });

    expect(command).toHaveBeenCalledWith("set_thread_pinned", { threadId: "c", pinned: true });
    expect(screen.getByRole("button", { name: "Pinned, 1 thread" })).toBeTruthy();
    expect(rows()).toEqual(["Thread c", "Thread a", "Thread b"]);
  });

  it("puts the row back, and says so, when the pin fails", async () => {
    await load();
    command.mockImplementationOnce(async () => {
      throw new Error("offline");
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Pin thread Thread c" }));
    });

    expect(toast.error).toHaveBeenCalledWith("Could not pin that thread", expect.anything());
    expect(screen.queryByRole("button", { name: "Pinned, 1 thread" })).toBeNull();
    expect(rows()).toEqual(["Thread a", "Thread b", "Thread c"]);
  });
});

describe("picking the thread already open", () => {
  const openThread = async () => {
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a"), thread("b")]);
    });
    await act(async () => {
      fireEvent.click(screen.getByText("Thread a"));
      events.onState("a", state("a", "default"));
    });
    attach.mockClear();
  };
  // The header names the open thread too; the pick is the sidebar row.
  const row = (id: string) =>
    within(screen.getByRole("navigation", { name: "Threads" })).getByText(`Thread ${id}`);

  it("leaves it as it is, with no re-attach", async () => {
    await openThread();

    fireEvent.click(row("a"));

    expect(attach).not.toHaveBeenCalled();
    expect(screen.queryByText("Attaching to thread…")).toBeNull();
    expect(screen.queryByText("Attaching…")).toBeNull();
  });

  it("keeps the side panel open", async () => {
    await openThread();
    fireEvent.click(screen.getByRole("button", { name: "Show the panel" }));

    fireEvent.click(row("a"));

    expect(screen.getByRole("button", { name: "Hide the panel" })).toBeTruthy();
  });

  it("still switches to a different thread", async () => {
    await openThread();

    fireEvent.click(row("b"));

    expect(attach).toHaveBeenCalledWith("b");
  });

  it("puts the phone's list away to show it", async () => {
    viewport("phone");
    await openThread();
    fireEvent.click(screen.getByRole("button", { name: "Show threads" }));
    expect(sidebarShowing()).toBe(true);

    fireEvent.click(row("a"));

    expect(sidebarShowing()).toBe(false);
    expect(attach).not.toHaveBeenCalled();
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

    expect(screen.queryByRole("button", { name: "Copy transcript" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Pin thread" })).toBeNull();
    fireEvent.pointerDown(screen.getByRole("button", { name: "More thread actions" }), {
      button: 0,
      ctrlKey: false,
    });

    expect(screen.getByRole("menuitem", { name: "Open panel" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Copy transcript" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: /diff/i })).toBeNull();

    fireEvent.click(screen.getByRole("menuitem", { name: "Pin thread" }));
    expect(command).toHaveBeenCalledWith("set_thread_pinned", { threadId: "a", pinned: true });
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

describe("harness extensions", () => {
  const openThread = async (kind: "phone" | "desktop", extensionStatuses: unknown[] = []) => {
    viewport(kind);
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a"), thread("b")]);
    });
    await act(async () => {
      fireEvent.click(screen.getByText("Thread a"));
      events.onState("a", { ...state("a", "default"), extensionStatuses });
    });
  };
  const statuses = () => screen.queryByRole("list", { name: "Extension status" });

  it.each(["phone", "desktop"] as const)("shows each status in the header on a %s, until cleared", async (kind) => {
    await openThread(kind, [
      { key: "git", text: "main*" },
      { key: "tests", text: "3 failing" },
    ]);
    expect(within(statuses()!).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["main*", "3 failing"]);

    await act(async () => events.onState("a", { ...state("a", "default"), extensionStatuses: [] }));
    expect(statuses()).toBeNull();
  });

  it("shows no status line when no extension set one", async () => {
    await openThread("phone");
    expect(statuses()).toBeNull();
  });

  it("toasts what the open thread's extension says, at its level", async () => {
    await openThread("desktop");
    const notified = (threadId: string, seq: number, level: string, message: string) =>
      events.onLiveEvent(threadId, { threadId, seq, timestamp: 0, type: "extension.notified", payload: { level, message } });

    await act(async () => {
      notified("a", 2, "warning", "No API key for lint");
      notified("a", 3, "error", "Formatter crashed");
      notified("a", 4, "info", "Ready");
      notified("b", 5, "error", "Not on screen");
      events.onLiveEvent("a", { threadId: "a", seq: 6, timestamp: 0, type: "plan.updated", payload: { entries: [] } });
    });
    await waitFor(() => expect(toast.info).toHaveBeenCalledWith("Ready"));
    expect(toast.warning.mock.calls).toEqual([["No API key for lint"]]);
    expect(toast.error.mock.calls).toEqual([["Formatter crashed"]]);
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

describe("opening a thread from a notification", () => {
  // The page's service worker container, standing in for the real one so a
  // test can post the "open-thread" message the worker sends on a tap.
  let sw: EventTarget;
  beforeEach(() => {
    sw = new EventTarget();
    Object.defineProperty(navigator, "serviceWorker", { value: sw, configurable: true });
  });
  afterEach(() => {
    delete (navigator as { serviceWorker?: unknown }).serviceWorker;
    window.history.replaceState(null, "", "/");
  });
  const tap = (threadId: string) =>
    act(() => {
      sw.dispatchEvent(new MessageEvent("message", { data: { type: "open-thread", threadId } }));
    });

  it("opens /thread/<id> ahead of the last thread, and stays on it", async () => {
    window.history.replaceState(null, "", "/thread/b");
    localStorage.setItem("omniplex.lastThread", "a");
    viewport("phone");
    render(<App />);
    // About to open a thread, so the phone's list must not flash open.
    expect(sidebarShowing()).toBe(false);

    await act(async () => events.onThreads([thread("a"), thread("b")]));
    expect(attach).toHaveBeenCalledWith("b");
    expect(attach).not.toHaveBeenCalledWith("a");
    expect(window.location.pathname).toBe("/thread/b");
    expect(sidebarShowing()).toBe(false);
  });

  it("does not paint the cached last thread when the link names another", () => {
    window.history.replaceState(null, "", "/thread/b");
    localStorage.setItem("omniplex.lastThread", "a");
    sessionStorage.setItem(
      "omniplex.resume",
      JSON.stringify({ build: "dev", state: state("a", "default"), position: { top: 0, atBottom: true } }),
    );
    render(<App />);
    expect(prime).not.toHaveBeenCalled();
  });

  it("falls back to the last thread when the linked one is gone", async () => {
    window.history.replaceState(null, "", "/thread/gone");
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));
    expect(attach).toHaveBeenCalledWith("a");
    expect(window.location.pathname).toBe("/thread/a");
  });

  it("switches thread when the worker names one in an open window", async () => {
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    await act(async () => events.onThreads([thread("a"), thread("b")]));
    attach.mockClear();

    tap("b");
    expect(attach).toHaveBeenCalledWith("b");
  });

  it("holds a tapped thread the list does not have yet", async () => {
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));
    tap("gone");
    expect(attach).not.toHaveBeenCalled();
  });

  it("holds a tap on a thread made elsewhere until the refreshed list brings it", async () => {
    // The phone slept on thread a; a thread was started from another device,
    // and its notification's tap beats the list that carries it.
    viewport("phone");
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));
    attach.mockClear();

    tap("new");
    expect(attach).not.toHaveBeenCalled();

    await act(async () => events.onThreads([thread("a"), thread("new")]));
    expect(attach).toHaveBeenCalledWith("new");
    expect(sidebarShowing()).toBe(false);
  });

  it("drops a held tap once the user picks a thread themselves", async () => {
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    await act(async () => events.onThreads([thread("a"), thread("b")]));
    tap("new");
    fireEvent.click(screen.getByText("Thread b"));
    attach.mockClear();

    await act(async () => events.onThreads([thread("a"), thread("b"), thread("new")]));
    expect(attach).not.toHaveBeenCalled();
  });

  it("follows the newest tap when an older one is still held", async () => {
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));
    tap("old");
    tap("new");
    attach.mockClear();

    await act(async () => events.onThreads([thread("a"), thread("old"), thread("new")]));
    expect(attach).toHaveBeenCalledWith("new");
    expect(attach).not.toHaveBeenCalledWith("old");
  });

  it("holds a tap that lands before the list, and opens it once the list arrives", async () => {
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    tap("b");
    expect(attach).not.toHaveBeenCalled();

    await act(async () => events.onThreads([thread("a"), thread("b")]));
    expect(attach).toHaveBeenCalledWith("b");
    expect(attach).not.toHaveBeenCalledWith("a");
  });
});

describe("moving between threads", () => {
  it("names the picked thread in the URL, and Back goes to the one before", async () => {
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a"), thread("b")]);
    });
    fireEvent.click(screen.getByText("Thread a"));
    fireEvent.click(screen.getByText("Thread b"));
    expect(window.location.pathname).toBe("/thread/b");
    attach.mockClear();

    const popped = new Promise((r) => window.addEventListener("popstate", r, { once: true }));
    await act(async () => {
      window.history.back();
      await popped;
    });
    expect(window.location.pathname).toBe("/thread/a");
    expect(attach).toHaveBeenCalledWith("a");
  });
});

describe("presence", () => {
  const last = () => setPresence.mock.calls.at(-1);
  const setVisibility = (v: DocumentVisibilityState) => {
    Object.defineProperty(document, "visibilityState", { value: v, configurable: true });
    act(() => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
  };
  afterEach(() => {
    delete (document as { visibilityState?: unknown }).visibilityState;
  });

  it("reports the open thread, and again when the page is hidden and shown", async () => {
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    expect(last()).toEqual([true, ""]);

    await act(async () => events.onThreads([thread("a"), thread("b")]));
    expect(last()).toEqual([true, "a"]);

    fireEvent.click(screen.getByText("Thread b"));
    expect(last()).toEqual([true, "b"]);

    setVisibility("hidden");
    expect(last()).toEqual([false, "b"]);
    setVisibility("visible");
    expect(last()).toEqual([true, "b"]);
  });

  it("reports no thread while the phone's list covers it", async () => {
    viewport("phone");
    render(<App />);
    await act(async () => events.onThreads([thread("a")]));
    // Nothing restored, so the phone lands on the list.
    expect(sidebarShowing()).toBe(true);
    fireEvent.click(screen.getByText("Thread a"));
    expect(last()).toEqual([true, "a"]);

    // Back to the list: the thread is still selected but no longer on screen.
    fireEvent.click(screen.getByRole("button", { name: "Show threads" }));
    expect(sidebarShowing()).toBe(true);
    expect(last()).toEqual([true, ""]);
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
  const boot = async (kind: "phone" | "desktop" = "desktop", { strict = false } = {}) => {
    viewport(kind);
    // At the root, as main.tsx has it: under the providers it mounts once.
    if (strict) rtlRender(<StrictMode>{wrap(<App />)}</StrictMode>);
    else render(<App />);
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

    expect(command).toHaveBeenCalledWith(
      "prompt",
      { threadId: "a", text: "hello" },
      expect.any(String),
    );
    expect(composer().value).toBe("");

    // Coming back to the thread shows the cleared field, not the sent text.
    await open("b");
    await open("a");
    expect(composer().value).toBe("");
  });

  const send = async (text: string) => {
    await act(async () => {
      fireEvent.change(composer(), { target: { value: text } });
    });
    await act(async () => {
      fireEvent.keyDown(composer(), { key: "Enter" });
    });
  };
  const commandIdOf = (call: unknown[]) => call[2] as string;

  it("puts a message that failed back in the composer, and retries it under the same id", async () => {
    let refuse = true;
    command.mockImplementation(async (name: string) => {
      if (name === "prompt" && refuse) throw new Error("workspace is not ready");
      return {} as any;
    });
    await boot();
    await open("a");

    await send("hello");

    await waitFor(() => expect(composer().value).toBe("hello"));
    expect(screen.getByRole("alert").textContent).toContain("Not sent: workspace is not ready.");
    expect(toast.error).not.toHaveBeenCalled();

    refuse = false;
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    });

    const [first, retry] = promptsSent();
    expect(commandIdOf(retry)).toBe(commandIdOf(first));
    expect(composer().value).toBe("");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("sends an edited message as a new one", async () => {
    let refuse = true;
    command.mockImplementation(async (name: string) => {
      if (name === "prompt" && refuse) throw new Error("connection lost");
      return {} as any;
    });
    await boot();
    await open("a");

    await send("hello");
    await waitFor(() => expect(composer().value).toBe("hello"));

    refuse = false;
    await send("hello again");

    const [first, edited] = promptsSent();
    expect(commandIdOf(edited)).not.toBe(commandIdOf(first));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("puts a failed message in front of what was typed while it was on its way", async () => {
    let fail: (e: Error) => void = () => {};
    command.mockImplementation((name: string) =>
      name === "prompt" ? new Promise((_, reject) => (fail = reject)) : Promise.resolve({}),
    );
    await boot();
    await open("a");

    await send("first");
    await act(async () => {
      fireEvent.change(composer(), { target: { value: "second" } });
    });
    await act(async () => fail(new Error("thread is closed")));

    expect(composer().value).toBe("first\n\nsecond");
  });

  it("retries the failed message alone, leaving what was typed after it", async () => {
    let fail: (e: Error) => void = () => {};
    command.mockImplementation((name: string) => {
      if (name !== "prompt" || promptsSent().length > 1) return Promise.resolve({});
      return new Promise((_, reject) => (fail = reject));
    });
    await boot();
    await open("a");

    await send("first");
    await act(async () => {
      fireEvent.change(composer(), { target: { value: "second" } });
    });
    await act(async () => fail(new Error("connection lost")));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    });

    const [first, retry] = promptsSent();
    expect(retry[1]).toMatchObject({ text: "first" });
    expect(commandIdOf(retry)).toBe(commandIdOf(first));
    expect(composer().value).toBe("second");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["a line", "first\nsecond"],
    ["a blank line", "first\n\nsecond"],
  ])("retries the failed message alone when what follows it is %s down", async (_, typed) => {
    let fail: (e: Error) => void = () => {};
    command.mockImplementation((name: string) => {
      if (name !== "prompt" || promptsSent().length > 1) return Promise.resolve({});
      return new Promise((_, reject) => (fail = reject));
    });
    await boot();
    await open("a");

    await send("first");
    await act(async () => fail(new Error("connection lost")));
    await act(async () => {
      fireEvent.change(composer(), { target: { value: typed } });
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    });

    expect(promptsSent()[1][1]).toMatchObject({ text: "first" });
    expect(composer().value).toBe("second");
  });

  it("says the server never answered when the connection was found dead", async () => {
    command.mockImplementation(async (name: string, _args: unknown, id?: string) => {
      if (name === "prompt") throw new CommandTimeout(name, id!);
      return {} as any;
    });
    await boot();
    await open("a");

    await send("hello");

    await waitFor(() => expect(composer().value).toBe("hello"));
    expect(screen.getByRole("alert").textContent).toContain("Not sent: no answer from the server.");
  });

  it("still retries under the same id after the note is dismissed", async () => {
    command.mockImplementation(async (name: string) => {
      if (name === "prompt" && promptsSent().length === 1) throw new Error("connection lost");
      return {} as any;
    });
    await boot();
    await open("a");

    await send("hello");
    await waitFor(() => expect(composer().value).toBe("hello"));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(composer().value).toBe("hello");

    await send("hello");
    const [first, again] = promptsSent();
    expect(commandIdOf(again)).toBe(commandIdOf(first));
  });

  it("keeps every thread's draft across a reload", async () => {
    await boot();
    await open("a");
    await act(async () => {
      fireEvent.change(composer(), { target: { value: "draft for a" } });
    });
    await open("b");
    await act(async () => {
      fireEvent.change(composer(), { target: { value: "draft for b" } });
    });
    // What a phone does to a backgrounded tab before discarding it.
    await act(async () => {
      window.dispatchEvent(new Event("pagehide"));
    });
    cleanup();

    await boot();
    await open("a");
    expect(composer().value).toBe("draft for a");
    await open("b");
    expect(composer().value).toBe("draft for b");
  });

  it("sends again, under its id, a message the page died waiting on", async () => {
    command.mockImplementation((name: string) =>
      name === "prompt" ? new Promise(() => {}) : Promise.resolve({}),
    );
    await boot();
    await open("a");
    await send("hello");
    cleanup();

    command.mockImplementation(async () => ({}) as any);
    await boot();
    await open("a");

    const [first, again] = promptsSent();
    expect(again[1]).toMatchObject({ threadId: "a", text: "hello" });
    expect(commandIdOf(again)).toBe(commandIdOf(first));
    expect(composer().value).toBe("");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("sends it again on the connection that stays, when the page mounts twice", async () => {
    command.mockImplementation((name: string) =>
      name === "prompt" ? new Promise(() => {}) : Promise.resolve({}),
    );
    await boot();
    await open("a");
    await send("hello");
    cleanup();

    // Dev mounts twice: the first connection is closed before it is up, and
    // what went on it times out.
    const sent: { client: { closed: boolean }; reject: (e: Error) => void }[] = [];
    command.mockImplementation(function (this: { closed: boolean }, name: string) {
      if (name !== "prompt") return Promise.resolve({});
      return new Promise((_, reject) => sent.push({ client: this, reject }));
    });
    await boot("desktop", { strict: true });
    await act(async () => {
      for (const s of sent) if (s.client.closed) s.reject(new Error("connection lost"));
    });
    await open("a");

    const [first, ...again] = promptsSent();
    expect(sent.filter((s) => !s.client.closed)).toHaveLength(1);
    expect(again.map(commandIdOf)).toContain(commandIdOf(first));
    expect(composer().value).toBe("");
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("brings back a message whose resend after a reload fails, in front of what was typed", async () => {
    command.mockImplementation((name: string) =>
      name === "prompt" ? new Promise(() => {}) : Promise.resolve({}),
    );
    await boot();
    await open("a");
    await send("hello");
    await act(async () => {
      fireEvent.change(composer(), { target: { value: "later" } });
    });
    await open("b");
    await act(async () => {
      window.dispatchEvent(new Event("pagehide"));
    });
    cleanup();

    command.mockImplementation(async (name: string) => {
      if (name === "prompt") throw new Error("workspace is not ready");
      return {} as any;
    });
    await boot();
    await open("a");
    expect(composer().value).toBe("hello\n\nlater");
    expect(screen.getByRole("alert").textContent).toContain("workspace is not ready");

    // Back in the draft for good: a second reload does not lose it, and
    // Retry is still the same message.
    cleanup();
    command.mockImplementation(async () => ({}) as any);
    await boot();
    await open("a");
    expect(composer().value).toBe("hello\n\nlater");
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    });
    const prompts = promptsSent();
    expect(commandIdOf(prompts.at(-1)!)).toBe(commandIdOf(prompts[0]));
    expect(composer().value).toBe("later");
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

    expect(promptsSent()).toEqual([]);
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
    expect(promptsSent()).toEqual([]);
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
    expect(promptsSent()).toEqual([]);
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
    expect(promptsSent()).toEqual([]);
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
    expect(promptsSent()).toEqual([]);
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
    expect(promptsSent()).toEqual([]);
  });

  it("dismisses completion with Escape", async () => {
    useCatalogue();
    await boot("phone");
    await open("a");

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "$al", selectionStart: 3 } });
    const menu = await screen.findByRole("listbox");
    // Scoped to the menu: an empty transcript is also offering this command
    // as a recent, so the bare text is no longer unique to the completion.
    expect(within(menu).getByText("Run alpha workflow")).toBeTruthy();

    fireEvent.keyDown(composer(), { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(composer().value).toBe("$al");
  });

  it("keeps completion shut after a tap outside, until the token changes", async () => {
    useCatalogue();
    await boot("phone");
    await open("a");

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "$al", selectionStart: 3 } });
    await screen.findByRole("listbox");

    // Radix arms its outside-press listener a tick after the menu opens, and
    // holds a primary press until the click that completes it.
    await act(() => new Promise((resolve) => setTimeout(resolve, 0)));
    const tap = (target: Element) => {
      fireEvent.pointerDown(target, { button: 0, ctrlKey: false });
      fireEvent.click(target);
    };

    // A tap in the textarea itself is moving the cursor, not closing the menu.
    tap(composer());
    expect(screen.getByRole("listbox")).toBeTruthy();

    tap(document.body);
    fireEvent.blur(composer());
    fireEvent.focus(composer());
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());

    fireEvent.change(composer(), { target: { value: "$alp", selectionStart: 4 } });
    expect(await screen.findByRole("listbox")).toBeTruthy();
  });

  it("keeps a new thread's draft while the list has not caught up with it", async () => {
    await boot();

    // Create a thread: it is attached, and can be typed into, before the
    // broadcast listing it arrives.
    await act(async () => {
      // Shift-click: straight to a draft, past the "New thread in…" step.
      fireEvent.click(screen.getAllByRole("button", { name: /New thread/ })[0], { shiftKey: true });
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
      // Shift-click: straight to a draft, past the "New thread in…" step.
      fireEvent.click(screen.getAllByRole("button", { name: /New thread/ })[0], { shiftKey: true });
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
      JSON.stringify({ build: "dev", state: state(id, "default"), position: { top: 120, atBottom: false } }),
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
      JSON.stringify({ build: "other", state: state("a", "default"), position: { top: 0, atBottom: true } }),
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
      JSON.stringify({ build: "dev", state: state("a", "default"), position: { top: 300, atBottom: false } }),
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
    expect(promptsSent()).toEqual([]);
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

// The Skills page from the sidebar: whose skills it lists follows what is open.
describe("the Skills page", () => {
  const listed = {
    name: "alpha",
    description: "Run alpha workflow",
    dir: "/lib/alpha",
    scope: "user",
    editable: true,
    mode: "on",
  };

  const boot = async () => {
    viewport("desktop");
    command.mockImplementation(async (name: string) => {
      if (name === "list_composer_items") return { items: [] };
      if (name === "list_skills") return { skills: [listed], claudeSync: true, codexBundled: true };
      if (name === "read_skill") return { ...listed, content: "# Alpha\n", files: [] };
      return {} as any;
    });
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a")]);
    });
  };

  const attachThread = () =>
    act(async () => {
      fireEvent.click(screen.getByText("Thread a"));
      events.onState("a", state("a", "default"));
    });

  const openPage = async () => {
    fireEvent.click(screen.getByRole("button", { name: "Skills, MCP and sign-ins" }));
    return screen.findByRole("button", { name: /alpha/ });
  };

  const listArgs = () => command.mock.calls.filter(([name]) => name === "list_skills").map(([, args]) => args);

  it("lists personal skills only when no project is known", async () => {
    await boot();
    fireEvent.click(await openPage());
    await screen.findByRole("button", { name: "Back to skills" });

    expect(listArgs()).toEqual([{}]);
  });

  it("lists the last project's skills when there is no thread open", async () => {
    localStorage.setItem("omniplex.lastProject.v1", "p1");
    await boot();
    await openPage();
    expect(listArgs()).toEqual([{ projectId: "p1" }]);
  });

  it("lists what the open thread sees and closes back to it", async () => {
    await boot();
    await attachThread();
    await openPage();
    expect(listArgs()).toEqual([{ threadId: "a" }]);

    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.getByLabelText("Message")).toBeTruthy();
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
      // Shift-click: straight to a draft, past the "New thread in…" step.
      fireEvent.click(screen.getAllByRole("button", { name: /New thread/ })[0], { shiftKey: true });
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
      expect(promptsSent()).toEqual([
        ["prompt", { threadId: "fresh", text: "read this", files: [{ artefactId: "art-1" }] }, expect.any(String)],
      ]),
    );
  });

  it("sends the message once, with nothing left in the new thread's composer to send again", async () => {
    let finish!: (v: unknown) => void;
    uploadStaged.mockImplementation(() => new Promise((resolve) => (finish = resolve)));
    await start();
    await pick(report);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });
    await act(async () => events.onState("fresh", state("fresh", "default")));

    expect(screen.getByText(/Sending once the uploads finish/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Remove report.txt" })).toBeNull();
    await act(async () => {
      fireEvent.keyDown(screen.getByLabelText("Message"), { key: "Enter" });
    });
    await act(async () => finish({ kind: "file", status: "ready", artefactId: "art-1", progress: 1 }));

    await waitFor(() => expect(promptsSent()).toHaveLength(1));
  });

  it("brings the message back in the new thread when a file fails to upload", async () => {
    uploadStaged.mockRejectedValue(new Error("disk full"));
    await start();
    await pick(report);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Send" }));
    });
    await act(async () => events.onState("fresh", state("fresh", "default")));

    await waitFor(() =>
      expect((screen.getByLabelText("Message") as HTMLTextAreaElement).value).toBe("read this"),
    );
    expect(promptsSent()).toEqual([]);
    expect(screen.getByRole("alert").textContent).toContain("Not sent: report.txt did not upload.");
    expect(screen.getByRole("button", { name: "Retry report.txt" })).toBeTruthy();
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
    expect(promptsSent()).toEqual([]);
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
    expect(promptsSent()).toEqual([]);
  });
});

describe("sending while files upload", () => {
  const boot = async () => {
    viewport("desktop");
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
  const pick = (file: File) =>
    act(async () => {
      fireEvent.change(document.querySelector('input[type="file"]')!, {
        target: { files: [file] },
      });
    });
  const send = async (text: string) => {
    await act(async () => {
      fireEvent.change(composer(), { target: { value: text } });
    });
    await act(async () => {
      fireEvent.keyDown(composer(), { key: "Enter" });
    });
  };
  const report = new File(["numbers"], "report.txt", { type: "text/plain" });
  const uploaded = (artefactId: string) => ({ kind: "file", status: "ready", artefactId, progress: 1 });

  // Each upload waits for the test to say how it ends.
  let uploads: { resolve: (v: unknown) => void; reject: (e: unknown) => void }[];
  beforeEach(() => {
    uploads = [];
    uploadStaged.mockReset();
    uploadStaged.mockImplementation(
      () => new Promise((resolve, reject) => uploads.push({ resolve, reject })),
    );
    command.mockReset();
    command.mockImplementation(async () => ({}) as any);
  });

  it("empties the composer at once and sends the message once its file is up", async () => {
    await boot();
    await pick(report);
    await send("read this");

    expect(composer().value).toBe("");
    expect(screen.queryByRole("button", { name: "Remove report.txt" })).toBeNull();
    expect(screen.getByText(/Sending once the uploads finish/)).toBeTruthy();
    expect(promptsSent()).toEqual([]);

    await act(async () => uploads[0].resolve(uploaded("art-1")));

    await waitFor(() =>
      expect(promptsSent()).toEqual([
        ["prompt", { threadId: "a", text: "read this", files: [{ artefactId: "art-1" }] }, expect.any(String)],
      ]),
    );
    expect(screen.queryByText(/Sending once the uploads finish/)).toBeNull();
  });

  it("brings the message back with its file when the upload fails, and sends it once the file is retried", async () => {
    await boot();
    await pick(report);
    await send("read this");
    await act(async () => uploads[0].reject(new Error("disk full")));

    await waitFor(() => expect(composer().value).toBe("read this"));
    expect(promptsSent()).toEqual([]);
    const note = screen.getByRole("alert");
    expect(note.textContent).toContain("Not sent: report.txt did not upload.");
    expect(screen.getByText("disk full")).toBeTruthy();
    // Retrying the message as it stands would send it without the file.
    expect(within(note).queryByRole("button", { name: "Retry" })).toBeNull();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Retry report.txt" }));
    });
    expect(uploadStaged).toHaveBeenCalledTimes(2);
    expect(uploadStaged).toHaveBeenLastCalledWith("a", report, expect.anything());
    await act(async () => uploads[1].resolve(uploaded("art-2")));
    expect(screen.queryByRole("button", { name: "Retry report.txt" })).toBeNull();

    await act(async () => {
      fireEvent.keyDown(composer(), { key: "Enter" });
    });
    expect(promptsSent()).toEqual([
      ["prompt", { threadId: "a", text: "read this", files: [{ artefactId: "art-2" }] }, expect.any(String)],
    ]);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("brings the message back when its files turn out to be over the limit once up", async () => {
    await boot();
    await pick(report);
    await send("read this");
    await act(async () =>
      uploads[0].resolve({ kind: "file", status: "ready", id: "att-1", size: 20_000_001 }),
    );

    await waitFor(() => expect(composer().value).toBe("read this"));
    expect(promptsSent()).toEqual([]);
    expect(screen.getByRole("alert").textContent).toContain("more than 20 MB");
  });

  it("keeps a file that failed in the composer when a message goes without it", async () => {
    await boot();
    await pick(report);
    await act(async () => uploads[0].reject(new Error("disk full")));

    await send("something else");

    expect(promptsSent()).toEqual([
      ["prompt", { threadId: "a", text: "something else" }, expect.any(String)],
    ]);
    expect(screen.getByRole("button", { name: "Retry report.txt" })).toBeTruthy();
  });

  it("marks a stalled upload failed, for a retry, rather than dropping it", async () => {
    await boot();
    await pick(report);
    const stalled = Object.assign(new Error("The upload stopped making progress."), {
      name: "UploadStalled",
    });
    await act(async () => uploads[0].reject(stalled));

    expect(screen.getByRole("button", { name: "Retry report.txt" })).toBeTruthy();
    expect(screen.getByText("The upload stopped making progress.")).toBeTruthy();
  });
});

describe("send and start a new thread", () => {
  const ready = { state: "ready" } as const;
  const claude = {
    ...harness,
    instances: [
      {
        ...harness.instances[0],
        models: [
          { id: "claude-basic", label: "Basic", default: true },
          { id: "claude-advanced", label: "Advanced" },
        ],
        availability: ready,
      },
    ],
  };
  const boot = async (kind: "phone" | "desktop" = "desktop", where = { project, thread: thread("a") }) => {
    viewport(kind);
    render(<App />);
    await act(async () => {
      events.onProjects([where.project]);
      events.onHarnesses([claude]);
      events.onThreads([where.thread]);
    });
    await act(async () => {
      fireEvent.click(screen.getByText("Thread a"));
      events.onState("a", { ...state("a", "default"), model: "claude-advanced" });
    });
  };
  const composer = () => screen.getByLabelText("Message") as HTMLTextAreaElement;
  const type = (text: string) =>
    act(async () => {
      fireEvent.change(composer(), { target: { value: text } });
    });
  // The chord is the composer's only while it has the cursor.
  const sendAndNew = () =>
    act(async () => {
      composer().focus();
      fireEvent.keyDown(composer(), { key: "Enter", ctrlKey: true, shiftKey: true });
    });
  const picker = () => screen.getByRole("combobox", { name: "Harness and model" });

  let uploads: { resolve: (v: unknown) => void }[];
  beforeEach(() => {
    uploads = [];
    uploadStaged.mockReset();
    uploadStaged.mockImplementation(() => new Promise((resolve) => uploads.push({ resolve })));
    command.mockReset();
    command.mockImplementation(async () => ({}) as any);
  });

  it("sends to the thread, then opens a draft on its model with the cursor in it", async () => {
    await boot();
    await type("first task");
    await sendAndNew();

    expect(promptsSent().map(([, args]) => args)).toEqual([{ threadId: "a", text: "first task" }]);
    expect(detach).toHaveBeenCalled();
    expect(window.location.pathname).toBe("/");
    expect(composer().value).toBe("");
    expect(picker().textContent).toContain("Advanced");
    await waitFor(() => expect(document.activeElement).toBe(composer()));
  });

  it("opens the draft on the thread's folder, not the one the project last used", async () => {
    localStorage.setItem(
      "omniplex.threadChoices.v1",
      JSON.stringify({ p1: { harness: "claude", byHarness: {}, folderId: "f1" } }),
    );
    const notes = { ...project.folders[0], id: "f2", path: "/tmp/notes", git: false };
    await boot("desktop", {
      project: { ...project, folders: [project.folders[0], notes] },
      thread: { ...thread("a"), folderId: "f2" },
    });
    await type("first task");
    await sendAndNew();

    expect(screen.getByRole("button", { name: /^Scope:/ }).textContent).toBe("notes");
  });

  it("leaves the keyboard down on a phone", async () => {
    await boot("phone");
    await type("first task");
    fireEvent.pointerDown(screen.getByRole("button", { name: "More send options" }), {
      button: 0,
      ctrlKey: false,
    });
    fireEvent.click(await screen.findByRole("menuitem", { name: /^Send and start a new thread/ }));

    expect(promptsSent()).toHaveLength(1);
    await waitFor(() => expect(picker().textContent).toContain("Advanced"));
    expect(document.activeElement).not.toBe(composer());
  });

  it("puts a message that failed after leaving back in its thread", async () => {
    command.mockImplementation(async (name: string) => {
      if (name === "prompt") throw new Error("thread is busy");
      return {} as any;
    });
    await boot();
    await type("first task");
    await sendAndNew();
    expect(detach).toHaveBeenCalled();
    await act(async () => {
      fireEvent.click(screen.getByText("Thread a"));
      events.onState("a", state("a", "default"));
    });

    expect(composer().value).toBe("first task");
    expect(screen.getByText(/thread is busy/)).toBeTruthy();
  });

  it("still sends a message waiting on its file once the file is up", async () => {
    await boot();
    await act(async () => {
      fireEvent.change(document.querySelector('input[type="file"]')!, {
        target: { files: [new File(["numbers"], "report.txt", { type: "text/plain" })] },
      });
    });
    await type("read this");
    await sendAndNew();
    expect(promptsSent()).toHaveLength(0);

    await act(async () => {
      uploads[0]!.resolve({ kind: "file", status: "ready", artefactId: "art-1", progress: 1 });
    });
    expect(promptsSent().map(([, args]) => args)).toEqual([
      expect.objectContaining({ threadId: "a", text: "read this" }),
    ]);
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

  it("opens adding an account for a harness that has none", async () => {
    const noAccount = {
      ...setupReport,
      checks: [
        setupReport.checks[0],
        {
          id: "claude",
          name: "Claude Code",
          kind: "harness",
          availability: {
            state: "unavailable",
            reason: "No Claude Code account yet.",
            remedy: [{ text: "Add account", action: "add_account" }],
          },
        },
      ],
    };
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(noAccount))));
    render(<App />);
    await act(async () => events.onHarnesses([{ ...harness, instances: [] }]));
    const card = await screen.findByRole("article", { name: "Claude Code" });

    fireEvent.click(within(card).getByRole("button"));

    expect(await screen.findByText("New Claude Code account")).toBeTruthy();
  });
});

describe("a card the agent raised", () => {
  const card = {
    requestId: "c1",
    prompt: "Add the MCP server linear to this project",
    schema: {},
    card: { kind: "add_mcp_server", projectId: "p1", scope: "project", server: { name: "linear", url: "https://x" } },
  };
  const question = {
    requestId: "q1",
    prompt: "Pick one",
    schema: { type: "object", properties: { q0: { type: "string", title: "Which cache?" } } },
  };

  async function open(pendingElicitations: unknown[]) {
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a")]);
    });
    await act(async () => events.onState("a", { ...state("a", "default"), pendingElicitations }));
  }

  it("shows beside an ordinary question, and answers through resolve_card", async () => {
    await open([card, question]);

    const band = await screen.findByRole("form", { name: card.prompt });
    expect(await screen.findByText("Which cache?")).toBeTruthy();

    // Folded while the question holds the room; opened, it answers.
    fireEvent.click(within(band).getByRole("button", { expanded: false }));
    const resolves = () => command.mock.calls.filter(([name]) => name === "resolve_card");
    fireEvent.click(within(band).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(resolves()).toEqual([["resolve_card", { threadId: "a", requestId: "c1", action: "accept" }]]));
  });

  it("opens on its own when nothing else is waiting", async () => {
    await open([card]);
    const band = await screen.findByRole("form", { name: card.prompt });
    expect(within(band).getByRole("button", { name: "Save" })).toBeTruthy();
  });
});

describe("keyboard shortcuts and the palette", () => {
  // Off a Mac, mod is Ctrl; jsdom says nothing about its platform, so the
  // tests say it.
  const key = (key: string, mods: Partial<KeyboardEventInit> = {}, target: Element = document.body) =>
    fireEvent.keyDown(target, { key, ...mods });
  const palette = () => screen.queryByRole("dialog", { name: "Command palette" });
  const search = () => within(palette()!).getByRole("combobox");
  // The palette loads on first use, so it shows up a tick after the key.
  const opened = () => screen.findByRole("dialog", { name: "Command palette" });
  const chip = (label: string) => screen.getByRole("button", { name: new RegExp(`^${label}:`) });
  const composer = () => screen.getByLabelText("Message") as HTMLTextAreaElement;
  const attached = () => attach.mock.calls.map(([id]) => id);

  const web = { ...project.folders[0], id: "f2", path: "/tmp/web" };
  const twoFolders = { ...project, folders: [project.folders[0], web] };
  const side = { path: "/tmp/web/.worktrees/side", branch: "issue/1-side" };

  beforeEach(() => {
    Object.defineProperty(navigator, "platform", { value: "Linux x86_64", configurable: true });
    command.mockReset();
    command.mockImplementation(async (name: string) =>
      name === "list_workspaces" ? { workspaces: [{ path: "/tmp/web", isRoot: true }, side] } : ({} as any),
    );
  });
  afterEach(() => {
    delete (navigator as { platform?: string }).platform;
    delete window.omniplexDesktop;
    window.history.replaceState(null, "", "/");
  });

  const boot = async (threads: ThreadMeta[] = [thread("a"), thread("b")], projects = [project]) => {
    render(<App />);
    await act(async () => {
      events.onProjects(projects);
      events.onHarnesses([harness]);
      events.onThreads(threads);
    });
  };
  const openThread = async (id: string, phase = "idle") => {
    await act(async () => {
      fireEvent.click(screen.getByText(`Thread ${id}`));
      events.onState(id, { ...state(id, "default"), phase });
    });
  };

  it("opens the palette on Ctrl+K and puts it away on a second", async () => {
    await boot();
    key("k", { ctrlKey: true });
    await opened();
    expect(search().getAttribute("placeholder")).toBe("Search threads and actions…");
    key("k", { ctrlKey: true }, search());
    await waitFor(() => expect(palette()).toBeNull());
  });

  it("starts a thread in a project, folder and existing worktree from the keyboard alone", async () => {
    await boot([thread("a")], [twoFolders]);
    // Ctrl+N is the browser's, so a tab gets the new-thread step on Ctrl+Shift+O.
    await act(async () => key("o", { ctrlKey: true, shiftKey: true }));
    await opened();
    expect(search().getAttribute("placeholder")).toBe("Which project?");

    fireEvent.keyDown(search(), { key: "Enter" });
    expect(search().getAttribute("placeholder")).toBe("Which folder?");
    fireEvent.change(search(), { target: { value: "web" } });
    fireEvent.keyDown(search(), { key: "Enter" });
    expect(search().getAttribute("placeholder")).toBe("Which checkout?");
    await within(palette()!).findByText("issue/1-side");
    fireEvent.change(search(), { target: { value: "side" } });
    await act(async () => {
      fireEvent.keyDown(search(), { key: "Enter" });
    });

    await waitFor(() => expect(palette()).toBeNull());
    expect(chip("Project").textContent).toBe("repo");
    expect(chip("Scope").textContent).toBe("web");
    await waitFor(() => expect(chip("Git").textContent).toBe("Worktree: issue/1-side"));
    await waitFor(() => expect(document.activeElement).toBe(composer()));
  });

  it("starts the new-thread step over when asked for it with the palette already open", async () => {
    await boot();
    key("k", { ctrlKey: true });
    await opened();
    await act(async () => key("o", { ctrlKey: true, shiftKey: true }, search()));
    expect(search().getAttribute("placeholder")).toBe("Which project?");
  });

  it("puts the palette away for a new thread here, so the draft's composer gets the keys", async () => {
    await boot();
    key("k", { ctrlKey: true });
    await opened();
    await act(async () => key("n", { ctrlKey: true, altKey: true }, search()));
    await waitFor(() => expect(palette()).toBeNull());
    expect(chip("Project").textContent).toBe("repo");
    await waitFor(() => expect(document.activeElement).toBe(composer()));
  });

  it("leaves the theme sample for a new thread", async () => {
    window.history.replaceState(null, "", "/#themes");
    await boot();
    await act(async () => key("n", { ctrlKey: true, altKey: true }));
    expect(chip("Project").textContent).toBe("repo");
    expect(window.location.hash).toBe("");
  });

  it("offers the composer's actions only where the composer is", async () => {
    await boot();
    await openThread("a");
    const offers = (label: string) => !!within(palette()!).queryByText(label);
    key("k", { ctrlKey: true });
    await opened();
    expect(offers("Model picker")).toBe(true);

    fireEvent.change(search(), { target: { value: "Skills" } });
    await act(async () => {
      fireEvent.keyDown(search(), { key: "Enter" });
    });
    await waitFor(() => expect(palette()).toBeNull());
    key("k", { ctrlKey: true });
    expect(offers("Model picker")).toBe(false);
    expect(offers("Reasoning effort")).toBe(false);
  });

  it("walks threads in the sidebar's order, past a folded group, wrapping at the ends", async () => {
    const other = { ...project, id: "p2", name: "other", createdAt: 1 };
    const repo = { ...project, createdAt: 2 };
    localStorage.setItem("omniplex.projectCollapsed", JSON.stringify(["p2"]));
    await boot(
      [
        { ...thread("d"), projectId: "p2", pinned: true, position: 0 },
        { ...thread("a"), position: 1 },
        { ...thread("b"), position: 2 },
        { ...thread("c"), projectId: "p2", position: 3 },
      ],
      [repo, other],
    );
    const next = () => act(async () => key("}", { code: "BracketRight", ctrlKey: true, shiftKey: true }));
    const prev = () => act(async () => key("{", { code: "BracketLeft", ctrlKey: true, shiftKey: true }));

    await next(); // nothing open: the first row
    await next();
    await next();
    await next(); // c is folded away, so b wraps to the top
    await prev();
    expect(attached()).toEqual(["d", "a", "b", "d", "b"]);
  });

  it("finds the next thread that needs an answer, folded or not", async () => {
    localStorage.setItem("omniplex.projectCollapsed", JSON.stringify(["p2"]));
    const other = { ...project, id: "p2", name: "other", createdAt: 1 };
    await boot(
      [
        { ...thread("a"), position: 0 },
        { ...thread("b"), position: 1 },
        { ...thread("c"), projectId: "p2", position: 2, attention: "needs_answer" },
      ],
      [{ ...project, createdAt: 2 }, other],
    );
    await act(async () => key("ArrowDown", { altKey: true, shiftKey: true }));
    expect(attached()).toEqual(["c"]);
  });

  it("jumps to the Nth thread with Ctrl+digit only in the desktop app", async () => {
    await boot([
      { ...thread("a"), position: 0 },
      { ...thread("b"), position: 1 },
    ]);
    const jump = () => key("2", { code: "Digit2", ctrlKey: true });
    // A browser keeps Ctrl+1–9 for its tabs.
    expect(jump()).toBe(true);
    expect(attach).not.toHaveBeenCalled();

    window.omniplexDesktop = { platform: "linux" };
    await act(async () => {
      jump();
    });
    expect(attached()).toEqual(["b"]);
  });

  it("closes the side panel on Ctrl+W in the desktop app, and leaves Ctrl+W to a browser", async () => {
    await boot();
    await openThread("a");
    fireEvent.click(screen.getByRole("button", { name: "Show the panel" }));
    expect(screen.getByRole("button", { name: "Hide the panel" })).toBeTruthy();

    expect(key("w", { ctrlKey: true })).toBe(true);
    expect(screen.getByRole("button", { name: "Hide the panel" })).toBeTruthy();

    window.omniplexDesktop = { platform: "linux" };
    await act(async () => {
      key("w", { ctrlKey: true });
    });
    expect(screen.getByRole("button", { name: "Show the panel" })).toBeTruthy();
    // Nothing left to close, and still the window's close is held back.
    expect(key("w", { ctrlKey: true })).toBe(false);
  });

  it("asks where from the sidebar's New thread, and skips the question on shift-click", async () => {
    await boot();
    const button = within(document.querySelector("aside") ?? document.body).getAllByRole("button", {
      name: /^New thread/,
    })[0];
    await act(async () => {
      fireEvent.click(button);
    });
    await opened();
    expect(search().getAttribute("placeholder")).toBe("Which project?");
    expect(screen.queryByRole("button", { name: /^Project:/ })).toBeNull();

    key("Escape", {}, search());
    await waitFor(() => expect(palette()).toBeNull());
    await act(async () => {
      fireEvent.click(button, { shiftKey: true });
    });
    expect(palette()).toBeNull();
    expect(chip("Project").textContent).toBe("repo");
  });

  it("stops a running turn on Escape in the composer, unless the composer's menu takes it", async () => {
    command.mockImplementation(async (name: string) =>
      name === "list_composer_items"
        ? {
            items: [
              {
                id: "skill:alpha",
                name: "alpha",
                description: "Run alpha",
                kind: "skill",
                trigger: "$",
                insertText: "$alpha",
                behavior: "prompt",
                origin: "project",
              },
            ],
          }
        : ({} as any),
    );
    await boot();
    await openThread("a", "turn");
    const cancels = () => command.mock.calls.filter(([name]) => name === "cancel");

    composer().focus();
    key("Escape", {}, composer());
    expect(cancels()).toEqual([["cancel", { threadId: "a" }]]);

    fireEvent.focus(composer());
    fireEvent.change(composer(), { target: { value: "$alp", selectionStart: 4 } });
    expect(await screen.findByRole("listbox")).toBeTruthy();
    key("Escape", {}, composer());
    await waitFor(() => expect(screen.queryByRole("listbox")).toBeNull());
    expect(cancels()).toHaveLength(1);
  });
});

// A running subagent reports progress, and a turn its token counts, far more
// often than the conversation changes. Those frames are for what shows them:
// the spawn's card, the jobs strip, the context meter. The app around them and
// the transcript they sit in have nothing new to draw.
describe("a job or usage tick", () => {
  const running = (): any => ({
    ...state("a", "default"),
    phase: "turn",
    items: [
      { id: "u1", kind: "message", role: "user", text: "go", turnId: "t1", receivedAt: 1 },
      { id: "spawn", kind: "tool", toolKind: "agent", title: "scout", status: "in_progress", turnId: "t1", receivedAt: 2 },
    ],
    turns: [{ id: "t1", prompt: "go", done: false }],
    jobs: [{ id: "j1", toolCallId: "spawn", name: "scout", kind: "agent", status: "running", depth: 0, usage: {}, activity: "reading files" }],
    usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, cost: 0 },
  });

  it("redraws neither the app nor the transcript", async () => {
    localStorage.setItem("omniplex.lastThread", "a");
    render(<App />);
    await act(async () => {
      events.onProjects([project]);
      events.onHarnesses([harness]);
      events.onThreads([thread("a")]);
    });
    const s = running();
    await act(async () => events.onState("a", s));
    expect(await screen.findByText("reading files")).toBeTruthy();
    renders.app = 0;
    renders.transcript = 0;

    // What apply.ts makes of each: a new state with the seq bumped and only
    // the one field copied.
    const ticked = { ...s, seq: 2, jobs: [{ ...s.jobs[0], activity: "running tests" }] };
    await act(async () => events.onState("a", ticked));
    await act(async () => events.onState("a", { ...ticked, seq: 3, usage: { ...s.usage, output: 2 } }));

    expect(screen.getByText("running tests")).toBeTruthy();
    expect(renders).toEqual({ app: 0, transcript: 0 });
  });
});
