// @vitest-environment jsdom
import { screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { Transcript } from "./Transcript";
import { render } from "~/test/harness";

// The failed-turn card is fetched on first use, often right after a
// reconnect. A fetch that fails on a bad link must leave the thread readable.
vi.mock("./transcript/InterruptedCard", () => {
  throw new Error("Failed to fetch dynamically imported module");
});

it("says to reload, and keeps the thread, when the card cannot be fetched", async () => {
  render(
    <Transcript
      state={{
        threadId: "a",
        seq: 1,
        cwd: "/tmp/repo",
        harness: "claude",
        model: "",
        mode: "default",
        effort: "",
        title: "Thread a",
        phase: "idle",
        closed: false,
        workspace: { phase: "ready", projectId: "p1", projectRoot: "/tmp/repo" },
        items: [{ id: "u1", kind: "message", role: "user", text: "go", turnId: "t1", receivedAt: 1 }],
        turns: [{ id: "t1", prompt: "go", done: true, stopReason: "error", failure: "limit" }],
        usage: {},
        pendingPermissions: [],
        pendingElicitations: [],
        queuedPrompts: [],
      } as any}
      onFinish={() => {}}
      onRetryProvision={() => {}}
      onCleanup={() => {}}
      onForceDelete={() => {}}
      onContinue={() => {}}
      onOpenDiff={() => {}}
    />,
  );
  expect(await screen.findByText("This turn stopped. Reload to see why.")).toBeTruthy();
  expect(screen.getByText("go")).toBeTruthy();
});
