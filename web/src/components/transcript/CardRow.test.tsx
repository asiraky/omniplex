// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";
import type { Card, CardOutcome, Item } from "~/protocol";

import { CardRow } from "./CardRow";

const server: Card = { kind: "add_mcp_server", projectId: "p1", server: { name: "linear", url: "https://x" } };
const signIn: Card = {
  kind: "add_sign_in",
  cli: { id: "gws", name: "Google Workspace", statusCommand: "s", signInCommand: "l", accounts: ["work", "home"] },
};

function row(card: Card, outcome?: CardOutcome) {
  const item: Item = { id: "card:r1", kind: "card", card, status: outcome?.result ?? "pending", outcome };
  const onSignIn = vi.fn();
  render(<CardRow item={item} onSignIn={onSignIn} />);
  return onSignIn;
}

const signInButtons = () => screen.queryAllByRole("button");

describe("a card's row in the transcript", () => {
  it("offers Sign in for a saved server that needs one, and opens it for that server", () => {
    const onSignIn = row(server, { result: "saved", server: { name: "linear", project: "p1" }, needsSignIn: true });
    expect(signInButtons()).toHaveLength(1);
    fireEvent.click(signInButtons()[0]);
    expect(onSignIn.mock.calls[0][0].begin).toEqual({
      mcpServer: "linear",
      mcpProject: "p1",
      origin: window.location.origin,
    });
  });

  it("offers nothing for a saved server that needs no sign-in", () => {
    row(server, { result: "saved", server: { name: "linear", project: "p1" } });
    expect(signInButtons()).toHaveLength(0);
  });

  it("offers one Sign in per account of a saved sign-in", () => {
    const onSignIn = row(signIn, { result: "saved", cli: { id: "gws", accounts: ["work", "home"] } });
    expect(signInButtons()).toHaveLength(2);
    fireEvent.click(signInButtons()[1]);
    expect(onSignIn.mock.calls[0][0].begin).toEqual({ cli: "gws", account: "home" });
  });

  it("offers nothing while the card waits", () => {
    row(server);
    expect(signInButtons()).toHaveLength(0);
  });

  it.each(["declined", "cancelled"] as const)("offers nothing for a %s card", (result) => {
    row(signIn, { result, server: { name: "linear" }, needsSignIn: true, cli: { id: "gws", accounts: ["work"] } });
    expect(signInButtons()).toHaveLength(0);
  });
});
