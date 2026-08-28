import { describe, expect, it } from "vitest";

import { applyEvent, emptyState } from "./apply";
import type { Event } from "./protocol";

function ev(seq: number, type: string, payload: Record<string, unknown>, timestamp = 0): Event {
  return { sessionId: "s1", seq, timestamp, type, payload } as Event;
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

  it("does not reopen a closed session on a stray chunk", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "session.closed", { reason: "closed" }));
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

// Mirrors internal/projection/state_test.go: "" is a level the user can
// choose — the harness's own default — so an event carrying it must win.
describe("applyEvent config changes", () => {
  it("lets a cleared effort clear the effort", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "session.config_changed", { effort: "high" }));
    expect(s.effort).toBe("high");

    s = applyEvent(s, ev(2, "session.config_changed", { effort: "" }));
    expect(s.effort).toBe("");
  });

  it("leaves effort alone when the event is about something else", () => {
    let s = emptyState("s1");
    s = applyEvent(s, ev(1, "session.config_changed", { effort: "high" }));
    s = applyEvent(s, ev(2, "session.config_changed", { model: "sonnet" }));
    expect(s.effort).toBe("high");
    expect(s.model).toBe("sonnet");
  });
});

describe("jobs", () => {
  const ev = (seq: number, type: string, payload: unknown, timestamp = seq * 1000) =>
    ({ seq, sessionId: "s", type, timestamp, payload }) as unknown as Event;

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
