// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ScheduledPrompts } from "./ScheduledPrompts";
import { render, wrap } from "~/test/harness";
import type { ScheduledPrompt } from "~/protocol";

const schedule = (over: Partial<ScheduledPrompt>): ScheduledPrompt => ({
  id: "s1",
  revision: 1,
  prompt: "",
  dueAt: Date.now() + 60_000,
  timeZone: "UTC",
  model: "",
  mode: "default",
  effort: "",
  serviceTier: "",
  status: "pending",
  ...over,
});

describe("scheduled prompts", () => {
  afterEach(() => vi.useRealTimers());

  // The usage-limit card shows and cancels the continuation it armed.
  it("leaves out a limit continuation", () => {
    render(
      <ScheduledPrompts
        schedules={[
          schedule({ id: "human", prompt: "check the deploy" }),
          schedule({ id: "limit", prompt: "carry on", kind: "limit" }),
        ]}
        onEdit={() => {}}
        onAction={async () => {}}
      />,
    );
    expect(screen.getByText("check the deploy")).toBeTruthy();
    expect(screen.queryByText("carry on")).toBeNull();
  });

  it("counts down from the time of the render, not the last minute tick", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-11T10:00:00Z"));
    const props = { onEdit: () => {}, onAction: async () => {} };
    const { rerender } = render(<ScheduledPrompts schedules={[]} {...props} />);
    // 59 seconds after the tick, a schedule made for five minutes from now arrives.
    vi.setSystemTime(new Date("2026-11-11T10:00:59Z"));
    rerender(wrap(<ScheduledPrompts schedules={[schedule({ dueAt: Date.now() + 5 * 60_000 })]} {...props} />));
    expect(screen.getByText(/in 5 min/)).toBeTruthy();
  });

  it("shows nothing when the continuation is all there is", () => {
    const { container } = render(
      <ScheduledPrompts
        schedules={[schedule({ kind: "limit" })]}
        onEdit={() => {}}
        onAction={async () => {}}
      />,
    );
    expect(container.querySelector("[aria-label='Scheduled messages']")).toBeNull();
  });
});
