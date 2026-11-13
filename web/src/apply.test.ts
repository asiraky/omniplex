import { describe, expect, it } from "vitest";

import { applyEvent, capToolText, emptyState, prependItems, TOOL_HEAD, TOOL_MAX_ENTRIES, TOOL_MAX_TEXT, TOOL_TAIL } from "./apply";
import type { Event, Item, ThreadState } from "./protocol";

function ev(seq: number, type: string, payload: Record<string, unknown>, timestamp = 0): Event {
  return { threadId: "s1", seq, timestamp, type, payload } as Event;
}

// These mirror internal/projection/state_test.go: the client reducer and the
// server projection must reach the same phase from the same events.
describe("applyEvent turn lifecycle", () => {
  it("opens a harness-initiated turn without fabricating a prompt item", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1" }));
    expect(s.phase).toBe("turn");
    expect(s.turns).toHaveLength(1);
    expect(s.items).toHaveLength(0);

    s = applyEvent(s, ev(2, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    expect(s.phase).toBe("idle");
    expect(s.turns[0].done).toBe(true);
  });

  it("stamps the turn with when it started and finished", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }, 1000));
    s = applyEvent(s, ev(2, "turn.finished", { turnId: "t1", stopReason: "end_turn" }, 35_000));
    expect(s.turns[0].startedAt).toBe(1000);
    expect(s.turns[0].finishedAt).toBe(35_000);
  });

  it("keeps the prompt item on a prompted turn", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "do the thing" }));
    expect(s.items).toHaveLength(1);
    expect(s.items[0].text).toBe("do the thing");
  });

  it("treats streaming while idle as a running turn", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "message.chunk", { blockId: "b1", role: "agent", kind: "text", delta: "The web" }));
    expect(s.phase).toBe("turn");

    let s2 = emptyState("s2");
    s2 = applyEvent(s2, ev(1, "tool_call.started", { toolCallId: "c1", kind: "execute", title: "ls", status: "pending" }));
    expect(s2.phase).toBe("turn");
  });

  it("ignores a stale turn.finished while a different turn is open", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "tool_call.started", { turnId: "t1", toolCallId: "c1", kind: "execute", title: "ls", status: "in_progress" }));
    s = applyEvent(s, ev(3, "turn.finished", { turnId: "t-stale", stopReason: "error" }));
    expect(s.phase).toBe("turn");
    expect(s.items[1].status).toBe("in_progress");

    s = applyEvent(s, ev(4, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    expect(s.phase).toBe("idle");
    expect(s.items[1].status).toBe("cancelled");
  });

  it("treats a tool going active while idle as a running turn, but not a straggling completion", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "tool_call.updated", { toolCallId: "c1", status: "completed" }));
    expect(s.phase).toBe("idle");
    s = applyEvent(s, ev(2, "tool_call.updated", { toolCallId: "c2", status: "in_progress" }));
    expect(s.phase).toBe("turn");
  });

  it("does not reopen a closed thread on a stray chunk", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "thread.closed", { reason: "closed" }));
    s = applyEvent(s, ev(2, "message.chunk", { blockId: "b1", role: "agent", kind: "text", delta: "late" }));
    expect(s.phase).toBe("closed");
  });

  it("folds a compaction boundary into a standalone notice item", () => {
    let s = emptyState("s1");
    s = applyEvent(
      s,
      ev(1, "context.compacted", { trigger: "auto", preTokens: 180000, postTokens: 42000 }, 5000),
    );
    expect(s.items).toHaveLength(1);
    const it = s.items[0];
    expect(it.kind).toBe("notice");
    expect(it.noticeKind).toBe("compaction");
    expect(it.trigger).toBe("auto");
    expect(it.preTokens).toBe(180000);
    expect(it.postTokens).toBe(42000);
    // No turn id: it stands on its own line rather than folding into a turn.
    expect(it.turnId).toBeUndefined();
  });

  it("carries unclamped context occupancy through usage.updated", () => {
    let s = emptyState("s1");
    s = applyEvent(
      s,
      ev(1, "usage.updated", {
        input: 1,
        output: 1,
        cacheRead: 1,
        cacheWrite: 0,
        cost: 0,
        contextPct: 106,
        contextUsed: 212000,
        contextWindow: 200000,
      }),
    );
    expect(s.usage.contextPct).toBe(106);
    expect(s.usage.contextUsed).toBe(212000);
  });
});

// Mirrors internal/projection/state_test.go.
describe("turn suggestions", () => {
  function finished(): ThreadState {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    return s;
  }

  it("keeps a suggestion for the latest turn", () => {
    const s = applyEvent(finished(), ev(3, "turn.suggested", { turnId: "t1", text: "run the tests" }));
    expect(s.suggestion).toEqual({ turnId: "t1", text: "run the tests" });
  });

  it("ignores a suggestion for a turn that is no longer the latest", () => {
    let s = applyEvent(finished(), ev(3, "turn.started", { turnId: "t2", prompt: "next" }));
    s = applyEvent(s, ev(4, "turn.suggested", { turnId: "t1", text: "stale" }));
    expect(s.suggestion).toBeUndefined();
  });

  it("ignores a suggestion for a turn it has never seen", () => {
    const s = applyEvent(finished(), ev(3, "turn.suggested", { turnId: "t9", text: "who?" }));
    expect(s.suggestion).toBeUndefined();
  });

  it("clears the suggestion when the next turn starts", () => {
    let s = applyEvent(finished(), ev(3, "turn.suggested", { turnId: "t1", text: "run the tests" }));
    s = applyEvent(s, ev(4, "turn.started", { turnId: "t2", prompt: "run the tests" }));
    expect(s.suggestion).toBeUndefined();
  });
});

// Mirrors internal/projection/state_test.go: "" is a level the user can
// choose — the harness's own default — so an event carrying it must win.
describe("applyEvent config changes", () => {
  it("lets a cleared effort clear the effort", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "thread.config_changed", { effort: "high" }));
    expect(s.effort).toBe("high");

    s = applyEvent(s, ev(2, "thread.config_changed", { effort: "" }));
    expect(s.effort).toBe("");
  });

  it("leaves effort alone when the event is about something else", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "thread.config_changed", { effort: "high" }));
    s = applyEvent(s, ev(2, "thread.config_changed", { model: "sonnet" }));
    expect(s.effort).toBe("high");
    expect(s.model).toBe("sonnet");
  });

  it("lets an empty service tier turn Fast off", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "thread.config_changed", { serviceTier: "priority" }));
    expect(s.serviceTier).toBe("priority");

    s = applyEvent(s, ev(2, "thread.config_changed", { serviceTier: "" }));
    expect(s.serviceTier).toBe("");
  });

  it("leaves the service tier alone when the event is about something else", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "thread.config_changed", { serviceTier: "priority" }));
    s = applyEvent(s, ev(2, "thread.config_changed", { effort: "low" }));
    expect(s.serviceTier).toBe("priority");
  });
});

// Mirrors internal/projection/state_test.go: a switch is marked where the next
// turn starts, against the settings the last turn ran on.
describe("applyEvent model notices", () => {
  const turn = (seq: number, id: string) => ev(seq, "turn.started", { turnId: id, prompt: "go" }, seq * 1000);
  const config = (seq: number, payload: Record<string, unknown>) => ev(seq, "thread.config_changed", payload);
  const notices = (s: ReturnType<typeof emptyState>) =>
    s.items.filter((it) => it.kind === "notice" && it.noticeKind === "model").map((it) => [it.id, it.model, it.effort]);
  const replay = (...events: Event[]) => events.reduce(applyEvent, emptyState("s1"));

  it("marks a switch above the next turn's prompt, naming what it runs on", () => {
    let s = replay(ev(1, "thread.created", { model: "opus", effort: "high" }), turn(2, "t1"), config(3, { model: "sonnet" }));
    expect(notices(s)).toEqual([]);
    s = [turn(4, "t2"), config(5, { effort: "low" }), turn(6, "t3")].reduce(applyEvent, s);
    expect(notices(s)).toEqual([
      ["model:4", "sonnet", "high"],
      ["model:6", "sonnet", "low"],
    ]);
    expect(s.items.map((it) => it.id)).toEqual(["prompt:t1", "model:4", "prompt:t2", "model:6", "prompt:t3"]);
    expect(s.items[1].receivedAt).toBe(4000);
    expect(s.items[1].from).toEqual({ model: "opus", effort: "high" });
  });

  it("does not mark the first turn", () => {
    const s = replay(ev(1, "thread.created", { model: "opus" }), config(2, { model: "sonnet" }), turn(3, "t1"));
    expect(notices(s)).toEqual([]);
  });

  it("treats switches between turns as one change, and switching back as none", () => {
    let s = replay(
      ev(1, "thread.created", { model: "opus", effort: "high" }),
      turn(2, "t1"),
      config(3, { model: "sonnet" }),
      config(4, { model: "opus" }),
      turn(5, "t2"),
    );
    expect(notices(s)).toEqual([]);
    s = [config(6, { model: "sonnet" }), config(7, { model: "haiku" }), config(8, { effort: "low" }), turn(9, "t3")].reduce(
      applyEvent,
      s,
    );
    expect(notices(s)).toEqual([["model:9", "haiku", "low"]]);
  });

  it("does not take the harness reporting its own model for a switch, nor let it swallow one", () => {
    let s = replay(
      ev(1, "thread.created", { model: "opus" }),
      turn(2, "t1"),
      config(3, { model: "claude-opus-5", harnessSessionId: "h1" }),
      turn(4, "t2"),
    );
    expect(notices(s)).toEqual([]);
    expect(s.model).toBe("claude-opus-5");

    s = [config(5, { model: "sonnet" }), config(6, { model: "claude-sonnet-5", harnessSessionId: "h2" }), turn(7, "t3")].reduce(
      applyEvent,
      s,
    );
    expect(notices(s)).toEqual([["model:7", "sonnet", ""]]);
  });

  it("takes a model report during an effort switch, and still marks the effort", () => {
    let s = replay(
      ev(1, "thread.created", { model: "opus", effort: "high" }),
      turn(2, "t1"),
      config(3, { effort: "low" }),
      config(4, { model: "claude-opus-5", harnessSessionId: "h1" }),
    );
    expect(s.model).toBe("claude-opus-5");
    s = applyEvent(s, turn(5, "t2"));
    expect(notices(s)).toEqual([["model:5", "claude-opus-5", "low"]]);
    expect(s.items[1].from).toEqual({ model: "claude-opus-5", effort: "high" });
  });

  it("keeps a pick that a late report from the turn before names over", () => {
    let s = replay(
      ev(1, "thread.created", { model: "claude-opus-5" }),
      turn(2, "t1"),
      config(3, { model: "sonnet" }),
      config(4, { model: "claude-opus-5", harnessSessionId: "h1" }),
    );
    expect(s.model).toBe("sonnet");
    s = applyEvent(s, turn(5, "t2"));
    expect(notices(s)).toEqual([["model:5", "sonnet", ""]]);
  });

  it("marks the next turn after a snapshot taken between a switch and it", () => {
    const snapshot: ThreadState = JSON.parse(
      JSON.stringify(replay(ev(1, "thread.created", { model: "opus" }), turn(2, "t1"), config(3, { model: "sonnet" }))),
    );
    expect(notices(applyEvent(snapshot, turn(4, "t2")))).toEqual([["model:4", "sonnet", ""]]);
    // A snapshot from before the baseline existed has none to measure from.
    expect(notices(applyEvent({ ...snapshot, ranWith: undefined }, turn(4, "t2")))).toEqual([]);
  });
});

describe("jobs", () => {
  const ev = (seq: number, type: string, payload: unknown, timestamp = seq * 1000) =>
    ({ seq, threadId: "s", type, timestamp, payload }) as unknown as Event;

  it("folds job rows by merging and settles the spawning tool on finish", () => {
    let s = emptyState("s");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "tool_call.started", { turnId: "t1", toolCallId: "c1", kind: "agent", status: "in_progress", title: "Task" }));
    s = applyEvent(s, ev(3, "job.started", { jobId: "j1", toolCallId: "c1", taskType: "subagent", name: "Explore" }));
    s = applyEvent(s, ev(4, "job.updated", { jobId: "j1", usage: { totalTokens: 500 }, activity: "Read" }));
    expect(s.jobs).toHaveLength(1);
    expect(s.jobs[0]).toMatchObject({ kind: "agent", name: "Explore", status: "running", turnId: "t1", activity: "Read" });
    expect(s.jobs[0].usage.totalTokens).toBe(500);
    // The turn ends while the job is live: its tool is left in flight.
    s = applyEvent(s, ev(5, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    expect(s.items[1].status).toBe("in_progress");
    expect(s.phase).toBe("idle");
    s = applyEvent(s, ev(6, "job.finished", { jobId: "j1", status: "stopped" }));
    expect(s.jobs[0].status).toBe("stopped");
    expect(s.jobs[0].finishedAt).toBe(6000);
    expect(s.items[1].status).toBe("cancelled");
  });

  it("keeps a subagent's work out of the parent's turn and alive past its finish", () => {
    let s = emptyState("s");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "tool_call.started", { turnId: "t1", toolCallId: "c1", kind: "agent", status: "in_progress", title: "spawn" }));
    s = applyEvent(s, ev(3, "job.started", { jobId: "j1", toolCallId: "c1", taskType: "subagent" }));
    s = applyEvent(s, ev(4, "tool_call.started", { turnId: "t1", toolCallId: "sub1", parentToolCallId: "c1", kind: "execute", status: "in_progress", title: "go test" }));
    s = applyEvent(s, ev(5, "tool_call.started", { turnId: "t1", toolCallId: "own1", kind: "execute", status: "in_progress", title: "ls" }));
    s = applyEvent(s, ev(6, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    expect(s.items.find((it) => it.id === "sub1")?.status).toBe("in_progress");
    expect(s.items.find((it) => it.id === "own1")?.status).toBe("cancelled");

    s = applyEvent(s, ev(7, "message.chunk", { turnId: "t1", blockId: "b1", role: "agent", kind: "text", delta: "found it", parentToolCallId: "c1" }));
    s = applyEvent(s, ev(8, "tool_call.started", { turnId: "t1", toolCallId: "sub2", parentToolCallId: "c1", kind: "read", status: "pending", title: "read" }));
    s = applyEvent(s, ev(9, "tool_call.updated", { toolCallId: "sub2", parentToolCallId: "c1", status: "in_progress" }));
    expect(s.phase).toBe("idle");
    expect(s.items.find((it) => it.id === "b1")).toMatchObject({ parentId: "c1", text: "found it" });
    expect(s.items.find((it) => it.id === "sub2")).toMatchObject({ parentId: "c1", status: "in_progress" });
  });

  it("keeps a subagent's running command's tail past the parent's turn, and drops it when the job ends", () => {
    let s = emptyState("s");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "tool_call.started", { turnId: "t1", toolCallId: "c1", kind: "agent", status: "in_progress", title: "spawn" }));
    s = applyEvent(s, ev(3, "job.started", { jobId: "j1", toolCallId: "c1", taskType: "subagent" }));
    s = applyEvent(s, ev(4, "tool_call.started", { turnId: "t1", toolCallId: "sub1", parentToolCallId: "c1", kind: "execute", status: "in_progress", title: "go test" }));
    s = applyEvent(s, ev(5, "tool_call.updated", { toolCallId: "sub1", outputDelta: "ok 1\n" }));
    s = applyEvent(s, ev(6, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    expect(s.items.find((it) => it.id === "sub1")).toMatchObject({ status: "in_progress", tail: "ok 1\n" });

    s = applyEvent(s, ev(7, "job.finished", { jobId: "j1", toolCallId: "c1", status: "stopped" }));
    const sub1 = s.items.find((it) => it.id === "sub1");
    expect(sub1?.status).toBe("cancelled");
    expect(sub1 && "tail" in sub1 ? sub1.tail : undefined).toBeUndefined();
  });

  it("cancels a finished job's unfinished work, and lets a later completion land", () => {
    let s = emptyState("s");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "tool_call.started", { turnId: "t1", toolCallId: "c1", kind: "agent", status: "in_progress", title: "spawn" }));
    s = applyEvent(s, ev(3, "tool_call.started", { turnId: "t1", toolCallId: "c2", kind: "agent", status: "in_progress", title: "spawn" }));
    s = applyEvent(s, ev(4, "job.started", { jobId: "j1", toolCallId: "c1", taskType: "subagent" }));
    s = applyEvent(s, ev(5, "job.started", { jobId: "j2", toolCallId: "c2", taskType: "subagent" }));
    s = applyEvent(s, ev(6, "tool_call.started", { turnId: "t1", toolCallId: "run1", parentToolCallId: "c1", kind: "execute", status: "in_progress", title: "run1" }));
    s = applyEvent(s, ev(7, "tool_call.started", { turnId: "t1", toolCallId: "wait1", parentToolCallId: "c1", kind: "execute", status: "pending", title: "wait1" }));
    s = applyEvent(s, ev(8, "tool_call.started", { turnId: "t1", toolCallId: "done1", parentToolCallId: "c1", kind: "execute", status: "in_progress", title: "done1" }));
    s = applyEvent(s, ev(9, "tool_call.started", { turnId: "t1", toolCallId: "other", parentToolCallId: "c2", kind: "execute", status: "in_progress", title: "other" }));
    // j1's child spawned a grandchild that outlives it.
    s = applyEvent(s, ev(10, "tool_call.started", { turnId: "t1", toolCallId: "spawn3", parentToolCallId: "c1", kind: "agent", status: "in_progress", title: "spawn" }));
    s = applyEvent(s, ev(11, "job.started", { jobId: "j3", toolCallId: "spawn3", parentJobId: "j1", taskType: "subagent" }));
    s = applyEvent(s, ev(12, "tool_call.updated", { toolCallId: "done1", status: "completed" }));
    s = applyEvent(s, ev(13, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    s = applyEvent(s, ev(14, "job.finished", { jobId: "j1", status: "stopped" }));
    const status = (id: string) => s.items.find((it) => it.id === id)?.status;
    expect({ c1: status("c1"), run1: status("run1"), wait1: status("wait1"), done1: status("done1"), other: status("other"), spawn3: status("spawn3") }).toEqual({
      c1: "cancelled",
      run1: "cancelled",
      wait1: "cancelled",
      done1: "completed",
      other: "in_progress",
      spawn3: "in_progress",
    });
    s = applyEvent(s, ev(15, "tool_call.updated", { toolCallId: "run1", parentToolCallId: "c1", status: "completed" }));
    expect(status("run1")).toBe("completed");
  });

  it("clears finishedAt when a finished job is reopened, and stamps it again on the next finish", () => {
    let s = emptyState("s");
    s = applyEvent(s, ev(1, "job.started", { jobId: "j1", taskType: "subagent" }));
    s = applyEvent(s, ev(2, "job.finished", { jobId: "j1", status: "completed" }));
    expect(s.jobs[0].finishedAt).toBe(2000);
    s = applyEvent(s, ev(3, "job.updated", { jobId: "j1", status: "running" }));
    expect(s.jobs[0].status).toBe("running");
    expect(s.jobs[0].finishedAt).toBeUndefined();
    // An update that sets no status leaves a finished job finished.
    s = applyEvent(s, ev(4, "job.finished", { jobId: "j1", status: "completed" }));
    s = applyEvent(s, ev(5, "job.updated", { jobId: "j1", activity: "done" }));
    expect(s.jobs[0]).toMatchObject({ status: "completed", finishedAt: 4000 });
  });

  it("classifies by task type and nests children", () => {
    let s = emptyState("s");
    s = applyEvent(s, ev(1, "job.started", { jobId: "a", taskType: "local_bash" }));
    s = applyEvent(s, ev(2, "job.started", { jobId: "b", parentJobId: "a", taskType: "whatever" }));
    expect(s.jobs[0].kind).toBe("shell");
    expect(s.jobs[1]).toMatchObject({ kind: "agent", depth: 1 });
  });
});

describe("queued prompts", () => {
  it("adds on prompt.queued and removes on prompt.dequeued, leaving the turn list alone", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "prompt.queued", { queueId: "q1", prompt: "then this" }, 2000));
    s = applyEvent(s, ev(3, "prompt.queued", { queueId: "q2", prompt: "and this" }));
    expect(s.queuedPrompts.map((q) => q.queueId)).toEqual(["q1", "q2"]);
    expect(s.queuedPrompts[0].queuedAt).toBe(2000);
    expect(s.turns).toHaveLength(1);
    expect(s.phase).toBe("turn");
    s = applyEvent(s, ev(4, "prompt.dequeued", { queueId: "q1", reason: "removed" }));
    expect(s.queuedPrompts.map((q) => q.queueId)).toEqual(["q2"]);
  });

  it("replaces an entry's text and pictures on prompt.edited, in place", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "prompt.queued", { queueId: "q1", prompt: "first", images: [{ id: "img" }] }, 2000));
    s = applyEvent(s, ev(2, "prompt.queued", { queueId: "q2", prompt: "second" }));
    s = applyEvent(s, ev(3, "prompt.edited", { queueId: "q1", prompt: "first, reworded" }));
    expect(s.queuedPrompts.map((q) => q.queueId)).toEqual(["q1", "q2"]);
    // No images on the edit means the entry has none now.
    expect(s.queuedPrompts[0]).toMatchObject({ prompt: "first, reworded", images: undefined, queuedAt: 2000 });
    s = applyEvent(s, ev(4, "prompt.edited", { queueId: "q2", prompt: "second", images: [{ id: "new" }] }));
    expect(s.queuedPrompts[1].images).toEqual([{ id: "new" }]);
  });

  it("ignores an edit to an entry it does not have", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "prompt.queued", { queueId: "q1", prompt: "first" }));
    const before = s.queuedPrompts;
    s = applyEvent(s, ev(2, "prompt.edited", { queueId: "nope", prompt: "x" }));
    expect(s.queuedPrompts).toEqual(before);
  });

  it("holds the named entries, taking them back from the harness, and releases them", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "prompt.queued", { queueId: "q1", prompt: "a" }));
    s = applyEvent(s, ev(2, "prompt.sent", { queueId: "q1" }));
    s = applyEvent(s, ev(3, "prompt.queued", { queueId: "q2", prompt: "b" }));
    s = applyEvent(s, ev(4, "prompt.queued", { queueId: "q3", prompt: "c", held: true }));
    expect(s.queuedPrompts[2].held).toBe(true);
    s = applyEvent(s, ev(5, "prompt.held", { queueIds: ["q1", "q2"] }));
    expect(s.queuedPrompts.map((q) => [q.held, !!q.sent])).toEqual([
      [true, false],
      [true, false],
      [true, false],
    ]);
    s = applyEvent(s, ev(6, "prompt.released", { queueIds: ["q1", "q3"] }));
    expect(s.queuedPrompts.map((q) => q.held)).toEqual([false, true, false]);
  });
});

describe("a held prompt the harness takes", () => {
  it("is sent and no longer held", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "prompt.queued", { queueId: "q1", prompt: "a", held: true }));
    s = applyEvent(s, ev(2, "prompt.sent", { queueId: "q1" }));
    expect(s.queuedPrompts[0]).toMatchObject({ sent: true, held: false });
  });
});

describe("a queued prompt starting", () => {
  it("leaves the queue when the turn it became names it", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "prompt.queued", { queueId: "q1", prompt: "next" }));
    s = applyEvent(s, ev(2, "turn.started", { turnId: "t2", prompt: "next", queueId: "q1" }));
    expect(s.queuedPrompts).toEqual([]);
    expect(s.turns.map((t) => t.id)).toEqual(["t2"]);
    expect(s.items.map((it) => it.id)).toEqual(["prompt:t2"]);
  });
});

describe("a sent prompt", () => {
  it("is marked sent only once the harness takes it, and a late prompt.sent changes nothing", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "prompt.queued", { queueId: "q1", prompt: "one" }));
    s = applyEvent(s, ev(3, "prompt.queued", { queueId: "q2", prompt: "two" }));
    expect(s.queuedPrompts.map((q) => !!q.sent)).toEqual([false, false]);
    s = applyEvent(s, ev(4, "prompt.sent", { queueId: "q2" }));
    expect(s.queuedPrompts.map((q) => !!q.sent)).toEqual([false, true]);
    s = applyEvent(s, ev(5, "prompt.injected", { queueId: "q1", turnId: "t1" }));
    s = applyEvent(s, ev(6, "prompt.sent", { queueId: "q1" }));
    expect(s.queuedPrompts.map((q) => q.queueId)).toEqual(["q2"]);
  });

  it("joins the running turn as a user message when the harness reads it", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "tool_call.started", { turnId: "t1", toolCallId: "c1", title: "ls" }));
    s = applyEvent(s, ev(3, "prompt.queued", { queueId: "q1", prompt: "also this", images: [{ id: "img" }] }));
    s = applyEvent(s, ev(4, "prompt.sent", { queueId: "q1" }));
    expect(s.queuedPrompts[0].sent).toBe(true);
    s = applyEvent(s, ev(5, "prompt.injected", { queueId: "q1", turnId: "t1" }, 4000));
    expect(s.queuedPrompts).toEqual([]);
    const last = s.items[s.items.length - 1];
    expect(last).toMatchObject({ id: "prompt:q1", role: "user", text: "also this", turnId: "t1", receivedAt: 4000 });
    expect(last.images).toHaveLength(1);
    expect(s.turns).toHaveLength(1);
    expect(s.phase).toBe("turn");
    // Injecting something not in the queue is not an event.
    const before = s.items.length;
    s = applyEvent(s, ev(6, "prompt.injected", { queueId: "nope", turnId: "t1" }));
    expect(s.items).toHaveLength(before);
  });

  it("fills the turn the harness started from it with what the queue entry carried", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "prompt.queued", { queueId: "q1", prompt: "next", images: [{ id: "img" }] }));
    s = applyEvent(s, ev(2, "prompt.sent", { queueId: "q1" }));
    s = applyEvent(s, ev(3, "turn.started", { turnId: "t2", queueId: "q1" }));
    expect(s.queuedPrompts).toEqual([]);
    expect(s.turns[0]).toMatchObject({ id: "t2", prompt: "next" });
    expect(s.turns[0].images).toHaveLength(1);
    expect(s.title).toBe("next");
    expect(s.items.map((it) => it.id)).toEqual(["prompt:t2"]);
    expect(s.items[0].images).toHaveLength(1);
  });
});

describe("cards", () => {
  const card = { kind: "add_mcp_server", projectId: "p1", server: { name: "linear" } };
  const requested = (seq: number, requestId: string, over: Record<string, unknown> = {}) =>
    ev(seq, "elicitation.requested", { requestId, turnId: "t1", prompt: "Add linear", schema: {}, card, ...over }, 5000);

  it("adds a pending elicitation and a pending card item for the turn", () => {
    const s = applyEvent(emptyState("s1"), requested(1, "r1"));
    expect(s.pendingElicitations).toEqual([{ requestId: "r1", turnId: "t1", prompt: "Add linear", schema: {}, card }]);
    expect(s.items).toEqual([
      { id: "card:r1", kind: "card", turnId: "t1", card, status: "pending", receivedAt: 5000 },
    ]);
  });

  it("adds no item for an ordinary question", () => {
    const s = applyEvent(emptyState("s1"), ev(1, "elicitation.requested", { requestId: "q1", prompt: "Which?", schema: {} }));
    expect(s.pendingElicitations).toHaveLength(1);
    expect(s.items).toHaveLength(0);
  });

  it("settles the card item with the outcome and drops the pending request", () => {
    let s = applyEvent(emptyState("s1"), requested(1, "r1"));
    s = applyEvent(s, requested(2, "r2"));
    const outcome = { result: "saved", summary: "Saved linear for this project", needsSignIn: true };
    s = applyEvent(s, ev(3, "elicitation.resolved", { requestId: "r1", action: "accept", value: outcome }));

    expect(s.pendingElicitations.map((e) => e.requestId)).toEqual(["r2"]);
    const item = s.items.find((it) => it.id === "card:r1")!;
    expect(item.status).toBe("saved");
    expect(item.outcome).toEqual(outcome);
    expect(s.items.find((it) => it.id === "card:r2")!.status).toBe("pending");
  });

  it.each([
    ["accept", "saved"],
    ["decline", "declined"],
    ["cancel", "cancelled"],
  ])("falls back on the action when the resolution carries no outcome: %s is %s", (action, status) => {
    let s = applyEvent(emptyState("s1"), requested(1, "r1"));
    s = applyEvent(s, ev(2, "elicitation.resolved", { requestId: "r1", action }));
    expect(s.items[0].status).toBe(status);
    expect(s.items[0].outcome).toBeUndefined();
  });

  it("does not append a card item this window never held", () => {
    let s = applyEvent(emptyState("s1"), ev(1, "turn.started", { turnId: "t2", prompt: "go" }));
    s = applyEvent(s, ev(2, "elicitation.resolved", { requestId: "r-old", action: "accept", value: { result: "saved" } }));
    expect(s.items.map((it) => it.id)).not.toContain("card:r-old");
  });

  it("leaves a pending card alone when its turn stops", () => {
    let s = applyEvent(emptyState("s1"), ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, requested(2, "r1"));
    s = applyEvent(s, ev(3, "turn.finished", { turnId: "t1", stopReason: "cancelled" }));
    expect(s.items.find((it) => it.id === "card:r1")!.status).toBe("pending");
    expect(s.pendingElicitations).toHaveLength(1);
  });
});

// These mirror the plan.updated cases in internal/projection/state_test.go.
describe("plans", () => {
  const step = (text: string, status = "pending") => ({ text, status });
  const plans = (s: ReturnType<typeof emptyState>) => s.items.filter((it) => it.kind === "plan");

  it("supersedes a TodoWrite list in an older page read before a newer list landed", () => {
    const todo = (id: string, superseded?: boolean): Item => ({ id: `todo:${id}`, kind: "plan", steps: [], superseded });
    const page = [todo("a"), { id: "x", kind: "message" } as Item, todo("b")];
    const items = prependItems([todo("b"), todo("c")], page);
    expect(items.map((it) => [it.id, !!it.superseded])).toEqual([
      ["todo:a", true],
      ["x", false],
      ["todo:b", false],
      ["todo:c", false],
    ]);
    // A window with no TodoWrite list of its own leaves the page as it came.
    expect(prependItems([], [todo("a")])[0].superseded).toBeUndefined();
  });

  it("drops a plan in the old shape, which names no turn and no call", () => {
    const s = applyEvent(emptyState("s1"), ev(1, "plan.updated", { entries: [{ content: "a", status: "pending" }] }));
    expect(plans(s)).toEqual([]);
  });

  it("revises a codex plan in place within its turn, and starts a new one next turn", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "plan.updated", { turnId: "t1", steps: [step("a", "running"), step("b")] }, 100));
    s = applyEvent(s, ev(3, "tool_call.started", { turnId: "t1", toolCallId: "c1", kind: "execute", status: "in_progress" }));
    s = applyEvent(s, ev(4, "plan.updated", { turnId: "t1", steps: [step("a", "completed"), step("b", "running")] }, 200));
    expect(s.items.map((it) => it.id)).toEqual(["prompt:t1", "turn-plan:t1", "c1"]);
    const p = s.items[1];
    expect(p.kind).toBe("plan");
    expect(p.steps).toEqual([step("a", "completed"), step("b", "running")]);
    expect(p.receivedAt).toBe(100);

    s = applyEvent(s, ev(5, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    s = applyEvent(s, ev(6, "turn.started", { turnId: "t2", prompt: "again" }));
    s = applyEvent(s, ev(7, "plan.updated", { turnId: "t2", steps: [step("c")] }));
    expect(plans(s).map((it) => it.id)).toEqual(["turn-plan:t1", "turn-plan:t2"]);
    expect(plans(s)[0].steps).toEqual([step("a", "completed"), step("b", "running")]);
  });

  it("keeps a codex plan's explanation when a later update leaves it out", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "plan.updated", { turnId: "t1", explanation: "why", steps: [step("a")] }));
    expect(plans(s)[0].explanation).toBe("why");
    s = applyEvent(s, ev(2, "plan.updated", { turnId: "t1", steps: [step("a", "completed")] }));
    expect(plans(s)[0].explanation).toBe("why");
    s = applyEvent(s, ev(3, "plan.updated", { turnId: "t1", explanation: "why not", steps: [step("a", "completed")] }));
    expect(plans(s)[0].explanation).toBe("why not");
  });

  it("supersedes earlier TodoWrite lists when a new one is written, never codex plans", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "plan.updated", { turnId: "t1", toolCallId: "w1", steps: [step("a")] }));
    s = applyEvent(s, ev(2, "plan.updated", { turnId: "t1", steps: [step("x")] }));
    s = applyEvent(s, ev(3, "plan.updated", { turnId: "t1", toolCallId: "w2", steps: [step("a", "completed")] }));
    expect(plans(s).map((it) => [it.id, !!it.superseded])).toEqual([
      ["todo:w1", true],
      ["turn-plan:t1", false],
      ["todo:w2", false],
    ]);

    // Updating the latest list again supersedes nothing, itself included.
    s = applyEvent(s, ev(4, "plan.updated", { turnId: "t1", toolCallId: "w2", steps: [step("b")] }));
    expect(plans(s).map((it) => !!it.superseded)).toEqual([true, false, false]);
    expect(plans(s)[2].steps).toEqual([step("b")]);
  });

  it("drops a straggler for a plan windowed out with its finished turn", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "turn.finished", { turnId: "t1", stopReason: "end_turn" }));
    // A harness-initiated turn: nothing of it in the window yet.
    s = applyEvent(s, ev(3, "turn.started", { turnId: "t2" }));
    s = { ...s, items: [], itemsBefore: 1 };

    const late = applyEvent(s, ev(4, "plan.updated", { turnId: "t1", steps: [step("a")] }));
    expect(late.items).toEqual([]);
    expect(late.seq).toBe(4);

    // The running turn's first plan is new, not a straggler.
    const live = applyEvent(s, ev(4, "plan.updated", { turnId: "t2", steps: [step("a")] }));
    expect(live.items.map((it) => it.id)).toEqual(["turn-plan:t2"]);
  });
});

// Mirrors TestTurnFinishedSettlesItsRequests in internal/projection/state_test.go.
describe("turn.finished settles the turn's requests", () => {
  it("drops the finished turn's permission and question, keeping other turns', untagged ones and cards", () => {
    let s = applyEvent(emptyState("s1"), ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "permission.requested", { requestId: "p1", turnId: "t1", title: "rm -rf" }));
    s = applyEvent(s, ev(3, "permission.requested", { requestId: "p-other", turnId: "t0", title: "ls" }));
    s = applyEvent(s, ev(4, "permission.requested", { requestId: "p-untagged", title: "cat" }));
    s = applyEvent(s, ev(5, "elicitation.requested", { requestId: "q1", turnId: "t1", prompt: "which?", schema: {} }));
    s = applyEvent(
      s,
      ev(6, "elicitation.requested", { requestId: "card1", turnId: "t1", prompt: "", schema: {}, card: { kind: "mcp" } }),
    );

    s = applyEvent(s, ev(7, "turn.finished", { turnId: "t1", stopReason: "cancelled" }));

    expect(s.pendingPermissions.map((x) => x.requestId)).toEqual(["p-other", "p-untagged"]);
    expect(s.pendingElicitations.map((x) => x.requestId)).toEqual(["card1"]);
  });

  it("leaves every request alone when the finish names no turn", () => {
    let s = applyEvent(emptyState("s1"), ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "permission.requested", { requestId: "p-untagged", title: "cat" }));
    s = applyEvent(s, ev(3, "elicitation.requested", { requestId: "q-untagged", prompt: "which?", schema: {} }));

    s = applyEvent(s, ev(4, "turn.finished", { stopReason: "cancelled" }));

    expect(s.pendingPermissions.map((x) => x.requestId)).toEqual(["p-untagged"]);
    expect(s.pendingElicitations.map((x) => x.requestId)).toEqual(["q-untagged"]);
  });
});

// These mirror the tail tests in internal/projection/tail_test.go.
describe("a running call's output tail", () => {
  function lines(from: number, to: number): string {
    let s = "";
    for (let i = from; i <= to; i++) s += `line ${i}\n`;
    return s;
  }

  // One running Bash call, c1, in an open turn.
  function runningCall() {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "turn.started", { turnId: "t1", prompt: "go" }));
    s = applyEvent(s, ev(2, "tool_call.started", { turnId: "t1", toolCallId: "c1", kind: "execute", title: "make", status: "in_progress" }));
    return s;
  }

  const output = (seq: number, outputDelta: string) => ev(seq, "tool_call.updated", { toolCallId: "c1", outputDelta });
  const call = (s: ReturnType<typeof emptyState>) => s.items.find((it) => it.id === "c1")!;

  it("folds output deltas into a tail bounded to the last lines, leaving content alone", () => {
    let s = runningCall();
    s = applyEvent(s, output(3, "compiling a"));
    s = applyEvent(s, output(4, "\ncompiling b\n"));
    expect(call(s).tail).toBe("compiling a\ncompiling b\n");
    expect(call(s).content).toBeUndefined();

    for (let i = 1; i <= 100; i++) s = applyEvent(s, output(4 + i, `line ${i}\n`));
    expect(call(s).tail).toBe(lines(81, 100));
  });

  it("does not reopen an idle thread on output alone", () => {
    const s = applyEvent(emptyState("s1"), output(1, "late\n"));
    expect(s.phase).toBe("idle");
  });

  it("drops the tail on finishing, and output straggling in after it", () => {
    let s = runningCall();
    s = applyEvent(s, output(3, "partial\n"));
    s = applyEvent(s, ev(4, "tool_call.updated", { toolCallId: "c1", status: "completed", content: [{ type: "text", text: "partial\ndone\n" }] }));
    // Absent, not empty: the server's projection omits it.
    expect(call(s)).not.toHaveProperty("tail");
    expect(call(s).content).toEqual([{ type: "text", text: "partial\ndone\n" }]);

    s = applyEvent(s, output(5, "straggler\n"));
    expect(call(s)).not.toHaveProperty("tail");
    expect(call(s).status).toBe("completed");

    // A delta carried on the finishing update itself is dropped with the rest.
    let s2 = runningCall();
    s2 = applyEvent(s2, ev(3, "tool_call.updated", { toolCallId: "c1", status: "failed", outputDelta: "exit 1\n" }));
    expect(call(s2)).not.toHaveProperty("tail");
  });

  it("drops the tail of a call the turn's finish cancels or its job's finish settles", () => {
    let s = runningCall();
    s = applyEvent(s, output(3, "still going\n"));
    s = applyEvent(s, ev(4, "turn.finished", { turnId: "t1", stopReason: "cancelled" }));
    expect(call(s).status).toBe("cancelled");
    expect(call(s)).not.toHaveProperty("tail");

    let s2 = runningCall();
    s2 = applyEvent(s2, ev(3, "job.started", { jobId: "j1", toolCallId: "c1", taskType: "local_bash" }));
    s2 = applyEvent(s2, output(4, "serving\n"));
    s2 = applyEvent(s2, ev(5, "job.finished", { jobId: "j1", status: "failed" }));
    expect(call(s2).status).toBe("failed");
    expect(call(s2)).not.toHaveProperty("tail");
  });

  it("drops the tail at the turn's finish of a call its live job keeps running", () => {
    let s = runningCall();
    s = applyEvent(s, ev(3, "job.started", { jobId: "j1", toolCallId: "c1", taskType: "local_bash" }));
    s = applyEvent(s, output(4, "line 1\n"));
    s = applyEvent(s, ev(5, "turn.finished", { turnId: "t1", stopReason: "cancelled" }));
    expect(call(s).status).toBe("in_progress");
    expect(call(s)).not.toHaveProperty("tail");
  });
});

// Mirrors the cap in internal/projection: live events carry a tool's whole
// output, and the client keeps only what the server's projection keeps.
describe("applyEvent tool output cap", () => {
  const output = (seq: number, content: unknown[], id = "c1") =>
    ev(seq, "tool_call.updated", { toolCallId: id, status: "completed", content });

  it("keeps the head and tail of a huge output and marks the item truncated", () => {
    const huge = "h".repeat(TOOL_HEAD) + "m".repeat(50_000) + "t".repeat(TOOL_TAIL);
    const s = applyEvent(emptyState("s1"), output(1, [{ type: "text", text: huge }]));
    const it = s.items[0];
    expect(it.truncated).toBe(true);
    expect(it.content![0].text).toBe(
      "h".repeat(TOOL_HEAD) + "\n\n[… 50000 characters truncated …]\n\n" + "t".repeat(TOOL_TAIL),
    );
  });

  it("caps each side of a diff on its own", () => {
    const big = "x".repeat(20_000);
    const s = applyEvent(
      emptyState("s1"),
      output(1, [{ type: "diff", path: "a.go", oldText: big, newText: "small" }]),
    );
    const c = s.items[0].content![0];
    expect(c.oldText).toContain("characters truncated");
    expect(c.newText).toBe("small");
    expect(s.items[0].truncated).toBe(true);
  });

  it("leaves output under the cap alone", () => {
    const text = "y".repeat(TOOL_HEAD + TOOL_TAIL + 128);
    const s = applyEvent(emptyState("s1"), output(1, [{ type: "text", text }]));
    expect(s.items[0].content![0].text).toBe(text);
    expect(s.items[0].truncated).toBeUndefined();
  });

  it("caps an already capped output to itself", () => {
    const once = capToolText("z".repeat(100_000));
    expect(capToolText(once)).toBe(once);
    const s = applyEvent(emptyState("s1"), output(1, [{ type: "text", text: once }]));
    expect(s.items[0].content![0].text).toBe(once);
  });

  it("drops entries past the limit, across updates", () => {
    const entry = (n: number) => ({ type: "text", text: `entry ${n}` });
    let s = emptyState("s1");
    s = applyEvent(s, output(1, Array.from({ length: TOOL_MAX_ENTRIES - 2 }, (_, i) => entry(i))));
    expect(s.items[0].truncated).toBeUndefined();
    s = applyEvent(s, output(2, [entry(100), entry(101), entry(102), entry(103)]));
    const texts = s.items[0].content!.map((c) => c.text);
    expect(texts).toHaveLength(TOOL_MAX_ENTRIES);
    expect(texts.slice(-2)).toEqual(["entry 100", "entry 101"]);
    expect(s.items[0].truncated).toBe(true);
    // A later small update still lands nowhere, and the mark stays.
    s = applyEvent(s, output(3, [entry(200)]));
    expect(s.items[0].content).toHaveLength(TOOL_MAX_ENTRIES);
    expect(s.items[0].truncated).toBe(true);
  });

  it("drops text entries past the text budget, however they arrive, and keeps an image after them", () => {
    const entry = { type: "diff", path: "a.go", oldText: "o".repeat(3000), newText: "n".repeat(3000) };
    const image = { type: "image", url: "/api/threads/t/attachments/a.png" };
    const want = Math.ceil(TOOL_MAX_TEXT / 6000) + 1;
    const whole = applyEvent(emptyState("s1"), output(1, [...Array(10).fill(entry), image]));
    let split = applyEvent(emptyState("s1"), output(1, Array(2).fill(entry)));
    split = applyEvent(split, output(2, [...Array(8).fill(entry), image]));
    for (const s of [whole, split]) {
      expect(s.items[0].content).toHaveLength(want);
      expect(s.items[0].content?.at(-1)).toEqual(image);
      expect(s.items[0].truncated).toBe(true);
    }
  });

  it("does not split a surrogate pair at either cut", () => {
    const face = "\u{1F600}"; // two UTF-16 units
    // The head cut falls between the halves of the emoji at 4095..4096, and
    // the tail cut between the halves of the one ending the omitted middle.
    const text = "a".repeat(TOOL_HEAD - 1) + face + "m".repeat(20_000) + face + "b".repeat(TOOL_TAIL - 1);
    const capped = capToolText(text);
    const [head, tail] = capped.split(/\n\n\[… \d+ characters truncated …\]\n\n/);
    expect(head).toBe("a".repeat(TOOL_HEAD - 1));
    expect(tail).toBe("b".repeat(TOOL_TAIL - 1));
    // The emoji went whole into the omitted middle, and the count says so.
    expect(capped).toContain(`[… ${text.length - head.length - tail.length} characters truncated …]`);
  });

  it("cuts between lines when a break is near", () => {
    const text = Array.from({ length: 5000 }, (_, i) => `${i + 1}\n`).join("");
    const [head, tail] = capToolText(text).split(/\n\n\[… \d+ characters truncated …\]\n\n/);
    expect(head.endsWith("\n")).toBe(true);
    expect(text.startsWith(head)).toBe(true);
    expect(text.endsWith(tail)).toBe(true);
    expect(text[text.length - tail.length - 1]).toBe("\n");
  });
});

describe("a payload the server capped", () => {
  it("marks the item truncated from either tool event", () => {
    let s = applyEvent(emptyState("s1"), ev(1, "tool_call.started", { toolCallId: "c1", rawInput: { x: "…" }, truncated: true }));
    expect(s.items[0].truncated).toBe(true);
    s = applyEvent(emptyState("s1"), ev(1, "tool_call.started", { toolCallId: "c1", rawInput: {} }));
    expect(s.items[0].truncated).toBeUndefined();
    s = applyEvent(s, ev(2, "tool_call.updated", { toolCallId: "c1", status: "completed", truncated: true }));
    expect(s.items[0].truncated).toBe(true);
  });
});

describe("stragglers for what the snapshot left out", () => {
  const withJobs = (jobsBefore: number) => ({
    ...emptyState("s1"),
    jobs: [{ id: "j1", depth: 0, kind: "agent" as const, status: "running" as const, usage: {} }],
    jobsBefore,
  });

  it("drops an update or finish for a job it does not hold, moving only the cursor", () => {
    let s = applyEvent(withJobs(4), ev(5, "job.updated", { jobId: "old", activity: "Read" }));
    expect(s.jobs.map((j) => j.id)).toEqual(["j1"]);
    expect(s.seq).toBe(5);
    s = applyEvent(s, ev(6, "job.finished", { jobId: "old", status: "completed" }));
    expect(s.jobs.map((j) => j.id)).toEqual(["j1"]);
    expect(s.seq).toBe(6);
    // A held job still folds.
    s = applyEvent(s, ev(7, "job.updated", { jobId: "j1", activity: "Grep" }));
    expect(s.jobs[0].activity).toBe("Grep");
  });

  it("always creates a started job", () => {
    const s = applyEvent(withJobs(4), ev(5, "job.started", { jobId: "new", name: "Explore" }));
    expect(s.jobs.map((j) => j.id)).toEqual(["j1", "new"]);
  });

  it("creates a job from a later row when the list is complete", () => {
    const s = applyEvent(withJobs(0), ev(5, "job.updated", { jobId: "late" }));
    expect(s.jobs.map((j) => j.id)).toEqual(["j1", "late"]);
  });

  it("drops an update for an unknown child of a parent whose children were left out", () => {
    const base = {
      ...emptyState("s1"),
      items: [{ id: "task", kind: "tool" as const, childrenBefore: 12 }],
    };
    let s = applyEvent(base, ev(5, "tool_call.updated", { toolCallId: "child", parentToolCallId: "task", status: "completed" }));
    expect(s.items.map((it) => it.id)).toEqual(["task"]);
    expect(s.seq).toBe(5);
    // A child it does hold still updates, and a parent with nothing left out
    // takes new children.
    s = { ...s, items: [...s.items, { id: "held", kind: "tool", parentId: "task" }, { id: "other", kind: "tool" }] };
    s = applyEvent(s, ev(6, "tool_call.updated", { toolCallId: "held", parentToolCallId: "task", status: "failed" }));
    expect(s.items.find((it) => it.id === "held")?.status).toBe("failed");
    s = applyEvent(s, ev(7, "tool_call.updated", { toolCallId: "fresh", parentToolCallId: "other", status: "in_progress" }));
    expect(s.items.map((it) => it.id)).toContain("fresh");
  });
});

// Mirrors internal/projection/state_test.go.
describe("extension notices", () => {
  it.each(["warning", "error"] as const)("keeps a %s as a standalone notice item", (level) => {
    const s = applyEvent(emptyState("s1"), ev(4, "extension.notified", { level, message: "lint failed" }, 5000));
    expect(s.items).toEqual([
      { id: "notify:4", kind: "notice", noticeKind: "notify", level, text: "lint failed", receivedAt: 5000 },
    ]);
  });

  it("leaves info to the toast", () => {
    const s = applyEvent(emptyState("s1"), ev(1, "extension.notified", { level: "info", message: "ready" }));
    expect(s.items).toEqual([]);
    expect(s.seq).toBe(1);
  });

  it("gives two notices with the same message two items", () => {
    let s = applyEvent(emptyState("s1"), ev(1, "extension.notified", { level: "error", message: "boom" }));
    s = applyEvent(s, ev(2, "extension.notified", { level: "error", message: "boom" }));
    expect(s.items.map((it) => it.id)).toEqual(["notify:1", "notify:2"]);
  });
});

describe("extension statuses", () => {
  const status = (seq: number, key: string, text?: string) =>
    ev(seq, "extension.status", text === undefined ? { key } : { key, text });

  it("appends new keys and updates a known one in place", () => {
    let s = emptyState("s1");
    s = applyEvent(s, status(1, "git", "main"));
    s = applyEvent(s, status(2, "tests", "3 failing"));
    s = applyEvent(s, status(3, "git", "main*"));
    expect(s.extensionStatuses).toEqual([
      { key: "git", text: "main*" },
      { key: "tests", text: "3 failing" },
    ]);
  });

  it.each([
    ["empty text", ""],
    ["no text", undefined],
  ])("clears a key on %s", (_, text) => {
    let s = emptyState("s1");
    s = applyEvent(s, status(1, "git", "main"));
    s = applyEvent(s, status(2, "tests", "ok"));
    s = applyEvent(s, status(3, "git", text));
    expect(s.extensionStatuses).toEqual([{ key: "tests", text: "ok" }]);
  });

  it("ignores clearing a key it never had", () => {
    let s = applyEvent(emptyState("s1"), status(1, "git", "main"));
    s = applyEvent(s, status(2, "tests", ""));
    expect(s.extensionStatuses).toEqual([{ key: "git", text: "main" }]);
  });

  it("clears every status when the thread closes", () => {
    let s = applyEvent(emptyState("s1"), status(1, "git", "main"));
    s = applyEvent(s, ev(2, "thread.closed", { reason: "done" }));
    expect(s.extensionStatuses).toEqual([]);
  });

  it("starts the list on a state cached without one", () => {
    const { extensionStatuses: _, ...older } = emptyState("s1");
    const s = applyEvent(older, status(1, "git", "main"));
    expect(s.extensionStatuses).toEqual([{ key: "git", text: "main" }]);
  });
});
