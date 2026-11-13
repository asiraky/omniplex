// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { describe, expect, it, vi } from "vitest";

import type { Item } from "~/protocol";
import { render } from "~/test/harness";

import { ApprovalChip, ApprovalRecord } from "./ApprovalChip";
import { PromptFocusContext } from "./promptFocus";

function withFocus(ui: ReactElement, focusPrompt = vi.fn()) {
  render(<PromptFocusContext.Provider value={focusPrompt}>{ui}</PromptFocusContext.Provider>);
  return focusPrompt;
}

describe("the approval chip", () => {
  it("takes the reader to the permission prompt while pending", () => {
    const focusPrompt = withFocus(<ApprovalChip approval="pending" />);
    fireEvent.click(screen.getByRole("button", { name: "Needs approval" }));
    expect(focusPrompt).toHaveBeenCalledWith("permission");
  });

  it("is only a label once settled", () => {
    withFocus(<ApprovalChip approval="allowed" />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("is a plain label with no prompt to go to", () => {
    render(<ApprovalChip approval="pending" />);
    expect(screen.getByText("Needs approval")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
});

describe("a standalone approval record", () => {
  const item: Item = { id: "approval:r1", kind: "approval", title: "Write outside the workspace", approval: "pending" };

  it("names the request beside its chip", () => {
    const focusPrompt = withFocus(<ApprovalRecord item={item} />);
    expect(screen.getByText("Write outside the workspace")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Needs approval" }));
    expect(focusPrompt).toHaveBeenCalledWith("permission");
  });

  it("shows how it went once settled", () => {
    withFocus(<ApprovalRecord item={{ ...item, approval: "denied" }} />);
    expect(screen.getByText("Denied")).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
