// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ScheduledPrompts } from "./ScheduledPrompts";
import { render } from "~/test/harness";

const prompt = (over: Record<string, unknown> = {}): any => ({
  id: "s1",
  revision: 0,
  prompt: "check the deploy",
  dueAt: Date.now() + 3_600_000,
  timeZone: "Australia/Brisbane",
  model: "",
  mode: "",
  effort: "",
  status: "pending",
  ...over,
});

describe("the strip above the composer", () => {
  it("lists a message somebody scheduled", () => {
    render(
      <ScheduledPrompts
        schedules={[prompt()]}
        onEdit={() => {}}
        onAction={async () => {}}
      />,
    );
    expect(screen.getByText("check the deploy")).toBeTruthy();
  });

  // A resume belongs to the failure card in the transcript, which says when it
  // is coming and offers both overrides. Carrying it here as well was the same
  // fact in two boxes, on a screen that is short of room.
  it("leaves an armed resume to the card that owns it", () => {
    const { container } = render(
      <ScheduledPrompts
        schedules={[prompt({ kind: "resume", attempt: 1, resumeOf: "t1" })]}
        onEdit={() => {}}
        onAction={async () => {}}
      />,
    );
    expect(container.textContent).toBe("");
  });
});
