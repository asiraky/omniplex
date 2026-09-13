import { describe, expect, it } from "vitest";

import type { AuthFlowEvent } from "~/protocol";

import { answeredPrompt, applyAuthFlowEvent, emptyAuthFlowView } from "./authFlow";

const flowId = "flow-1";

function fold(...events: AuthFlowEvent[]) {
  return events.reduce(applyAuthFlowEvent, emptyAuthFlowView());
}

describe("applyAuthFlowEvent", () => {
  it("accumulates narration in order", () => {
    const view = fold(
      { flowId, event: { type: "info", message: "Starting" } },
      { flowId, event: { type: "auth_url", url: "https://example.com/auth" } },
    );
    expect(view.notices.map((n) => n.type)).toEqual(["info", "auth_url"]);
    expect(view.done).toBe(false);
    expect(view.error).toBeNull();
  });

  it("replaces consecutive progress lines instead of stacking them", () => {
    const view = fold(
      { flowId, event: { type: "info", message: "Starting" } },
      { flowId, event: { type: "progress", message: "Waiting…" } },
      { flowId, event: { type: "progress", message: "Still waiting…" } },
    );
    expect(view.notices).toHaveLength(2);
    expect(view.notices[1]).toEqual({ type: "progress", message: "Still waiting…" });
  });

  it("keeps a progress line that follows other narration", () => {
    const view = fold(
      { flowId, event: { type: "progress", message: "Waiting…" } },
      { flowId, event: { type: "info", message: "Code accepted" } },
      { flowId, event: { type: "progress", message: "Finishing…" } },
    );
    expect(view.notices).toHaveLength(3);
  });

  it("surfaces a prompt", () => {
    const view = fold({ flowId, prompt: { id: "key", message: "Paste your API key", secret: true } });
    expect(view.prompt?.id).toBe("key");
  });

  it("treats error as terminal and drops any pending prompt", () => {
    const view = fold(
      { flowId, prompt: { id: "key", message: "Paste your API key" } },
      { flowId, error: "expired" },
    );
    expect(view.error).toBe("expired");
    expect(view.done).toBe(true);
    expect(view.prompt).toBeNull();
  });

  it("treats done as terminal success", () => {
    const view = fold(
      { flowId, prompt: { id: "key", message: "Paste your API key" } },
      { flowId, done: true },
    );
    expect(view.done).toBe(true);
    expect(view.error).toBeNull();
    expect(view.prompt).toBeNull();
  });
});

describe("answeredPrompt", () => {
  it("clears the prompt after a respond", () => {
    const view = fold({ flowId, prompt: { id: "key", message: "Paste your API key" } });
    expect(answeredPrompt(view).prompt).toBeNull();
  });

  it("leaves a promptless view untouched", () => {
    const view = emptyAuthFlowView();
    expect(answeredPrompt(view)).toBe(view);
  });
});
