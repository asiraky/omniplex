// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";

import { ToolCard } from "./ToolCards";

// The loader is its own chunk, which a dropped connection or a redeploy can
// leave unloadable. Apart from ToolCards.test.tsx because lazy() caches the
// import for the whole module.
vi.mock("./LoadFullOutput", () => {
  throw new Error("Failed to fetch dynamically imported module");
});

describe("a truncated tool card whose loader cannot load", () => {
  it("says so in the card instead of taking the transcript down", async () => {
    render(
      <ToolCard
        threadId="s1"
        item={{
          id: "c1",
          kind: "tool",
          title: "go test ./...",
          status: "completed",
          content: [{ type: "text", text: "head\n\n[… 9000 characters truncated …]\n\ntail" }],
          truncated: true,
        }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /go test/ }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/Reload the page/);
    expect(screen.getByText(/characters truncated/)).toBeTruthy();
  });
});
