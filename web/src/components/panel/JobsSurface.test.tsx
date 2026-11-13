// @vitest-environment jsdom
import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { emptyState } from "~/apply";
import type { Item, Job, ThreadState } from "~/protocol";
import { createThreadStore, ThreadStoreContext } from "~/app/threadStore";
import { render, wrap } from "~/test/harness";

import { JobsSurface, type JobFocus } from "./JobsSurface";

const agent: Job = {
  id: "j1",
  toolCallId: "task",
  depth: 0,
  kind: "agent",
  name: "Explore",
  status: "completed",
  usage: {},
};

const said = (id: string, text: string): Item => ({
  id,
  kind: "message",
  role: "agent",
  parentId: "task",
  text,
});

function thread(over: Partial<ThreadState> = {}): ThreadState {
  return { ...emptyState("s1"), jobs: [agent], ...over };
}

function serve(...responses: (() => Response)[]) {
  const fetch = vi.fn(async (_input: RequestInfo | URL) => {
    const next = responses.shift();
    return next
      ? next()
      : new Response(JSON.stringify({ error: "no more responses" }), {
          status: 500,
        });
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

const children =
  (...items: Item[]) =>
  () =>
    new Response(JSON.stringify({ items }));

// The surface selects its thread from the store the app attaches it into.
function mount(state: ThreadState, ui: React.ReactElement) {
  return render(
    <ThreadStoreContext.Provider value={createThreadStore(state)}>
      {ui}
    </ThreadStoreContext.Provider>,
  );
}

function openAgent(state: ThreadState) {
  mount(state, <JobsSurface threadId="s1" command={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: /Explore/ }));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("an agent's drill-down", () => {
  it("fetches children the snapshot left out and lays the live ones over them", async () => {
    const fetch = serve(
      children(said("m1", "first"), said("m2", "fetched copy")),
    );
    openAgent(
      thread({
        items: [
          { id: "task", kind: "tool", childrenBefore: 1 },
          said("m2", "live copy"),
          said("m3", "live extra"),
        ],
      }),
    );
    expect(await screen.findByText("first")).toBeTruthy();
    expect(String(fetch.mock.calls[0][0])).toBe(
      "/api/threads/s1/items/task/children",
    );
    const texts = ["first", "live copy", "live extra"].map((t) =>
      screen.getByText(t),
    );
    // In the fetched order, the thread's extras after.
    for (let i = 1; i < texts.length; i++) {
      expect(
        texts[i - 1].compareDocumentPosition(texts[i]) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
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
    openAgent(
      thread({ items: [{ id: "task", kind: "tool" }, said("m1", "all here")] }),
    );
    expect(screen.getByText("all here")).toBeTruthy();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("reports a failed fetch and fetches again on retry", async () => {
    serve(
      () =>
        new Response(JSON.stringify({ error: "the disk is on fire" }), {
          status: 500,
        }),
      children(said("m1", "second time lucky")),
    );
    openAgent(thread());
    expect(await screen.findByText("the disk is on fire")).toBeTruthy();
    // A failure is not a claim that the agent did nothing.
    expect(screen.queryByText(/Nothing recorded/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("second time lucky")).toBeTruthy();
  });
});

describe("the roster with jobs left out", () => {
  it("loads them on request, and offers a retry when that fails", async () => {
    const loadAllJobs = vi
      .fn<() => Promise<ThreadState | null>>()
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(thread());
    mount(
      thread({ jobsBefore: 7 }),
      <JobsSurface threadId="s1" command={vi.fn()} loadAllJobs={loadAllJobs} />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Show earlier jobs" }));
    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    expect(loadAllJobs).toHaveBeenCalledTimes(2);
  });

  it("offers them even when nothing loaded is visible", () => {
    mount(
      thread({ jobs: [], jobsBefore: 3 }),
      <JobsSurface threadId="s1" command={vi.fn()} loadAllJobs={vi.fn()} />,
    );
    expect(
      screen.getByRole("button", { name: "Show earlier jobs" }),
    ).toBeTruthy();
  });
});

describe("a job asked for from outside", () => {
  const planner: Job = {
    ...agent,
    id: "j2",
    toolCallId: "task2",
    name: "Plan",
  };
  const said2 = (id: string, text: string): Item => ({
    ...said(id, text),
    parentId: "task2",
  });
  // Every child in the thread, so neither pane fetches.
  const state = thread({
    jobs: [agent, planner],
    items: [
      { id: "task", kind: "tool" },
      said("m1", "explored"),
      { id: "task2", kind: "tool" },
      said2("m2", "planned"),
    ],
  });

  function mountFocused(focus?: JobFocus) {
    const store = createThreadStore(state);
    const ui = (f?: JobFocus) => (
      <ThreadStoreContext.Provider value={store}>
        <JobsSurface threadId="s1" command={vi.fn()} focus={f} />
      </ThreadStoreContext.Provider>
    );
    const { rerender } = render(ui(focus));
    return (f?: JobFocus) => rerender(wrap(ui(f)));
  }

  const back = () =>
    fireEvent.click(screen.getByRole("button", { name: "Back to jobs" }));

  it("opens on that job's pane", () => {
    mountFocused({ jobId: "j1", nonce: 1 });
    expect(screen.getByText("explored")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Back to jobs" })).toBeTruthy();
  });

  it("reopens the job for a new ask after the user backed out, but not for the same one", () => {
    const refocus = mountFocused({ jobId: "j1", nonce: 1 });
    back();
    expect(screen.queryByText("explored")).toBeNull();

    refocus({ jobId: "j1", nonce: 1 });
    expect(screen.queryByText("explored")).toBeNull();

    refocus({ jobId: "j1", nonce: 2 });
    expect(screen.getByText("explored")).toBeTruthy();
  });

  it("switches to a different job while one is open", () => {
    const refocus = mountFocused({ jobId: "j1", nonce: 1 });
    refocus({ jobId: "j2", nonce: 2 });
    expect(screen.getByText("planned")).toBeTruthy();
    expect(screen.queryByText("explored")).toBeNull();
  });

  it("goes back to the roster when asked with no job", () => {
    const refocus = mountFocused({ jobId: "j1", nonce: 1 });
    refocus({ jobId: null, nonce: 2 });
    expect(screen.queryByText("explored")).toBeNull();
    expect(screen.queryByRole("button", { name: "Back to jobs" })).toBeNull();
  });

  it("opens a job the list does not hold yet once it arrives", () => {
    const store = createThreadStore(
      thread({ jobs: [agent], items: state.items }),
    );
    render(
      <ThreadStoreContext.Provider value={store}>
        <JobsSurface
          threadId="s1"
          command={vi.fn()}
          focus={{ jobId: "j2", nonce: 1 }}
        />
      </ThreadStoreContext.Provider>,
    );
    expect(screen.getByRole("button", { name: /Explore/ })).toBeTruthy();

    act(() => store.set({ ...state }));
    expect(screen.getByText("planned")).toBeTruthy();
  });
});
