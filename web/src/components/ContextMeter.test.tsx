// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { render } from "~/test/harness";
import type { Agent } from "~/protocol";

import { agentCompactAt, ContextMeter } from "./ContextMeter";

describe("ContextMeter", () => {
  it("does not present a zero context window as a real limit", () => {
    render(
      <ContextMeter
        model="gpt-5.6-sol"
        usage={{
          input: 72000,
          output: 500,
          cacheRead: 45000,
          cacheWrite: 0,
          cost: 0,
          contextUsed: 53000,
        }}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Context usage unavailable" }));

    expect(screen.getByText("53k")).toBeTruthy();
    expect(screen.getByText("—")).toBeTruthy();
    expect(screen.queryByText("53k / 0")).toBeNull();
    expect(screen.getByText("Context window unavailable.")).toBeTruthy();
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBeNull();
  });

  it("measures an agent thread against its compaction point, not the window", () => {
    render(
      <ContextMeter
        compactAt={120_000}
        usage={{
          input: 0,
          output: 0,
          cacheRead: 0,
          cacheWrite: 0,
          cost: 0,
          contextUsed: 60_000,
          contextWindow: 167_000,
          contextLimit: 200_000,
        }}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Context 50% used" }));
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBe("50");
    expect(screen.getAllByText("60k / 120k").length).toBeGreaterThan(0);
  });
});

describe("agentCompactAt", () => {
  const agent = (harness: string, compactAt?: number) => ({ harness, compactAt }) as Agent;

  it("holds a Claude agent to Claude Code's 100k floor", () => {
    expect(agentCompactAt(agent("claude", 60_000))).toBe(100_000);
    expect(agentCompactAt(agent("claude", 120_000))).toBe(120_000);
  });

  it("takes another harness's number as set", () => {
    expect(agentCompactAt(agent("codex", 60_000))).toBe(60_000);
  });

  it("leaves the harness default alone", () => {
    expect(agentCompactAt(agent("claude", 0))).toBeUndefined();
    expect(agentCompactAt(agent("claude"))).toBeUndefined();
    expect(agentCompactAt(undefined)).toBeUndefined();
  });
});
