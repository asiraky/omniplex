// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Transcript } from "./Transcript";
import { render, viewport, wrap } from "~/test/harness";
import type { PullRequest } from "~/protocol";

const state = (text: string): any => ({
  sessionId: "a",
  seq: 1,
  cwd: "/tmp/repo",
  harness: "claude",
  model: "",
  mode: "default",
  effort: "",
  title: "Session a",
  phase: "idle",
  closed: false,
  workspace: { phase: "ready", projectId: "p1", projectRoot: "/tmp/repo" },
  items: [{ id: "m1", kind: "message", role: "agent", contentKind: "text", text, receivedAt: 1 }],
  turns: [{ id: "t1", status: "done" }],
  plan: [],
  usage: {},
  pendingPermissions: [],
  pendingElicitations: [],
});

function transcript(text: string) {
  return render(
    <Transcript
      state={state(text)}
      onRetryProvision={() => {}}
      onCleanup={() => {}}
      onForceDelete={() => {}}
      onContinue={() => {}}
      onOpenDiff={() => {}}
      onFinish={() => {}}
    />,
  );
}

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("copying an agent message", () => {
  it("copies the raw markdown, not the rendered text", () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });

    transcript("# Title\n\n**bold** and `code`");
    fireEvent.click(screen.getByLabelText("Copy message"));

    expect(writeText).toHaveBeenCalledWith("# Title\n\n**bold** and `code`");
  });

  // omniplex is routinely reached over plain http on a LAN address, which is not a
  // secure context: there is no `navigator.clipboard` there at all. The copy
  // has to happen anyway rather than dying silently under a thumb.
  it("still copies with no clipboard API — an http origin on a phone", () => {
    vi.stubGlobal("navigator", {});
    const exec = vi.fn().mockReturnValue(true);
    document.execCommand = exec;

    transcript("hello");
    fireEvent.click(screen.getByLabelText("Copy message"));

    expect(exec).toHaveBeenCalledWith("copy");
  });
});

describe("copying a user message", () => {
  it("copies the entire raw prompt from its footer", () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    const userState = state("");
    userState.items = [
      { id: "u1", kind: "message", role: "user", text: "A long **raw** prompt", receivedAt: 1 },
    ];

    render(view(userState));
    fireEvent.click(screen.getByLabelText("Copy message"));

    expect(writeText).toHaveBeenCalledWith("A long **raw** prompt");
  });
});

// A prompt sent into a session on screen is lifted to the top of the view so
// the answer streams into the space below it. Which prompts count as "just
// sent" is this component's rule — the hook is told what to hold, not when.
//
// jsdom lays nothing out, so the geometry is stated outright: a 600px
// transcript in a 400px window, with the prompt 500px into it.
const VIEW = 400;
const HEIGHT = 600;
const PROMPT_TOP = 500;
// The room the anchor has to reserve to lift a prompt that far up.
const RESERVE = `${VIEW - (HEIGHT - (PROMPT_TOP - 16))}px`;

const prompts = (ids: string[], sessionId = "a"): any => ({
  ...state(""),
  sessionId,
  items: ids.map((id) => ({ id, kind: "message", role: "user", text: id, receivedAt: 1 })),
});

function view(s: any) {
  return (
    <Transcript
      state={s}
      onFinish={() => {}}
      onRetryProvision={() => {}}
      onCleanup={() => {}}
      onForceDelete={() => {}}
      onContinue={() => {}}
      onOpenDiff={() => {}}
    />
  );
}

// What the transcript is asking its padding to add — "" when nothing is held.
function reserve(container: HTMLElement) {
  const content = container.querySelector<HTMLElement>(".overflow-y-auto > div");
  return content?.style.getPropertyValue("--anchor-reserve") ?? "";
}

// Give the transcript a body: a scroller that clamps its position the way a
// real one does, whose height includes the room the anchor reserves, and a
// prompt whose box moves with the scroll.
function measured(container: HTMLElement) {
  const el = container.querySelector<HTMLElement>(".overflow-y-auto")!;
  const height = () => HEIGHT + parseInt(reserve(container) || "0", 10);
  let top = 0;
  Object.defineProperty(el, "scrollHeight", { get: height, configurable: true });
  Object.defineProperty(el, "clientHeight", { value: VIEW, configurable: true });
  Object.defineProperty(el, "scrollTop", {
    get: () => top,
    set: (v: number) => {
      top = Math.max(0, Math.min(v, height() - VIEW));
    },
    configurable: true,
  });
  vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
    return { top: this.hasAttribute("data-msg-id") ? PROMPT_TOP - top : 0 } as DOMRect;
  });
  return el;
}

describe("lifting a just-sent prompt", () => {
  it("anchors a prompt that arrives while the session is on screen", () => {
    const { container, rerender } = render(view(prompts(["p1"])));
    const el = measured(container);

    rerender(wrap(view(prompts(["p1", "p2"]))));

    expect(reserve(container)).toBe(RESERVE);
    expect(el.scrollTop).toBe(PROMPT_TOP - 16);
  });

  it("anchors the first prompt of a session that had none", () => {
    const { container, rerender } = render(view(prompts([])));
    measured(container);

    rerender(wrap(view(prompts(["p1"]))));

    expect(reserve(container)).toBe(RESERVE);
  });

  it("leaves a session that was only just opened where it is", () => {
    // Switching sessions changes the newest prompt too — the whole transcript
    // arrives at once — and nobody asked for that view to move.
    const { container, rerender } = render(view(prompts(["p1"])));
    measured(container);

    rerender(wrap(view(prompts(["p9"], "b"))));

    expect(reserve(container)).toBe("");
  });
});

// A worktree session whose branch has landed is offered a way out of the
// transcript it is being read in.
function merged(pr: PullRequest | null, onFinish = () => {}) {
  return render(
    <Transcript
      state={state("done")}
      onRetryProvision={() => {}}
      onCleanup={() => {}}
      onForceDelete={() => {}}
      onContinue={() => {}}
      onOpenDiff={() => {}}
      pr={pr}
      onFinish={onFinish}
    />,
  );
}

const MERGED: PullRequest = {
  number: 75,
  state: "MERGED",
  merged: true,
  mergedAt: "2026-08-20T01:02:03Z",
};

describe("the merged-pull-request prompt", () => {
  it("offers to finish the session once the branch has landed", () => {
    merged(MERGED);
    expect(screen.getByRole("button", { name: /finish with this session/i })).toBeTruthy();
    expect(screen.getByText("PR #75 merged")).toBeTruthy();
  });

  it("says nothing while the pull request is still open", () => {
    merged({ number: 75, state: "OPEN", merged: false });
    expect(screen.queryByRole("button", { name: /finish with this session/i })).toBeNull();
  });

  it("says nothing when there is no pull request to speak of", () => {
    merged(null);
    expect(screen.queryByRole("button", { name: /finish with this session/i })).toBeNull();
  });

  it("opens the confirmation rather than deleting anything itself", () => {
    const onFinish = vi.fn();
    merged(MERGED, onFinish);
    fireEvent.click(screen.getByRole("button", { name: /finish with this session/i }));
    expect(onFinish).toHaveBeenCalledTimes(1);
  });

  it("still names the offer on a phone, where there is no hover to explain it", () => {
    viewport("phone");
    merged(MERGED);
    // The tooltip does not render on a coarse pointer, so the accessible name
    // is the whole explanation and has to carry it alone.
    expect(screen.getByRole("button", { name: /finish with this session/i })).toBeTruthy();
  });
});

// The provisioner's card is a receipt, not a task. It says "ready" and then
// leaves on its own, so the empty transcript it was sitting on top of is free
// for the affordance the user actually needs.
const empty = (over: any = {}): any => ({ ...state(""), items: [], ...over });

function provisioner(s: any) {
  return render(
    <Transcript
      state={s}
      onRetryProvision={() => {}}
      onCleanup={() => {}}
      onForceDelete={() => {}}
      onContinue={() => {}}
      onOpenDiff={() => {}}
      onFinish={() => {}}
    />,
  );
}

describe("the workspace card leaving on its own", () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => vi.useRealTimers());

  it("dismisses itself once the workspace is ready", async () => {
    provisioner(empty());
    expect(screen.getByText("Workspace ready")).toBeTruthy();

    // Twice: the first pass starts the collapse, the second lets it finish —
    // the unmount timer is only scheduled once the card is on its way out.
    await act(async () => {
      vi.advanceTimersByTime(3000);
    });
    await act(async () => {
      vi.advanceTimersByTime(1000);
    });

    expect(screen.queryByText("Workspace ready")).toBeNull();
  });

  it("keeps a failed workspace on screen — it is asking a question", async () => {
    provisioner(empty({ phase: "provision_failed", workspace: { phase: "provision_failed" } }));

    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    expect(screen.getByText("Workspace needs attention")).toBeTruthy();
  });

  it("comes back at full height when the workspace goes active mid-collapse", async () => {
    const { container, rerender } = provisioner(empty());
    await act(async () => {
      vi.advanceTimersByTime(3000);
    });

    // The collapse has started but not finished; cleanup begins.
    await act(async () => {
      rerender(
        wrap(
          <Transcript
            state={empty({ phase: "cleaning", workspace: { phase: "cleaning" } })}
            onRetryProvision={() => {}}
            onCleanup={() => {}}
            onForceDelete={() => {}}
            onContinue={() => {}}
            onOpenDiff={() => {}}
            onFinish={() => {}}
          />,
        ),
      );
    });

    expect(screen.getByText("Cleaning up workspace")).toBeTruthy();
    // Flat and transparent would be the same as gone, taking any failure
    // controls with it.
    const wrapper = container.querySelector<HTMLElement>(".transition-\\[max-height\\,opacity\\]")!;
    expect(wrapper.style.maxHeight).toBe("");
    expect(wrapper.style.opacity).toBe("");
  });

  it("stays while the reader has its output open", async () => {
    provisioner(empty());
    fireEvent.click(screen.getByText("Workspace ready"));

    await act(async () => {
      vi.advanceTimersByTime(5000);
    });

    expect(screen.getByText("Workspace ready")).toBeTruthy();
  });
});

// The first-run affordance: an empty transcript offers the skills this user
// reaches for, and hands the chosen one to the composer rather than running it.
const RECENTS: any[] = [
  { id: "s1", name: "work-issue", kind: "skill", trigger: "/", insertText: "/work-issue", behavior: "prompt" },
  { id: "s2", name: "review", kind: "skill", trigger: "/", insertText: "/review", behavior: "prompt" },
];

function withRecents(s: any, onPick = () => {}) {
  return render(
    <Transcript
      state={s}
      onRetryProvision={() => {}}
      onCleanup={() => {}}
      onForceDelete={() => {}}
      onContinue={() => {}}
      onOpenDiff={() => {}}
      onFinish={() => {}}
      recents={RECENTS}
      onPickRecent={onPick}
    />,
  );
}

describe("recent skills on an empty transcript", () => {
  it("offers them when there is nothing in the session yet", () => {
    withRecents(empty());
    expect(screen.getByText("/work-issue")).toBeTruthy();
  });

  it("hands the picked skill back rather than running it", () => {
    const onPick = vi.fn();
    withRecents(empty(), onPick);
    fireEvent.click(screen.getByText("/review"));
    expect(onPick).toHaveBeenCalledWith(expect.objectContaining({ insertText: "/review" }));
  });

  it("is gone the moment the transcript has anything in it", () => {
    withRecents(state("hello"));
    expect(screen.queryByText("/work-issue")).toBeNull();
  });

  it("stands aside while the workspace is still being provisioned", () => {
    withRecents(empty({ phase: "provisioning", workspace: { phase: "provisioning" } }));
    expect(screen.queryByText("/work-issue")).toBeNull();
  });
});

// The bug this guards against: a turn that failed for a reason of its own —
// an expired login, fixed with /login — was described as a server restart, in
// the card offering to continue it and in the pill above the continuation.
// Nothing had restarted, and the reader was sent looking for the wrong fault.
describe("a turn that ended in an error", () => {
  function renderState(extra: any) {
    return render(
      <Transcript
        state={{ ...state("working on it"), ...extra }}
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onOpenDiff={() => {}}
        onFinish={() => {}}
      />,
    );
  }

  // The card the reader is offered against the failed turn.
  function renderCard(error: string) {
    return renderState({
      items: [],
      turns: [{ id: "t1", prompt: "do the thing", done: true, stopReason: "error", error }],
    });
  }

  // The pill that stands in for the continuation prompt the server wrote.
  function renderContinuation(cause?: "restart" | "error") {
    return renderState({
      items: [
        { id: "m2", kind: "message", role: "user", text: "[omniplex] continue", turnId: "t2", receivedAt: 2 },
      ],
      turns: [
        { id: "t1", prompt: "do the thing", done: true, stopReason: "error", error: "boom" },
        { id: "t2", prompt: "[omniplex] continue", done: true, recovery: { resumeOf: "t1", attempt: 1, cause } },
      ],
    });
  }

  it("quotes the harness's own error instead of blaming a restart", () => {
    renderCard("Invalid API key · Please run /login");

    expect(screen.queryByText(/server restarted/i)).toBeNull();
    expect(screen.getByText(/Invalid API key/)).toBeTruthy();
    expect(screen.getByText(/This turn ended with an error/)).toBeTruthy();
  });

  it("still names the restart on a turn a resume closed", () => {
    renderCard("server restarted during turn");

    expect(screen.getByText(/The server restarted and this turn was interrupted/)).toBeTruthy();
  });

  it("says the turn ended early above a continuation that followed an error", () => {
    renderContinuation("error");

    expect(screen.getByText(/The turn ended early — the agent was asked/)).toBeTruthy();
  });

  it("says the server restarted above a continuation that followed one", () => {
    renderContinuation("restart");

    expect(screen.getByText(/Server restarted — the agent was asked/)).toBeTruthy();
  });
});

// Logs written before the cause was recorded still say what stopped the turn,
// one turn back: a continuation of a turn a resume closed was a restart, and a
// continuation of anything else was someone pressing the button.
describe("a continuation recorded before the cause was", () => {
  function renderLegacy(firstError: string) {
    return render(
      <Transcript
        state={{
          ...state("working on it"),
          items: [
            {
              id: "m2",
              kind: "message",
              role: "user",
              text: "[omniplex] continue",
              turnId: "t2",
              receivedAt: 2,
            },
          ],
          turns: [
            { id: "t1", prompt: "do the thing", done: true, stopReason: "error", error: firstError },
            {
              id: "t2",
              prompt: "[omniplex] continue",
              done: true,
              recovery: { resumeOf: "t1", attempt: 1 },
            },
          ],
        }}
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onOpenDiff={() => {}}
        onFinish={() => {}}
      />,
    );
  }

  it("reads a continuation of a restart-closed turn as a restart", () => {
    renderLegacy("server restarted during turn");

    expect(screen.getByText(/Server restarted — the agent was asked/)).toBeTruthy();
  });

  it("does not invent a restart behind a turn that failed on its own", () => {
    renderLegacy("Invalid API key · Please run /login");

    expect(screen.getByText(/The turn ended early — the agent was asked/)).toBeTruthy();
  });
});
