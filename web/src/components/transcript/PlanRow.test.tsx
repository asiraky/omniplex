// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { render } from "~/test/harness";
import type { Item, PlanStep } from "~/protocol";

import { PlanRow } from "./PlanRow";

const step = (text: string, status: PlanStep["status"]): PlanStep => ({ text, status });

describe("a plan's row in the transcript", () => {
  it("opens to its explanation and steps", () => {
    const item: Item = {
      id: "turn-plan:t1",
      kind: "plan",
      explanation: "Doing the thing",
      steps: [step("read the code", "completed"), step("write the fix", "running")],
    };
    render(<PlanRow item={item} />);
    const button = screen.getByRole("button");
    expect(button.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByText("read the code")).toBeNull();

    fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getByText("Doing the thing")).toBeTruthy();
    expect(screen.getByText("read the code")).toBeTruthy();
    expect(screen.getAllByRole("listitem")).toHaveLength(2);

    fireEvent.click(button);
    expect(screen.queryByText("read the code")).toBeNull();
  });
});
