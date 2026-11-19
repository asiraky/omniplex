// @vitest-environment jsdom
import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Item, Turn } from "~/protocol";

import { limitHold, useTranscriptModel } from "./useTranscriptModel";

const limited: Turn = { id: "t1", done: true, failure: "limit", stopReason: "error", finishedAt: 100 } as Turn;
const account: Item = { id: "account:5", kind: "notice", noticeKind: "account", title: "Work", receivedAt: 200 };
const restarted: Item = { id: "restart:6", kind: "notice", noticeKind: "restart", text: "", receivedAt: 300 };

function model(items: Item[]) {
  const state = {
    phase: "idle" as const,
    closed: false,
    items,
    turns: [limited],
    pendingPermissions: [],
    pendingElicitations: [],
  };
  return renderHook(() => useTranscriptModel(state)).result.current;
}

describe("useTranscriptModel: restart notes", () => {
  it("keeps the account the thread switched to when it restarts after", () => {
    expect(model([account, restarted]).switchedTo).toBe("Work");
  });

  it("does not lift a usage limit's hold", () => {
    const { heldFor } = limitHold({ turns: [limited], items: [restarted] });
    expect(heldFor({ queueId: "q", prompt: "go", queuedAt: 50 })).toBe("send");
  });
});
