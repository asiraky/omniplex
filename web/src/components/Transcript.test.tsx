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
  queuedPrompts: [],
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
// prompt that sits a fixed way down that content.
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
  // Layout positions, not painted ones: the hook measures the anchor with
  // `offsetTop` so a prompt that is still fading in cannot be measured 3px
  // below where it will settle. So the prompt sits `PROMPT_TOP` down the
  // content no matter where the view is.
  vi.spyOn(HTMLElement.prototype, "offsetTop", "get").mockImplementation(function (this: HTMLElement) {
    return this.hasAttribute("data-msg-id") ? PROMPT_TOP : 0;
  });
  vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockReturnValue(null);
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

function element(s: any) {
  return (
    <Transcript
      state={s}
      onRetryProvision={() => {}}
      onCleanup={() => {}}
      onForceDelete={() => {}}
      onContinue={() => {}}
      onOpenDiff={() => {}}
      onFinish={() => {}}
    />
  );
}

// Mounts while the provisioner is still working, then lands on `s`: the card
// is a receipt for work the reader watched, so the tests that expect to see
// one have to have watched it.
function provisioner(s: any) {
  const r = render(element(empty({ phase: "provisioning", workspace: { phase: "provisioning" } })));
  act(() => r.rerender(wrap(element(s))));
  return r;
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

  it("never appears for a workspace that was ready before the transcript mounted", async () => {
    render(element(empty()));
    expect(screen.queryByText("Workspace ready")).toBeNull();
    await act(async () => {
      vi.advanceTimersByTime(5000);
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
      rerender(wrap(element(empty({ phase: "cleaning", workspace: { phase: "cleaning" } }))));
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

// The failure card is the only thing between a person and a wrong story about
// their work. A turn that died because Claude is not signed in used to be
// offered a "continue where it left off" button whose prompt — and whose badge
// once it ran — announced a server restart that never happened.
describe("a turn that failed", () => {
  const failed = (turn: Record<string, unknown>) => {
    const s = state("");
    s.items = [
      { id: "u1", kind: "message", role: "user", text: "go", turnId: "t1", receivedAt: 1 },
    ];
    s.turns = [{ id: "t1", prompt: "go", done: true, stopReason: "error", ...turn }];
    return s;
  };

  it("lets a signed-out user sign in from the failed session", () => {
    const onLogin = vi.fn();
    render(
      <Transcript
        state={failed({ failure: "auth", error: "claude needs you to sign in again: …" })}
        onFinish={() => {}}
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onLogin={onLogin}
        providerName="Claude Code"
        onOpenDiff={() => {}}
      />,
    );

    expect(screen.getByText(/Claude Code needs to sign in again/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(onLogin).toHaveBeenCalledOnce();
    expect(screen.queryByText(/Continue where it left off/)).toBeNull();
    expect(screen.queryByText(/restart/i)).toBeNull();
  });

  // Retry appears only once the provider reports ready again, and re-sending
  // the failed prompt is always the user's press — never a side effect of a
  // sign-in finishing.
  it("offers retry only when the provider is ready, and only on request", () => {
    const onRetryTurn = vi.fn();
    const authFailed = failed({ failure: "auth", error: "signed out" });
    const props = {
      onFinish: () => {},
      onRetryProvision: () => {},
      onCleanup: () => {},
      onForceDelete: () => {},
      onContinue: () => {},
      onOpenDiff: () => {},
      providerName: "Claude Code",
      onRetryTurn,
    };

    const { unmount } = render(
      <Transcript state={authFailed} {...props} providerReady={false} />,
    );
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
    unmount();

    render(<Transcript state={authFailed} {...props} providerReady />);
    expect(onRetryTurn).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /retry this prompt/i }));
    expect(onRetryTurn).toHaveBeenCalledOnce();
    expect(onRetryTurn.mock.calls[0][0]).toMatchObject({ id: "t1", prompt: "go" });
  });

  it("only says the server restarted when the server says so", () => {
    render(view(failed({ failure: "restart", error: "server restarted during turn" })));
    expect(screen.getByText(/The server restarted/)).toBeTruthy();

    render(view(failed({ error: "claude exited: ENOENT" })));
    expect(screen.getByText(/ended with an error/)).toBeTruthy();
    expect(screen.getByText(/claude exited: ENOENT/)).toBeTruthy();
  });

  it("does not call a human's continue a restart", () => {
    const s = failed({ error: "claude exited: ENOENT" });
    s.items.push({
      id: "u2",
      kind: "message",
      role: "user",
      text: "[omniplex] Your previous turn ended in an error…",
      turnId: "t2",
      receivedAt: 2,
    });
    s.turns.push({
      id: "t2",
      done: false,
      recovery: { resumeOf: "t1", attempt: 1, cause: "continue" },
    });
    render(view(s));

    expect(screen.getByText("Asked the agent to pick the work back up")).toBeTruthy();
    expect(screen.queryByText(/Server restarted/)).toBeNull();
  });

  // A usage limit is not a bug report. The card's job is the wait: when the
  // window reopens, and whether this session is going to carry on by itself.
  describe("because the provider is out of budget", () => {
    const limited = (extra: Record<string, unknown> = {}, resetAt = Date.now() + 2 * 3_600_000) => {
      const s = failed({
        failure: "usage_limit",
        error: "You've hit your session limit · resets 11:10am (Australia/Brisbane)",
        resetAt,
      });
      return { ...s, ...extra };
    };
    const armed = (over: Record<string, unknown> = {}) => ({
      id: "s1",
      kind: "resume",
      attempt: 1,
      resumeOf: "t1",
      revision: 0,
      prompt: "[omniplex] …",
      dueAt: Date.now() + 2 * 3_600_000,
      timeZone: "Australia/Brisbane",
      model: "",
      mode: "",
      effort: "",
      status: "pending",
      ...over,
    });

    it("says when the work will pick itself back up", () => {
      render(view(limited({ scheduledPrompts: [armed()] })));
      expect(screen.getByText(/Usage limit reached/)).toBeTruthy();
      expect(screen.getByText(/pick the work back up when it reopens/)).toBeTruthy();
      // The reset time is stated once. The provider's own copy of it, with its
      // upsell, is not repeated underneath.
      expect(screen.queryByText(/hit your session limit/)).toBeNull();
      // Not a story about a crash: nothing broke.
      expect(screen.queryByText(/ended with an error/)).toBeNull();
      expect(screen.queryByText(/restart/i)).toBeNull();
    });

    it("says so when nothing is armed, and offers the switch either way", () => {
      const onAutoResume = vi.fn();
      render(
        <Transcript
          state={limited() as never}
          onFinish={() => {}}
          onRetryProvision={() => {}}
          onCleanup={() => {}}
          onForceDelete={() => {}}
          onContinue={() => {}}
          onAutoResume={onAutoResume}
          onOpenDiff={() => {}}
        />,
      );
      // Nothing armed says itself through the switch: no extra line for it.
      expect(screen.queryByText(/pick the work back up/)).toBeNull();

      const toggle = screen.getByRole("switch", { name: /resume automatically/i });
      expect(toggle.getAttribute("data-state")).toBe("unchecked");
      fireEvent.click(toggle);
      expect(onAutoResume).toHaveBeenCalledWith(true);
    });

    it("shows the switch as on while a resume is armed, so turning it off is the override", () => {
      const onAutoResume = vi.fn();
      render(
        <Transcript
          state={limited({ scheduledPrompts: [armed()] }) as never}
          onFinish={() => {}}
          onRetryProvision={() => {}}
          onCleanup={() => {}}
          onForceDelete={() => {}}
          onContinue={() => {}}
          onAutoResume={onAutoResume}
          onOpenDiff={() => {}}
        />,
      );
      const toggle = screen.getByRole("switch", { name: /resume automatically/i });
      expect(toggle.getAttribute("data-state")).toBe("checked");
      fireEvent.click(toggle);
      expect(onAutoResume).toHaveBeenCalledWith(false);
    });

    // Continuing before the window reopens fails the same way, so the offer
    // only exists once the wait is actually over.
    it("offers nothing to press while the window is still shut", () => {
      render(view(limited({ scheduledPrompts: [armed()] })));
      expect(screen.queryByRole("button", { name: /continue now/i })).toBeNull();
    });

    it("lets someone go by hand once the window has reopened", () => {
      const onContinue = vi.fn();
      render(
        <Transcript
          state={limited({ scheduledPrompts: [armed()] }, Date.now() - 60_000) as never}
          onFinish={() => {}}
          onRetryProvision={() => {}}
          onCleanup={() => {}}
          onForceDelete={() => {}}
          onContinue={onContinue}
          onOpenDiff={() => {}}
        />,
      );
      fireEvent.click(screen.getByRole("button", { name: /continue now/i }));
      expect(onContinue).toHaveBeenCalledOnce();
    });

    it("says so when the resume it was counting on never landed", () => {
      render(
        view(
          limited(
            { scheduledPrompts: [armed({ status: "failed", error: "session is closed" })] },
            Date.now() - 60_000,
          ),
        ),
      );
      // The strip above the composer no longer carries resumes, so this card
      // is the only place a broken one can be seen.
      expect(screen.getByText(/Picking the work back up did not work/)).toBeTruthy();
      expect(screen.getByText(/session is closed/)).toBeTruthy();
      expect(screen.getByRole("button", { name: /try again/i })).toBeTruthy();
    });

    it("ignores a resume that failed against an earlier turn", () => {
      // A resume that fired and then hit the limit again is marked failed
      // against the turn it started — saying "could not resume" on the new
      // limit would be a lie about work that did carry on.
      render(view(limited({ scheduledPrompts: [armed({ status: "failed", resumeOf: "t0" })] })));
      expect(screen.queryByText(/did not work/)).toBeNull();
    });

    it("names the resumed turn for what it is once it runs", () => {
      const s = limited() as never as ReturnType<typeof failed>;
      s.items.push({
        id: "u2",
        kind: "message",
        role: "user",
        text: "[omniplex] Your previous turn stopped because…",
        turnId: "t2",
        receivedAt: 2,
      });
      s.turns.push({
        id: "t2",
        done: false,
        recovery: { resumeOf: "t1", attempt: 1, cause: "limit" },
      });
      render(view(s));
      expect(screen.getByText("Usage limit reset — the agent picked the work back up")).toBeTruthy();
    });
  });
});
