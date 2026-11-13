// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Transcript } from "./Transcript";
import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import { render, viewport } from "~/test/harness";
import { shortTime } from "~/lib/scheduleTime";
import type { PullRequest, ScheduledPrompt, Turn } from "~/protocol";

// Every message the transcript draws, counted by item id and then drawn for
// real: the other tests here still see the actual message.
const messageRenders = vi.hoisted(() => new Map<string, number>());
vi.mock("./transcript/Message", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./transcript/Message")>();
  return {
    Message: (props: Parameters<typeof actual.Message>[0]) => {
      messageRenders.set(props.item.id, (messageRenders.get(props.item.id) ?? 0) + 1);
      return <actual.Message {...props} />;
    },
  };
});

const state = (text: string): any => ({
  threadId: "a",
  seq: 1,
  cwd: "/tmp/repo",
  harness: "claude",
  model: "",
  mode: "default",
  effort: "",
  serviceTier: "",
  title: "Thread a",
  phase: "idle",
  closed: false,
  workspace: { phase: "ready", projectId: "p1", projectRoot: "/tmp/repo" },
  items: [{ id: "m1", kind: "message", role: "agent", contentKind: "text", text, receivedAt: 1 }],
  turns: [{ id: "t1", status: "done" }],
  usage: {},
  pendingPermissions: [],
  pendingElicitations: [],
  queuedPrompts: [],
});

// The transcript reads its thread from the store the app attaches it into, so
// each mount here gets a store of its own holding `s`, and `frame` is the next
// state arriving off the wire.
function attach(s: any, ui: ReactElement) {
  const store = createThreadStore(s);
  const r = render(<ThreadStoreContext.Provider value={store}>{ui}</ThreadStoreContext.Provider>);
  return { ...r, frame: (next: any) => act(() => store.set(next)) };
}

function transcript(text: string) {
  return attach(
    state(text),
    <Transcript
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

    attach(userState, view());
    fireEvent.click(screen.getByLabelText("Copy message"));

    expect(writeText).toHaveBeenCalledWith("A long **raw** prompt");
  });
});

// A prompt sent into a thread on screen is lifted to the top of the view so
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

const prompts = (ids: string[], threadId = "a"): any => ({
  ...state(""),
  threadId,
  items: ids.map((id) => ({ id, kind: "message", role: "user", text: id, receivedAt: 1 })),
});

function view() {
  return (
    <Transcript
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
  it("anchors a prompt that arrives while the thread is on screen", () => {
    const { container, frame } = attach(prompts(["p1"]), view());
    const el = measured(container);

    frame(prompts(["p1", "p2"]));

    expect(reserve(container)).toBe(RESERVE);
    expect(el.scrollTop).toBe(PROMPT_TOP - 16);
  });

  it("anchors the first prompt of a thread that had none", () => {
    const { container, frame } = attach(prompts([]), view());
    measured(container);

    frame(prompts(["p1"]));

    expect(reserve(container)).toBe(RESERVE);
  });

  it("leaves a thread that was only just opened where it is", () => {
    // Switching threads changes the newest prompt too — the whole transcript
    // arrives at once — and nobody asked for that view to move.
    const { container, frame } = attach(prompts(["p1"]), view());
    measured(container);

    frame(prompts(["p9"], "b"));

    expect(reserve(container)).toBe("");
  });
});

// Leaving a thread and coming back, end to end through the component: the
// position it reports as it unmounts is the one it is handed on remount.
//
// jsdom lays nothing out, so each row's body is given a height and stacked
// with no gap, in a 400px window over the rows plus a 500px tail.
describe("coming back to a thread", () => {
  let sizes: Record<string, number>;
  const topOf = (id: string) => {
    const ids = Object.keys(sizes);
    return ids.slice(0, ids.indexOf(id)).reduce((a, k) => a + sizes[k], 0);
  };
  const rowOf = (el: HTMLElement) => el.parentElement?.getAttribute("data-row-id") ?? undefined;
  const scroller = () => document.querySelector<HTMLElement>("[data-transcript-scroller]")!;

  beforeEach(() => {
    sizes = { p1: 300, p2: 300, p3: 300, p4: 300 };
    const height = () => Object.values(sizes).reduce((a, h) => a + h, 0) + 500;
    let top = 0;
    const isScroller = (el: HTMLElement) => el.hasAttribute("data-transcript-scroller");
    vi.spyOn(HTMLElement.prototype, "offsetTop", "get").mockImplementation(function (this: HTMLElement) {
      const id = rowOf(this);
      return id ? topOf(id) : 0;
    });
    vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(function (this: HTMLElement) {
      const id = rowOf(this);
      return id ? sizes[id] : 0;
    });
    vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockReturnValue(null);
    Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
      get() {
        return isScroller(this) ? height() : 0;
      },
      configurable: true,
    });
    Object.defineProperty(HTMLElement.prototype, "clientHeight", { value: 400, configurable: true });
    Object.defineProperty(HTMLElement.prototype, "scrollTop", {
      get() {
        return isScroller(this) ? top : 0;
      },
      set(v: number) {
        if (isScroller(this)) top = Math.max(0, Math.min(v, height() - 400));
      },
      configurable: true,
    });
    // The settle that follows a restore is scrollAnchor's to test; here the
    // frames never come.
    vi.stubGlobal("requestAnimationFrame", () => 0);
  });
  afterEach(() => {
    delete (HTMLElement.prototype as any).scrollHeight;
    delete (HTMLElement.prototype as any).clientHeight;
    delete (HTMLElement.prototype as any).scrollTop;
  });

  const mount = (props: { initialScroll?: any; onScrollChange?: any }) =>
    attach(
      prompts(["p1", "p2", "p3", "p4"]),
      <Transcript
        onFinish={() => {}}
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onOpenDiff={() => {}}
        {...props}
      />,
    );

  it("returns to the row being read, though the rows above it have grown since", () => {
    const onScrollChange = vi.fn();
    const first = mount({ onScrollChange });
    act(() => {
      scroller().scrollTop = 450;
      scroller().dispatchEvent(new Event("scroll"));
    });
    first.unmount();
    const left = onScrollChange.mock.lastCall![1];

    // Something above the reader rendered taller the second time round.
    sizes.p1 = 500;
    mount({ initialScroll: left });

    expect(scroller().scrollTop).toBe(650);
  });
});

// A worktree thread whose branch has landed is offered a way out of the
// transcript it is being read in.
function merged(pr: PullRequest | null, onFinish = () => {}) {
  return attach(
    state("done"),
    <Transcript
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
  it("offers to finish the thread once the branch has landed", () => {
    merged(MERGED);
    expect(screen.getByRole("button", { name: /finish with this thread/i })).toBeTruthy();
    expect(screen.getByText("PR #75 merged")).toBeTruthy();
  });

  it("says nothing while the pull request is still open", () => {
    merged({ number: 75, state: "OPEN", merged: false });
    expect(screen.queryByRole("button", { name: /finish with this thread/i })).toBeNull();
  });

  it("says nothing when there is no pull request to speak of", () => {
    merged(null);
    expect(screen.queryByRole("button", { name: /finish with this thread/i })).toBeNull();
  });

  it("opens the confirmation rather than deleting anything itself", () => {
    const onFinish = vi.fn();
    merged(MERGED, onFinish);
    fireEvent.click(screen.getByRole("button", { name: /finish with this thread/i }));
    expect(onFinish).toHaveBeenCalledTimes(1);
  });

  it("still names the offer on a phone, where there is no hover to explain it", () => {
    viewport("phone");
    merged(MERGED);
    // The tooltip does not render on a coarse pointer, so the accessible name
    // is the whole explanation and has to carry it alone.
    expect(screen.getByRole("button", { name: /finish with this thread/i })).toBeTruthy();
  });
});

// The provisioner's card is a receipt, not a task. It says "ready" and then
// leaves on its own, so the empty transcript it was sitting on top of is free
// for the affordance the user actually needs.
const empty = (over: any = {}): any => ({ ...state(""), items: [], ...over });

function element() {
  return (
    <Transcript
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
  const r = attach(empty({ phase: "provisioning", workspace: { phase: "provisioning" } }), element());
  r.frame(s);
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
    attach(empty(), element());
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
    const { container, frame } = provisioner(empty());
    await act(async () => {
      vi.advanceTimersByTime(3000);
    });

    // The collapse has started but not finished; cleanup begins.
    frame(empty({ phase: "cleaning", workspace: { phase: "cleaning" } }));

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
  return attach(
    s,
    <Transcript
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
  it("offers them when there is nothing in the thread yet", () => {
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

  it("lets a signed-out user sign in from the failed thread", async () => {
    const onLogin = vi.fn();
    attach(
      failed({ failure: "auth", error: "claude needs you to sign in again: …" }),
      <Transcript
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

    expect(await screen.findByText(/Claude Code needs to sign in again/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
    expect(onLogin).toHaveBeenCalledOnce();
    expect(screen.queryByText(/Continue where it left off/)).toBeNull();
    expect(screen.queryByText(/restart/i)).toBeNull();
  });

  // Retry appears only once the provider reports ready again, and re-sending
  // the failed prompt is always the user's press — never a side effect of a
  // sign-in finishing.
  it("offers retry only when the provider is ready, and only on request", async () => {
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

    const { unmount } = attach(
      authFailed,
      <Transcript {...props} providerReady={false} />,
    );
    await screen.findByText(/needs to sign in again/);
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
    unmount();

    attach(authFailed, <Transcript {...props} providerReady />);
    const retry = await screen.findByRole("button", { name: /retry this prompt/i });
    expect(onRetryTurn).not.toHaveBeenCalled();
    fireEvent.click(retry);
    expect(onRetryTurn).toHaveBeenCalledOnce();
    expect(onRetryTurn.mock.calls[0][0]).toMatchObject({ id: "t1", prompt: "go" });
  });

  // A limit is not fixed by retrying on the same account. The card offers the
  // harness's other accounts; once the thread has moved, it offers the retry.
  describe("on a usage limit", () => {
    const limited = () => failed({ failure: "limit", error: "You've hit your thread limit · resets 2:10pm" });
    const props = {
      onFinish: () => {},
      onRetryProvision: () => {},
      onCleanup: () => {},
      onForceDelete: () => {},
      onContinue: () => {},
      onOpenDiff: () => {},
      providerName: "Aaron",
    };

    it("moves the failed prompt to the account picked", async () => {
      const onSwitchAccount = vi.fn((_instance: string, _turn: Turn) => new Promise<boolean>(() => {}));
      attach(
        limited(),
        <Transcript
          {...props}
          onRetryTurn={() => {}}
          switchTargets={[
            { id: "work", name: "Worksauce" },
            { id: "spare", name: "Spare" },
          ]}
          onSwitchAccount={onSwitchAccount}
        />,
      );
      const spare = await screen.findByRole("button", { name: /continue on spare/i });
      expect(screen.queryByText(/Continue where it left off/)).toBeNull();
      fireEvent.click(spare);
      expect(onSwitchAccount).toHaveBeenCalledOnce();
      expect(onSwitchAccount.mock.calls[0][0]).toBe("spare");
      expect(onSwitchAccount.mock.calls[0][1]).toMatchObject({ id: "t1", prompt: "go" });
      // One press: a second account's button cannot race the first switch.
      expect((screen.getByRole("button", { name: /continue on worksauce/i }) as HTMLButtonElement).disabled).toBe(true);
    });

    it("can be used again when the switch is refused", async () => {
      attach(
        limited(),
        <Transcript
          {...props}
          switchTargets={[{ id: "work", name: "Worksauce" }]}
          onSwitchAccount={async () => false}
        />,
      );
      const button = (await screen.findByRole("button", { name: /continue on worksauce/i })) as HTMLButtonElement;
      fireEvent.click(button);
      await waitFor(() => expect(button.disabled).toBe(false));
    });

    it("offers only the retry once the thread has switched", async () => {
      const s = limited();
      s.items.push({ id: "account:9", kind: "notice", noticeKind: "account", title: "Worksauce" });
      const onRetryTurn = vi.fn();
      attach(
        s,
        <Transcript
          {...props}
          onRetryTurn={onRetryTurn}
          switchTargets={[{ id: "aaron", name: "Aaron" }]}
          onSwitchAccount={async () => true}
        />,
      );
      const retry = await screen.findByRole("button", { name: /retry this prompt/i });
      expect(screen.getAllByText(/Switched to Worksauce/).length).toBeGreaterThan(0);
      expect(screen.queryByRole("button", { name: /continue on/i })).toBeNull();
      fireEvent.click(retry);
      expect(onRetryTurn).toHaveBeenCalledOnce();
    });

    // Extensions speak up as the switched session starts; their notice lands
    // after the switch and must not read as "something happened since".
    it("still counts the switch when an extension notice follows it", async () => {
      const s = limited();
      s.items.push(
        { id: "account:9", kind: "notice", noticeKind: "account", title: "Worksauce" },
        { id: "notify:10", kind: "notice", noticeKind: "notify", level: "warning", text: "No API key for lint" },
      );
      attach(
        s,
        <Transcript
          {...props}
          onRetryTurn={() => {}}
          switchTargets={[{ id: "aaron", name: "Aaron" }]}
          onSwitchAccount={async () => true}
        />,
      );
      expect(await screen.findByRole("button", { name: /retry this prompt/i })).toBeTruthy();
      expect(screen.getByText(/No API key for lint/)).toBeTruthy();
      expect(screen.queryByRole("button", { name: /continue on/i })).toBeNull();
    });

    it("with no other account, says to wait and still offers the retry", async () => {
      attach(limited(), <Transcript {...props} onRetryTurn={() => {}} switchTargets={[]} onSwitchAccount={async () => true} />);
      expect(await screen.findByRole("button", { name: /retry this prompt/i })).toBeTruthy();
      expect(screen.queryByRole("button", { name: /continue on/i })).toBeNull();
    });

    // The server arms a continuation at the reset; the card says when, and
    // is where it gets called off.
    describe("with a continuation armed", () => {
      const dueAt = new Date(2026, 10, 11, 15, 40).getTime();
      const schedule = (over: Partial<ScheduledPrompt> = {}): ScheduledPrompt => ({
        id: "s1",
        revision: 1,
        prompt: "continue",
        dueAt,
        timeZone: "Australia/Brisbane",
        model: "",
        mode: "default",
        effort: "",
        serviceTier: "",
        status: "pending",
        kind: "limit",
        ...over,
      });
      const armed = (...schedules: ScheduledPrompt[]) => {
        const s = limited();
        s.scheduledPrompts = schedules;
        return s;
      };
      beforeEach(() => {
        vi.useFakeTimers({ toFake: ["Date"] });
        vi.setSystemTime(new Date(2026, 10, 11, 12, 0));
      });
      afterEach(() => vi.useRealTimers());

      it("says when it continues", async () => {
        attach(armed(schedule()), <Transcript {...props} onCancelContinuation={async () => {}} />);
        expect(await screen.findByText(`Continuing at ${shortTime(dueAt)}`)).toBeTruthy();
        expect(screen.getByRole("button", { name: "Cancel" })).toBeTruthy();
      });

      it("says nothing once it is cancelled or sent, or when there is none", async () => {
        for (const s of [
          armed(schedule({ status: "cancelled" })),
          armed(schedule({ status: "sent" })),
          armed(schedule({ kind: undefined })),
          limited(),
        ]) {
          const { unmount } = attach(s, <Transcript {...props} onCancelContinuation={async () => {}} />);
          await screen.findByText(/hit its usage limit/);
          expect(screen.queryByText(/Continuing/)).toBeNull();
          unmount();
        }
      });

      it("cancels that schedule, and says so when it could not", async () => {
        let fail!: (e: Error) => void;
        const onCancelContinuation = vi.fn(
          (_p: ScheduledPrompt) => new Promise<void>((_, reject) => (fail = reject)),
        );
        attach(
          armed(schedule({ id: "human", kind: undefined }), schedule()),
          <Transcript
            {...props}
            onCancelContinuation={onCancelContinuation}
          />,
        );
        fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
        expect(onCancelContinuation).toHaveBeenCalledOnce();
        expect(onCancelContinuation.mock.calls[0][0]).toMatchObject({ id: "s1", revision: 1 });
        const pending = screen.getByRole("button", { name: /Cancelling/ }) as HTMLButtonElement;
        expect(pending.disabled).toBe(true);
        await act(async () => fail(new Error("Reconnect first")));
        expect(screen.getByRole("alert").textContent).toBe("Reconnect first");
        expect((screen.getByRole("button", { name: "Cancel" }) as HTMLButtonElement).disabled).toBe(false);
      });
    });

    it("labels queued prompts as held only while the latest turn is stopped at a limit", async () => {
      const queued = [{ queueId: "q1", prompt: "next" }];
      const s = limited();
      s.queuedPrompts = queued;
      const first = attach(s, <Transcript {...props} />);
      // Nothing armed: only the reader sending moves it.
      expect(await screen.findByText(/held until you send/)).toBeTruthy();
      first.unmount();

      s.scheduledPrompts = [
        { id: "s1", revision: 1, prompt: "continue", dueAt: Date.now() + 60_000, timeZone: "UTC",
          model: "", mode: "default", effort: "", status: "pending", kind: "limit" },
      ];
      const { unmount } = attach(s, <Transcript {...props} />);
      expect(await screen.findByText(/held until the limit resets/)).toBeTruthy();
      unmount();

      for (const other of [failed({ failure: "auth" }), failed({}), state("hi")]) {
        other.queuedPrompts = queued;
        const { unmount } = attach(other, <Transcript {...props} />);
        // The card loads lazily: wait for it, or the absence proves nothing.
        await screen.findByText("Queued");
        expect(screen.queryByText(/held until/)).toBeNull();
        unmount();
      }
    });
  });

  it("holds only what was queued before the limit, and nothing once the thread moved account", async () => {
    const s = failed({ failure: "limit", error: "You've hit your limit", finishedAt: 10 });
    s.queuedPrompts = [
      { queueId: "q1", prompt: "before", queuedAt: 5 },
      { queueId: "q2", prompt: "by hand", queuedAt: 20 },
    ];
    const props = {
      onFinish: () => {},
      onRetryProvision: () => {},
      onCleanup: () => {},
      onForceDelete: () => {},
      onContinue: () => {},
      onOpenDiff: () => {},
    };
    const first = attach(s, <Transcript {...props} />);
    expect(await screen.findAllByText(/held until you send/)).toHaveLength(1);
    first.unmount();

    s.items.push({ id: "account:9", kind: "notice", noticeKind: "account", title: "Worksauce", receivedAt: 30 });
    attach(s, <Transcript {...props} />);
    expect(await screen.findAllByText("Queued")).toHaveLength(2);
    expect(screen.queryByText(/held until/)).toBeNull();
  });

  it("offers Resume only for a paused queue, and still says what a limit holds it for", async () => {
    const s = failed({ failure: "limit", error: "You've hit your limit", finishedAt: 10 });
    s.queuedPrompts = [{ queueId: "q1", prompt: "before", queuedAt: 5 }];
    const props = {
      onFinish: () => {},
      onRetryProvision: () => {},
      onCleanup: () => {},
      onForceDelete: () => {},
      onContinue: () => {},
      onOpenDiff: () => {},
    };
    const first = attach(s, <Transcript {...props} />);
    // The limit lifts on its own; there is nothing to resume.
    expect(await screen.findByText("held until you send")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Resume queue" })).toBeNull();
    first.unmount();

    s.queuedPrompts = [{ queueId: "q1", prompt: "before", queuedAt: 5, held: true }];
    attach(s, <Transcript {...props} />);
    expect(await screen.findByText("until you send")).toBeTruthy();
    expect(screen.getByText("Held")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Resume queue" })).toBeTruthy();
  });

  it("only says the server restarted when the server says so", async () => {
    attach(failed({ failure: "restart", error: "server restarted during turn" }), view());
    expect(await screen.findByText(/The server restarted/)).toBeTruthy();

    attach(failed({ error: "claude exited: ENOENT" }), view());
    expect(await screen.findByText(/ended with an error/)).toBeTruthy();
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
    attach(s, view());

    expect(screen.getByText("Asked the agent to pick the work back up")).toBeTruthy();
    expect(screen.queryByText(/Server restarted/)).toBeNull();
  });

  it("shows a sent limit continuation as the server's note, not the reader's prompt", () => {
    const s = failed({ failure: "limit" });
    s.items.push({
      id: "u2",
      kind: "message",
      role: "user",
      text: "[omniplex] Your previous turn stopped because the account hit its usage limit…",
      turnId: "t2",
      receivedAt: 2,
    });
    s.turns.push({ id: "t2", done: false });
    const sent = { id: "limit-t1", kind: "limit", prompt: "", dueAt: 1, timeZone: "UTC" };
    s.scheduledPrompts = [{ ...sent, status: "sent", turnId: "t2" }];
    const { unmount } = attach(s, view());
    expect(screen.getByText("Usage limit reset — the agent was asked to pick the work back up")).toBeTruthy();
    expect(screen.queryByText(/account hit its usage limit/)).toBeNull();
    unmount();

    // A human's own schedule that started a turn is still their prompt.
    s.scheduledPrompts = [{ ...sent, kind: undefined, status: "sent", turnId: "t2" }];
    attach(s, view());
    expect(screen.getByText(/account hit its usage limit/)).toBeTruthy();
    expect(screen.queryByText(/Usage limit reset/)).toBeNull();
  });
});

describe("published artefacts", () => {
  const published = (n: number) => {
    const s = state("done");
    s.items = Array.from({ length: n }, (_, i) => ({
      id: `art${i}`,
      kind: "artefact",
      artefactId: `a${i}`,
      version: 1,
      title: `file-${i}.md`,
      mediaType: "text/markdown",
      size: 10,
      receivedAt: 1,
    }));
    s.turns = [];
    return attach(
      s,
      <Transcript
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onOpenDiff={() => {}}
        onFinish={() => {}}
      />,
    );
  };
  const tiles = () => screen.queryAllByText(/^file-\d+\.md$/);

  it("folds a long run of files behind a button that shows the rest", () => {
    published(9);
    expect(tiles()).toHaveLength(3);
    fireEvent.click(screen.getByRole("button", { name: "Show 6 more files" }));
    expect(tiles()).toHaveLength(9);
    expect(screen.queryByRole("button", { name: /^Show \d+ more/ })).toBeNull();
  });

  it("does not fold away a single file", () => {
    published(4);
    expect(tiles()).toHaveLength(4);
    expect(screen.queryByRole("button", { name: /^Show \d+ more/ })).toBeNull();
  });
});

describe("a held queue", () => {
  function queued(prompts: { queueId: string; prompt: string; held?: boolean }[], over: Record<string, unknown> = {}) {
    const onResumeQueue = vi.fn();
    const onSendQueued = vi.fn();
    attach(
      { ...state("hi"), queuedPrompts: prompts, ...over },
      <Transcript
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onOpenDiff={() => {}}
        onFinish={() => {}}
        onResumeQueue={onResumeQueue}
        onSendQueued={onSendQueued}
        steers
      />,
    );
    return { onResumeQueue, onSendQueued };
  }

  it("asks to resume while anything in it is held", () => {
    const { onResumeQueue } = queued([
      { queueId: "q1", prompt: "a" },
      { queueId: "q2", prompt: "b", held: true },
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Resume queue" }));
    expect(onResumeQueue).toHaveBeenCalledTimes(1);
  });

  it("does not ask once nothing is held", () => {
    queued([{ queueId: "q1", prompt: "a", held: false }]);
    expect(screen.queryByRole("button", { name: "Resume queue" })).toBeNull();
  });

  it("offers to steer a queued prompt in while a turn runs", async () => {
    const { onSendQueued } = queued([{ queueId: "q1", prompt: "a" }], { phase: "turn" });
    fireEvent.click(await screen.findByRole("button", { name: "Steer now" }));
    expect(onSendQueued).toHaveBeenCalledWith("q1");
  });
});

describe("a turn gone quiet", () => {
  // A turn whose tail is a tool call still running: the case where a retry
  // would otherwise look hung.
  const running = (phase: "turn" | "idle"): any => ({
    ...state("working"),
    phase,
    items: [
      { id: "u1", kind: "message", role: "user", contentKind: "text", text: "go", turnId: "t1", receivedAt: 1 },
      { id: "x1", kind: "tool", toolKind: "execute", status: "in_progress", title: "npm test", turnId: "t1" },
    ],
    turns: [{ id: "t1", status: phase === "turn" ? "running" : "done" }],
  });
  const view = (phase: "turn" | "idle") =>
    attach(
      running(phase),
      <Transcript
        turnStatus={{ state: "compacting" }}
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onOpenDiff={() => {}}
        onFinish={() => {}}
      />,
    );

  it("says why while a tool run is live", () => {
    view("turn");
    expect(screen.getByText("Compacting context…").closest("[role=status]")).not.toBeNull();
  });

  it("says nothing once the thread is idle", () => {
    view("idle");
    expect(screen.queryByText("Compacting context…")).toBeNull();
  });
});

describe("the working line", () => {
  const prompt = { id: "u1", kind: "message", role: "user", contentKind: "text", text: "go", turnId: "t1", receivedAt: 1 };
  const thought = (text: string) => ({ id: "th1", kind: "message", role: "agent", contentKind: "thought", text, turnId: "t1" });
  const running = (items: any[], phase = "turn"): any => ({
    ...state("unused"),
    phase,
    items,
    turns: [{ id: "t1", done: phase !== "turn", startedAt: 100_000, finishedAt: phase === "turn" ? undefined : 160_000 }],
  });
  const view = (s: any) =>
    attach(
      s,
      <Transcript
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onOpenDiff={() => {}}
        onFinish={() => {}}
      />,
    );
  const line = () => document.querySelector("[data-working-line]") as HTMLElement | null;

  afterEach(() => {
    vi.useRealTimers();
  });

  it("times the running turn, and keeps ticking", () => {
    vi.useFakeTimers({ now: 112_400 });
    view(running([prompt]));
    expect(line()?.textContent).toBe("Thinking·12s");
    act(() => vi.advanceTimersByTime(50_000));
    expect(line()?.textContent).toBe("Thinking·1m 2s");
  });

  it("says working while a call runs", () => {
    view(running([prompt, { id: "x1", kind: "tool", toolKind: "execute", status: "in_progress", title: "npm test", turnId: "t1" }]));
    expect(line()?.textContent).toMatch(/^Working·/);
  });

  it("previews reasoning at the tail, and keeps it off a phone", () => {
    view(running([prompt, thought("First.\n\n**Reading the router**\n\nIt routes by path.")]));
    expect(line()?.textContent).toContain("Reading the router");
    expect(screen.getByText("It routes by path.").closest("[data-previewed]")).toBeTruthy();
  });

  it("says it is waiting while a permission is up", () => {
    const call = { id: "x1", kind: "tool", toolKind: "execute", status: "pending", title: "rm -rf build", turnId: "t1" };
    view({ ...running([prompt, call]), pendingPermissions: [{ requestId: "r1", toolCallId: "x1", toolName: "Bash", title: "rm", options: [] }] });
    expect(line()?.textContent).toMatch(/^Waiting for you·/);
  });

  it("names a call still running once the agent writes past it", () => {
    const call = { id: "x1", kind: "tool", toolKind: "execute", status: "in_progress", title: "npm run build", turnId: "t1" };
    const text = { id: "m1", kind: "message", role: "agent", contentKind: "text", text: "While that builds", turnId: "t1" };
    view(running([prompt, call, text]));
    expect(line()?.textContent).toMatch(/^Working·.+·npm run build$/);
  });

  it("draws the reasoning in full once the agent moves past it", () => {
    const { frame } = view(running([prompt, thought("It routes by path.")]));
    frame(
      running([
        prompt,
        thought("It routes by path."),
        { id: "m1", kind: "message", role: "agent", contentKind: "text", text: "Fixed.", turnId: "t1" },
      ]),
    );
    expect(screen.getByText("It routes by path.").closest("[data-previewed]")).toBeNull();
    expect(line()?.textContent).toMatch(/^Working·/);
  });

  it("is gone once the turn ends", () => {
    view(running([prompt, thought("It routes by path.")], "idle"));
    expect(line()).toBeNull();
  });
});

// Every frame off the wire is a new thread state. A frame redraws the rows it
// changed and no others: a streamed token repainting every message above it,
// or a subagent's progress repainting the whole conversation, is what made a
// long thread crawl on a phone.
describe("redrawing on a new frame", () => {
  const props = {
    onFinish: () => {},
    onRetryProvision: () => {},
    onCleanup: () => {},
    onForceDelete: () => {},
    onContinue: () => {},
    onOpenDiff: () => {},
  };
  const streaming = (): any => ({
    ...state(""),
    phase: "turn",
    items: [
      { id: "u1", kind: "message", role: "user", text: "first", turnId: "t1", receivedAt: 1 },
      { id: "a1", kind: "message", role: "agent", contentKind: "text", text: "Done.", turnId: "t1", receivedAt: 2 },
      { id: "u2", kind: "message", role: "user", text: "second", turnId: "t2", receivedAt: 3 },
      { id: "spawn", kind: "tool", toolKind: "agent", title: "scout", status: "in_progress", turnId: "t2", receivedAt: 4 },
      { id: "a2", kind: "message", role: "agent", contentKind: "text", text: "Look", turnId: "t2", receivedAt: 5 },
    ],
    turns: [
      { id: "t1", prompt: "first", done: true, stopReason: "end_turn" },
      { id: "t2", prompt: "second", done: false },
    ],
    jobs: [{ id: "j1", toolCallId: "spawn", name: "scout", kind: "agent", status: "running", depth: 0, usage: {}, activity: "reading files" }],
    usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, cost: 0 },
  });
  const mount = (s: any) => {
    const r = attach(s, <Transcript {...props} />);
    messageRenders.clear();
    return r.frame;
  };

  it("redraws only the message a streamed chunk landed in", () => {
    const s = streaming();
    const rerender = mount(s);

    // What apply.ts makes of a chunk: the one item copied, the rest the same.
    const items = s.items.slice();
    items[4] = { ...items[4], text: "Looking" };
    rerender({ ...s, seq: 2, items });

    expect(messageRenders.get("a2")).toBeGreaterThan(0);
    expect([...messageRenders.keys()]).toEqual(["a2"]);
  });

  it("redraws no message for a job or usage tick, though the spawn card shows it", () => {
    const s = streaming();
    const rerender = mount(s);

    rerender({ ...s, seq: 2, jobs: [{ ...s.jobs[0], activity: "running tests" }] });
    rerender({ ...s, seq: 3, jobs: [{ ...s.jobs[0], activity: "running tests" }], usage: { ...s.usage, output: 2 } });
    expect(screen.getByText("running tests")).toBeTruthy();
    // The job finishing settles its spawn call: a new item list, in which
    // only that call is a new object.
    const items = s.items.map((it: any) => (it.id === "spawn" ? { ...it, status: "completed" } : it));
    rerender({ ...s, seq: 4, items, jobs: [{ ...s.jobs[0], status: "completed" }] });

    expect(messageRenders.size).toBe(0);
  });
});

// The diff panel names a turn by its place in the thread, so a card has to
// hand over the number of the turn it sits under, not its row's or its own.
describe("a turn's changed-files card", () => {
  it("opens the diff on the turn it sits under, numbered by its place in the thread", () => {
    const onOpenDiff = vi.fn();
    const changed = (turnId: string, path: string) => ({
      turnId,
      files: [{ path, status: "modified", additions: 1, deletions: 0 }],
      additions: 1,
      deletions: 0,
    });
    const s = {
      ...state(""),
      items: [
        { id: "u1", kind: "message", role: "user", text: "first", turnId: "t1", receivedAt: 1 },
        { id: "a1", kind: "message", role: "agent", contentKind: "text", text: "One.", turnId: "t1", receivedAt: 2 },
        { id: "u2", kind: "message", role: "user", text: "second", turnId: "t2", receivedAt: 3 },
        { id: "a2", kind: "message", role: "agent", contentKind: "text", text: "Two.", turnId: "t2", receivedAt: 4 },
        { id: "u3", kind: "message", role: "user", text: "third", turnId: "t3", receivedAt: 5 },
        { id: "a3", kind: "message", role: "agent", contentKind: "text", text: "Three.", turnId: "t3", receivedAt: 6 },
      ],
      // The middle turn changed nothing: it has no card, but still counts.
      turns: [
        { id: "t1", done: true, stopReason: "end_turn", diff: changed("t1", "a.ts") },
        { id: "t2", done: true, stopReason: "end_turn" },
        { id: "t3", done: true, stopReason: "end_turn", diff: changed("t3", "c.ts") },
      ],
    };
    attach(
      s,
      <Transcript
        onRetryProvision={() => {}}
        onCleanup={() => {}}
        onForceDelete={() => {}}
        onContinue={() => {}}
        onOpenDiff={onOpenDiff}
        onFinish={() => {}}
      />,
    );

    const [first, last] = screen.getAllByRole("button", { name: "Open diff" });
    fireEvent.click(first);
    expect(onOpenDiff).toHaveBeenLastCalledWith("a.ts", expect.objectContaining({ id: "t1", number: 1 }));
    fireEvent.click(last);
    expect(onOpenDiff).toHaveBeenLastCalledWith("c.ts", expect.objectContaining({ id: "t3", number: 3 }));
  });
});
