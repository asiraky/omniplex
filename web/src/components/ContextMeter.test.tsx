// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { render } from "~/test/harness";
import { ContextMeter } from "./ContextMeter";

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

    fireEvent.focus(screen.getByRole("button", { name: "Context usage unavailable" }));

    expect(screen.getByText("53k")).toBeTruthy();
    expect(screen.getByText("—")).toBeTruthy();
    expect(screen.queryByText("53k / 0")).toBeNull();
    expect(screen.getByText("Context window unavailable.")).toBeTruthy();
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBeNull();
  });
});
