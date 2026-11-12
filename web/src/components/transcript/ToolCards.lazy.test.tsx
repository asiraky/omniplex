// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { render } from "~/test/harness";

import { ToolCard } from "./ToolCards";

// The diff renderer and the image viewer are their own chunks, which a dropped
// connection or a redeploy can leave unloadable. Apart from ToolCards.test.tsx
// because lazy() caches the import for the whole module.
vi.mock("./ToolDiff", () => {
  throw new Error("Failed to fetch dynamically imported module");
});
vi.mock("~/components/ImageViewer", () => {
  throw new Error("Failed to fetch dynamically imported module");
});

const SRC = "/api/threads/t1/attachments/a1";

describe("a tool card whose chunks cannot load", () => {
  it("shows the patch as text instead of taking the transcript down", async () => {
    render(
      <ToolCard
        threadId="t1"
        item={{
          id: "e1",
          kind: "tool",
          title: "Edit a.ts",
          status: "completed",
          content: [{ type: "diff", path: "a.ts", text: "@@ -1 +1 @@\n-old\n+new\n" }],
        }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Edit a\.ts/ }));
    await new Promise((r) => setTimeout(r, 1000));
    console.log("BODY", document.body.innerHTML.slice(-1500));
    expect(await screen.findByText(/-old\s+\+new/, undefined, { timeout: 3000 })).toBeTruthy();
  });

  it("offers the original image when the viewer will not open", async () => {
    render(
      <ToolCard
        threadId="t1"
        item={{
          id: "s1",
          kind: "tool",
          title: "browser_screenshot",
          status: "completed",
          content: [{ type: "image", url: SRC, mediaType: "image/png", width: 1280, height: 800 }],
        }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /full size/ }));
    const alert = await screen.findByRole("alert", undefined, { timeout: 3000 });
    expect(alert.querySelector("a")?.getAttribute("href")).toBe(SRC);
  });
});
