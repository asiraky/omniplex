// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { emptyState } from "~/apply";
import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import type { Item, Job, ThreadState } from "~/protocol";
import { render } from "~/test/harness";

import { JobsCard } from "./JobsCard";

const spawn = (id: string, over: Partial<Item> = {}): Item => ({
  id,
  kind: "tool",
  toolKind: "agent",
  title: `spawn ${id}`,
  status: "in_progress",
  ...over,
});

const job = (over: Partial<Job> & { id: string }): Job => ({
  depth: 0,
  kind: "agent",
  status: "running",
  usage: {},
  ...over,
});

function mount(state: ThreadState, ui: React.ReactElement) {
  return render(<ThreadStoreContext.Provider value={createThreadStore(state)}>{ui}</ThreadStoreContext.Provider>);
}

describe("the agents card", () => {
  it("opens the panel on the agent tapped, or on the roster when it has no job yet", () => {
    const onOpen = vi.fn();
    const state: ThreadState = {
      ...emptyState("s1"),
      jobs: [job({ id: "j1", toolCallId: "a", name: "Explore" })],
    };
    mount(state, <JobsCard items={[spawn("a"), spawn("b", { status: "pending" })]} onOpen={onOpen} />);

    fireEvent.click(screen.getByRole("button", { name: /Explore, running/ }));
    expect(onOpen).toHaveBeenLastCalledWith("j1");

    fireEvent.click(screen.getByRole("button", { name: /spawn b, queued/ }));
    expect(onOpen).toHaveBeenLastCalledWith(undefined);
  });

  it("shows what a running agent is doing, and why a failed one failed", () => {
    const state: ThreadState = {
      ...emptyState("s1"),
      jobs: [
        job({ id: "j1", toolCallId: "a", name: "Explore", activity: "Reading rows.ts" }),
        job({ id: "j2", toolCallId: "b", name: "Fix", status: "failed", error: "rate limited" }),
      ],
    };
    mount(state, <JobsCard items={[spawn("a"), spawn("b")]} />);
    expect(screen.getByText("Reading rows.ts")).toBeTruthy();
    expect(screen.getByText("rate limited")).toBeTruthy();
    expect(screen.getByText("2 agents")).toBeTruthy();
  });

  it("as a recap, keeps each agent to one line", () => {
    const state: ThreadState = {
      ...emptyState("s1"),
      jobs: [job({ id: "j1", toolCallId: "a", name: "Explore", status: "completed", activity: "Reading rows.ts" })],
    };
    mount(state, <JobsCard items={[spawn("a", { status: "completed" })]} recap />);
    expect(screen.getByText("1 agent from this turn")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Explore, done/ })).toBeTruthy();
    expect(screen.queryByText("Reading rows.ts")).toBeNull();
  });

  it("ticks a running agent's time and freezes a finished one's", () => {
    vi.useFakeTimers();
    try {
      const now = Date.now();
      const state: ThreadState = {
        ...emptyState("s1"),
        jobs: [
          job({ id: "j1", toolCallId: "a", name: "Live", startedAt: now - 5_000 }),
          job({ id: "j2", toolCallId: "b", name: "Done", status: "completed", startedAt: now - 90_000, finishedAt: now - 30_000 }),
          // Settled but never stamped: no clock rather than one that runs forever.
          job({ id: "j3", toolCallId: "c", name: "Old", status: "completed", startedAt: now - 10_000 }),
        ],
      };
      mount(state, <JobsCard items={[spawn("a"), spawn("b"), spawn("c")]} />);
      const time = (name: RegExp) => screen.getByRole("button", { name }).textContent;
      expect(time(/Live/)).toContain("5s");
      expect(time(/Done/)).toContain("1m 0s");
      vi.advanceTimersByTime(3_000);
      expect(time(/Live/)).toContain("8s");
      expect(time(/Done/)).toContain("1m 0s");
      expect(time(/Old/)).not.toMatch(/\d+s/);
    } finally {
      vi.useRealTimers();
    }
  });
});
