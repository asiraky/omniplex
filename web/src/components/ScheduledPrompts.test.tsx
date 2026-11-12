// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ScheduledPrompts } from "./ScheduledPrompts";
import { render } from "~/test/harness";
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
  status: "pending",
  ...over,
});

describe("scheduled prompts", () => {
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
