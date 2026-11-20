import { describe, expect, it } from "vitest";

import type { ThreadMeta } from "~/protocol";

import { tabTitle } from "./useThreadTitle";

const meta = { id: "t1", title: "Fix the build" } as ThreadMeta;

describe("tabTitle", () => {
  it("names a page over whatever thread is still attached under it", () => {
    expect(tabTitle({ activeId: "t1", meta, title: "Fix the build", needsAttention: true, pageTitle: "Backlog" })).toEqual({
      title: "Backlog",
    });
  });

  it("names an agent's thread after the agent before its title lands", () => {
    expect(tabTitle({ activeId: "t1", meta: undefined, title: undefined, needsAttention: false, agentName: "Backlog" })).toEqual({
      title: "Backlog",
      needsAttention: false,
    });
  });

  it("names an ordinary thread after itself and carries whether it needs you", () => {
    expect(tabTitle({ activeId: "t1", meta, title: undefined, needsAttention: true })).toEqual({
      title: "Fix the build",
      needsAttention: true,
    });
  });

  it("leaves the tab alone with nothing attached and no page", () => {
    expect(tabTitle({ activeId: null, meta, title: "x", needsAttention: false })).toBeNull();
  });
});
