// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AgentNavContext } from "~/lib/agentNav";
import type { Agent, Item } from "~/protocol";

import { NoticeCard } from "./NoticeCard";

afterEach(cleanup);

// The catalogue knows "opus" and "claude-opus-5" are one model.
const catalogue = (model: string, effort: string) =>
  `${model === "claude-opus-5" ? "opus" : model}${effort ? ` · ${effort}` : ""}`;

const notice = (over: Partial<Item>): Item => ({ id: "model:4", kind: "notice", noticeKind: "model", ...over });

describe("NoticeCard model switch", () => {
  it("names the settings the thread switched to", () => {
    const { container } = render(
      <NoticeCard
        item={notice({ model: "sonnet", effort: "high", from: { model: "opus", effort: "high" } })}
        describeSettings={catalogue}
      />,
    );
    expect(container.textContent).toBe("Switched to sonnet · high");
  });

  it("draws nothing when both sides name the same model under different ids", () => {
    const { container } = render(
      <NoticeCard
        item={notice({ model: "opus", effort: "high", from: { model: "claude-opus-5", effort: "high" } })}
        describeSettings={catalogue}
      />,
    );
    expect(container.textContent).toBe("");
  });

  it("draws nothing until the catalogue has loaded", () => {
    const { container } = render(
      <NoticeCard item={notice({ model: "opus", effort: "high", from: { model: "claude-opus-5", effort: "high" } })} />,
    );
    expect(container.textContent).toBe("");
  });

  it("still marks an effort change on the same model", () => {
    const { container } = render(
      <NoticeCard
        item={notice({ model: "opus", effort: "low", from: { model: "claude-opus-5", effort: "high" } })}
        describeSettings={catalogue}
      />,
    );
    expect(container.textContent).toBe("Switched to opus · low");
  });
});

describe("NoticeCard restart", () => {
  const restart = (text: string) => notice({ id: "restart:7", noticeKind: "restart", text });

  it("marks a restart that worked", () => {
    const { container } = render(<NoticeCard item={restart("")} />);
    expect(container.textContent).toBe("Thread restarted");
  });

  it("links an agent's restart to the prompt change it came up on", () => {
    const openAgent = vi.fn();
    render(
      <AgentNavContext.Provider value={{ current: { id: "ag1" } as Agent, openAgent, openThread: () => {} }}>
        <NoticeCard item={{ ...restart(""), promptRev: 8 }} />
      </AgentNavContext.Provider>,
    );
    expect(screen.getByText("r8")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "View change" }));
    expect(openAgent).toHaveBeenCalledWith("ag1", "prompt", { rev: 8 });
  });

  it("has no change to show for the first revision or off an agent's thread", () => {
    render(
      <AgentNavContext.Provider value={{ current: { id: "ag1" } as Agent, openAgent: vi.fn(), openThread: () => {} }}>
        <NoticeCard item={{ ...restart(""), promptRev: 1 }} />
      </AgentNavContext.Provider>,
    );
    render(<NoticeCard item={{ ...restart(""), promptRev: 5 }} />);
    expect(screen.queryByRole("button", { name: "View change" })).toBeNull();
  });

  it("says why a restart failed", () => {
    const { container } = render(<NoticeCard item={restart("no prompt_snapshot in the transcript")} />);
    expect(container.textContent).toBe("Restart failed: no prompt_snapshot in the transcript");
  });
});

describe("NoticeCard compaction", () => {
  const compaction = (over: Partial<Item> = {}) =>
    notice({ id: "compact:9", noticeKind: "compaction", trigger: "auto", ...over });
  const agent = { id: "ag1", name: "backlog" } as Agent;
  const withAgent = (item: Item, current?: Agent, threadId?: string) => {
    const openAgent = vi.fn();
    render(
      <AgentNavContext.Provider value={{ current, threadId, openAgent, openThread: () => {} }}>
        <NoticeCard item={item} />
      </AgentNavContext.Provider>,
    );
    return openAgent;
  };

  it("opens the agent's log at this compaction's epoch", () => {
    const openAgent = withAgent(compaction({ epoch: 46 }), agent);
    fireEvent.click(screen.getByRole("button", { name: "Inspect" }));
    expect(openAgent).toHaveBeenCalledWith("ag1", "log", { epoch: 46 });
  });

  it("opens the log of the thread the notice is in, an archived one included", () => {
    const openAgent = withAgent(compaction({ epoch: 2 }), { ...agent, threadId: "live" }, "old");
    fireEvent.click(screen.getByRole("button", { name: "Inspect" }));
    expect(openAgent).toHaveBeenCalledWith("ag1", "log", { thread: "old", epoch: 2 });
  });

  it("has nothing to inspect on a thread no agent logs", () => {
    withAgent(compaction(), agent);
    expect(screen.queryByRole("button", { name: "Inspect" })).toBeNull();
  });
});

describe("NoticeCard agent start", () => {
  const started = (over: Partial<Item> = {}) =>
    notice({ id: "agent:started", noticeKind: "started", promptRev: 3, compactRev: 2, ...over });
  const agent = { id: "ag1", name: "backlog" } as Agent;
  const withAgent = (item: Item, current?: Agent, threadId?: string) => {
    const openAgent = vi.fn();
    const { container } = render(
      <AgentNavContext.Provider value={{ current, threadId, openAgent, openThread: () => {} }}>
        <NoticeCard item={item} />
      </AgentNavContext.Provider>,
    );
    return { openAgent, container };
  };

  it("names the agent and the revisions its thread started on", () => {
    const { container } = withAgent(started(), agent);
    expect(container.textContent).toBe("backlog started, prompt r3, compaction c2Inspect");
  });

  it("names no compaction revision for a harness that never reads one", () => {
    const { container } = withAgent(started({ compactRev: undefined }), agent);
    expect(container.textContent).toBe("backlog started, prompt r3Inspect");
  });

  it("opens epoch 0 of the thread the notice is in", () => {
    const { openAgent } = withAgent(started(), { ...agent, threadId: "live" }, "old");
    fireEvent.click(screen.getByRole("button", { name: "Inspect" }));
    expect(openAgent).toHaveBeenCalledWith("ag1", "log", { thread: "old", epoch: 0 });
  });

  it("has nothing to inspect until the agent is known", () => {
    const { container } = withAgent(started());
    expect(container.textContent).toBe("Agent started, prompt r3, compaction c2");
    expect(screen.queryByRole("button", { name: "Inspect" })).toBeNull();
  });
});
