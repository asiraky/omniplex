// @vitest-environment jsdom
import { fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { emptyState } from "~/apply";
import type { Item, Job, ThreadState } from "~/protocol";
import { render } from "~/test/harness";

import { JobsSurface } from "./JobsSurface";

const agent: Job = {
  id: "j1",
  toolCallId: "task",
  depth: 0,
  kind: "agent",
  name: "Explore",
  status: "completed",
  usage: {},
};

const said = (id: string, text: string): Item => ({ id, kind: "message", role: "agent", parentId: "task", text });

function thread(over: Partial<ThreadState> = {}): ThreadState {
  return { ...emptyState("s1"), jobs: [agent], ...over };
}

function serve(...responses: (() => Response)[]) {
  const fetch = vi.fn(async (_input: RequestInfo | URL) => {
    const next = responses.shift();
    return next ? next() : new Response(JSON.stringify({ error: "no more responses" }), { status: 500 });
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

const children = (...items: Item[]) => () => new Response(JSON.stringify({ items }));

function openAgent(state: ThreadState) {
  render(<JobsSurface threadId="s1" state={state} command={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: /Explore/ }));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("an agent's drill-down", () => {
  it("fetches children the snapshot left out and lays the live ones over them", async () => {
    const fetch = serve(children(said("m1", "first"), said("m2", "fetched copy")));
    openAgent(
      thread({
        items: [{ id: "task", kind: "tool", childrenBefore: 1 }, said("m2", "live copy"), said("m3", "live extra")],
      }),
    );
    expect(await screen.findByText("first")).toBeTruthy();
    expect(String(fetch.mock.calls[0][0])).toBe("/api/threads/s1/items/task/children");
    const texts = ["first", "live copy", "live extra"].map((t) => screen.getByText(t));
    // In the fetched order, the thread's extras after.
    for (let i = 1; i < texts.length; i++) {
      expect(texts[i - 1].compareDocumentPosition(texts[i]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
    expect(screen.queryByText("fetched copy")).toBeNull();
  });

  it("fetches when the spawn call is outside the window", async () => {
    serve(children(said("m1", "from the server")));
    openAgent(thread());
    expect(await screen.findByText("from the server")).toBeTruthy();
  });

  it("does not fetch when the thread holds every child", () => {
    const fetch = serve();
    openAgent(thread({ items: [{ id: "task", kind: "tool" }, said("m1", "all here")] }));
    expect(screen.getByText("all here")).toBeTruthy();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("reports a failed fetch and fetches again on retry", async () => {
    serve(() => new Response(JSON.stringify({ error: "the disk is on fire" }), { status: 500 }), children(said("m1", "second time lucky")));
    openAgent(thread());
    expect(await screen.findByText("the disk is on fire")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("second time lucky")).toBeTruthy();
  });
});

describe("the roster with jobs left out", () => {
  it("loads them on request, and offers a retry when that fails", async () => {
    const loadAllJobs = vi.fn<() => Promise<ThreadState | null>>().mockResolvedValueOnce(null).mockResolvedValueOnce(thread());
    render(<JobsSurface threadId="s1" state={thread({ jobsBefore: 7 })} command={vi.fn()} loadAllJobs={loadAllJobs} />);

    fireEvent.click(screen.getByRole("button", { name: "Show earlier jobs" }));
    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    expect(loadAllJobs).toHaveBeenCalledTimes(2);
  });

  it("offers them even when nothing loaded is visible", () => {
    render(<JobsSurface threadId="s1" state={thread({ jobs: [], jobsBefore: 3 })} command={vi.fn()} loadAllJobs={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Show earlier jobs" })).toBeTruthy();
  });
});
