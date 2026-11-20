import { describe, expect, it } from "vitest";

import type { Agent, ThreadMeta } from "~/protocol";

import { currentAgent, followedThread } from "./useAgents";

const agent = (id: string, threadId?: string): Agent =>
  ({ id, name: id, projectId: "p", harness: "claude", workspace: "local", promptRev: 1, compactRev: 1, epoch: 0, createdAt: 0, threadId }) as Agent;

describe("followedThread", () => {
  it("moves to the agent's new thread when its old one was attached", () => {
    expect(followedThread([agent("a", "t1")], [agent("a", "t2")], "t1")).toBe("t2");
  });

  it("stays put when the attached thread is not that agent's", () => {
    expect(followedThread([agent("a", "t1")], [agent("a", "t2")], "t9")).toBeNull();
    expect(followedThread([agent("a", "t1")], [agent("a", "t2")], null)).toBeNull();
  });

  it("stays on the transcript when the agent is archived or loses its thread", () => {
    expect(followedThread([agent("a", "t1")], [], "t1")).toBeNull();
    expect(followedThread([agent("a", "t1")], [agent("a")], "t1")).toBeNull();
  });

  it("does nothing when nothing moved", () => {
    expect(followedThread([agent("a", "t1")], [agent("a", "t1")], "t1")).toBeNull();
  });
});

describe("currentAgent", () => {
  const meta = (agentId?: string) => ({ id: "t0", agentId }) as ThreadMeta;

  it("is the agent whose live thread is attached", () => {
    expect(currentAgent([agent("a", "t1"), agent("b", "t2")], "t2", undefined)?.id).toBe("b");
  });

  it("falls back to the agent the thread names, for one of its archived threads", () => {
    expect(currentAgent([agent("a", "t1")], "t0", meta("a"))?.id).toBe("a");
  });

  it("is none for a plain thread or nothing attached", () => {
    expect(currentAgent([agent("a", "t1")], "t0", meta())).toBeUndefined();
    expect(currentAgent([agent("a", "t1")], null, undefined)).toBeUndefined();
  });
});
