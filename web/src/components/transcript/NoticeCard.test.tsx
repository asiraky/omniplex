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

  it("says why a restart failed", () => {
    const { container } = render(<NoticeCard item={restart("no prompt_snapshot in the transcript")} />);
    expect(container.textContent).toBe("Restart failed: no prompt_snapshot in the transcript");
  });
});

describe("NoticeCard compaction", () => {
  const compaction = (over: Partial<Item> = {}) =>
    notice({ id: "compact:9", noticeKind: "compaction", trigger: "auto", ...over });
  const agent = { id: "ag1", name: "backlog" } as Agent;
  const withAgent = (item: Item, current?: Agent) => {
    const openAgent = vi.fn();
    render(
      <AgentNavContext.Provider value={{ current, openAgent, openThread: () => {} }}>
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

  it("has nothing to inspect on a thread no agent logs", () => {
    withAgent(compaction(), agent);
    expect(screen.queryByRole("button", { name: "Inspect" })).toBeNull();
  });
});
