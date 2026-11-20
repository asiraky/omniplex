// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { emptyState } from "~/apply";
import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import { OpenThreadContext } from "~/lib/threadMentions";
import type { Item } from "~/protocol";
import { render } from "~/test/harness";

import { UserMessage } from "./UserMessage";

const said = (text: string): Item => ({ id: "u1", kind: "message", role: "user", text, receivedAt: 1 });

function mount(text: string, open: ((id: string) => void) | null) {
  render(
    <ThreadStoreContext.Provider value={createThreadStore(emptyState("s1"))}>
      <OpenThreadContext.Provider value={open}>
        <UserMessage item={said(text)} threadId="s1" artefacts={[]} />
      </OpenThreadContext.Provider>
    </ThreadStoreContext.Provider>,
  );
}

describe("UserMessage thread links", () => {
  it("shows mentioned threads as pills that open them, not as raw links", () => {
    const open = vi.fn();
    mount("read these\n\n[@Plan \\[v2\\]](omniplex-thread://t1)", open);
    expect(screen.queryByText(/omniplex-thread/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Open thread Plan [v2]" }));
    expect(open).toHaveBeenCalledWith("t1");
  });

  it("shows a pill it cannot open where nothing opens threads", () => {
    mount("[@Plan](omniplex-thread://t1)", null);
    expect(screen.queryByRole("button", { name: /Open thread/ })).toBeNull();
    expect(screen.getByText("Plan")).toBeTruthy();
  });
});
