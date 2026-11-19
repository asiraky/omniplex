import { describe, expect, it } from "vitest";

import { agentPagePath, parseAgentPage, type AgentPage } from "./agentRoutes";

function parse(url: string) {
  const u = new URL(url, "http://x");
  return parseAgentPage(u.pathname, u.search);
}

describe("agent page URLs", () => {
  it.each<AgentPage>([
    { kind: "new" },
    { kind: "new", projectId: "p 1" },
    { kind: "agent", agentId: "a1", tab: "prompt" },
    { kind: "agent", agentId: "a/b", tab: "log", focus: { epoch: 3 } },
    { kind: "agent", agentId: "a1", tab: "compact", focus: { rev: 4 } },
    { kind: "agent", agentId: "a1", tab: "settings" },
  ])("reads back what it writes: %o", (page) => {
    expect(parse(agentPagePath(page))).toEqual(page);
  });

  it("opens an agent on its first tab when the tab is missing or unknown", () => {
    expect(parse("/agents/a1")).toEqual({ kind: "agent", agentId: "a1", tab: "prompt" });
    expect(parse("/agents/a1/history")).toEqual({ kind: "agent", agentId: "a1", tab: "prompt" });
  });

  it("drops a focus that is not a whole number", () => {
    expect(parse("/agents/a1/log?epoch=x&rev=-1")).toEqual({ kind: "agent", agentId: "a1", tab: "log" });
  });

  it("is not an agent page anywhere else", () => {
    expect(parse("/")).toBeNull();
    expect(parse("/t/abc")).toBeNull();
    expect(parse("/agents")).toBeNull();
    expect(parse("/agents/")).toBeNull();
    expect(parse("/agents/a1/log/extra")).toBeNull();
  });
});
