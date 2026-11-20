// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, type Mock } from "vitest";

import { render } from "~/test/harness";
import type { Card } from "~/protocol";

import type { ResolveCard } from "./CardFrame";
import PendingCard from "./PendingCard";

function show(before: string, after: string, resolve: Mock<ResolveCard> = vi.fn(async () => ({}))) {
  const card: Card = {
    kind: "prompt_edit",
    promptEdit: { agentId: "ag1", agentName: "backlog", kind: "prompt", fromRev: 7, before, after, reason: "Remember that." },
  };
  render(<PendingCard request={{ requestId: "r1", prompt: "Proposal", schema: {}, card }} resolve={resolve} />);
  return resolve;
}

function type(value: string) {
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByLabelText("Edited change"), { target: { value } });
}

const appended = ["# Rules\n- one\n", "# Rules\n- one\n\n- kill a silent worker"] as const;
const replaced = ["# Rules\n- one\n- two\n", "# Rules\n- uno\n- two\n"] as const;

describe("a prompt edit card's edit box", () => {
  it("cannot approve an append whose box is cleared", () => {
    const resolve = show(...appended);
    type("");
    const button = screen.getByRole("button", { name: "No change" });
    expect(button).toHaveProperty("disabled", true);
    fireEvent.click(button);
    expect(resolve).not.toHaveBeenCalled();
  });

  it("cannot approve a replace typed back to the current text", () => {
    const resolve = show(...replaced);
    type("- one");
    expect(screen.getByRole("button", { name: "No change" })).toHaveProperty("disabled", true);
    expect(resolve).not.toHaveBeenCalled();
  });

  it("approves a cleared replace as the replaced lines removed", async () => {
    const resolve = show(...replaced);
    type("");
    fireEvent.click(screen.getByRole("button", { name: "Approve edited" }));
    await waitFor(() => expect(resolve).toHaveBeenCalledWith("accept", { body: "# Rules\n- two\n" }));
  });

  it("approves again once a cleared append is typed into", async () => {
    const resolve = show(...appended);
    type("");
    fireEvent.change(screen.getByLabelText("Edited change"), { target: { value: "- kill a stuck worker" } });
    fireEvent.click(screen.getByRole("button", { name: "Approve edited" }));
    await waitFor(() =>
      expect(resolve).toHaveBeenCalledWith("accept", { body: "# Rules\n- one\n\n- kill a stuck worker" }),
    );
  });
});
