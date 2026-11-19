// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { render, viewport, wrap } from "~/test/harness";
import type { AgentApi } from "~/lib/agentApi";
import type { Agent, AgentRevision, CompactionDetail, CompactionRow } from "~/protocol";

import { AgentPage } from "./AgentPage";
import { CompactionTab } from "./CompactionTab";
import { LogTab } from "./LogTab";
import { PromptTab } from "./PromptTab";

const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn(), info: vi.fn(), warning: vi.fn() }));
vi.mock("~/lib/toast", () => ({ toast }));

afterEach(() => vi.unstubAllGlobals());

const agent = (over: Partial<Agent> = {}): Agent => ({
  id: "a1",
  projectId: "p1",
  name: "Gardener",
  harness: "claude",
  workspace: "local",
  threadId: "t1",
  promptRev: 2,
  compactRev: 1,
  livePromptRev: 2,
  epoch: 3,
  createdAt: 0,
  ...over,
});

const rev = (n: number, body: string, kind: "prompt" | "compact" = "prompt"): AgentRevision => ({
  id: n,
  kind,
  n,
  body,
  author: "user",
  createdAt: n * 1000,
});

const row = (epoch: number, over: Partial<CompactionRow> = {}): CompactionRow => ({
  id: 100 + epoch,
  threadId: "t1",
  epoch,
  trigger: epoch === 0 ? "start" : "auto",
  preTokens: 150_000,
  postTokens: 12_000,
  durationMs: 30_000,
  promptRev: 1,
  compactRev: 1,
  attachments: [],
  createdAt: epoch * 60_000,
  ...over,
});

function mockApi(over: Partial<AgentApi> = {}): AgentApi {
  return {
    revisions: vi.fn(async () => [rev(1, "old line\n"), rev(2, "new line\n")]),
    saveRevision: vi.fn(),
    save: vi.fn(async () => ({ agent: agent() })),
    preamble: vi.fn(async () => ""),
    log: vi.fn(async () => ({ compactions: [], archived: [] })),
    compaction: vi.fn(),
    ...over,
  } as unknown as AgentApi;
}

/** A promise the test settles, for a save still out on a slow link. */
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

const epochs = (container: HTMLElement) =>
  [...container.querySelectorAll("[data-epoch]")].map((el) => Number(el.getAttribute("data-epoch")));

const rowButton = (container: HTMLElement, epoch: number) =>
  container.querySelector(`[data-epoch="${epoch}"] > button`) as HTMLButtonElement;

describe("LogTab", () => {
  it("pages the newest compactions and keeps epoch 0 at the bottom", async () => {
    const rows = Array.from({ length: 12 }, (_, i) => row(i));
    const api = mockApi({ log: vi.fn(async () => ({ compactions: rows, archived: [] })) });
    const { container } = render(<LogTab agent={agent({ epoch: 11 })} api={api} />);

    await waitFor(() => expect(epochs(container)).toEqual([11, 10, 9, 8, 7, 6, 5, 4, 0]));
    fireEvent.click(screen.getByRole("button", { name: /show 3 earlier/i }));
    expect(epochs(container)).toEqual([11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0]);
  });

  it("loads a row's detail the first time it opens and reuses it after", async () => {
    const detail: CompactionDetail = { ...row(2), summary: "the summary body" };
    const compaction = vi.fn(async () => detail);
    const api = mockApi({ log: vi.fn(async () => ({ compactions: [row(0), row(2)], archived: [] })), compaction });
    const { container } = render(<LogTab agent={agent()} api={api} />);

    await waitFor(() => expect(rowButton(container, 2)).toBeTruthy());
    fireEvent.click(rowButton(container, 2));
    await screen.findByText("the summary body");
    fireEvent.click(rowButton(container, 2));
    expect(screen.queryByText("the summary body")).toBeNull();
    fireEvent.click(rowButton(container, 2));
    expect(screen.getByText("the summary body")).toBeTruthy();
    expect(compaction).toHaveBeenCalledTimes(1);
    expect(compaction).toHaveBeenCalledWith("a1", 102);
  });

  it("says which harness left a field out", async () => {
    const detail: CompactionDetail = { ...row(1, { preTokens: undefined, postTokens: undefined }) };
    const api = mockApi({
      log: vi.fn(async () => ({ compactions: [row(1, { preTokens: undefined, postTokens: undefined })], archived: [] })),
      compaction: vi.fn(async () => detail),
    });
    const { container } = render(<LogTab agent={agent({ harness: "codex" })} api={api} />);

    await waitFor(() => expect(rowButton(container, 1)).toBeTruthy());
    fireEvent.click(rowButton(container, 1));
    const summary = await screen.findByRole("region", { name: "Summary" });
    expect(within(summary).getByText(/not reported by codex/i)).toBeTruthy();

    const instructions = screen.getByRole("region", { name: "Instructions sent" });
    fireEvent.click(within(instructions).getByRole("button"));
    expect(within(instructions).getByText(/not reported by codex/i)).toBeTruthy();
    expect(screen.getByText(/token counts not reported by codex/i)).toBeTruthy();
  });

  it("opens the row focus names, paging back to it", async () => {
    const rows = Array.from({ length: 12 }, (_, i) => row(i));
    const compaction = vi.fn(async (_: string, id: number) => ({ ...rows[id - 100], summary: `summary ${id}` }));
    const api = mockApi({ log: vi.fn(async () => ({ compactions: rows, archived: [] })), compaction });
    const { container } = render(<LogTab agent={agent({ epoch: 11 })} api={api} focus={{ epoch: 2 }} />);

    await screen.findByText("summary 102");
    expect(epochs(container)).toContain(1);
    expect(rowButton(container, 2).getAttribute("aria-expanded")).toBe("true");
  });

  it("refetches the list when the log moves and reloads an open row that changed", async () => {
    let summaryChars = 0;
    const log = vi.fn(async () => ({ compactions: [row(0), row(2, { summaryChars })], archived: [] }));
    const compaction = vi.fn(async () => ({ ...row(2), summary: summaryChars ? "late summary" : undefined }));
    const api = mockApi({ log, compaction });
    const { container, rerender } = render(<LogTab agent={agent({ logVersion: 1 })} api={api} />);

    await waitFor(() => expect(rowButton(container, 2)).toBeTruthy());
    fireEvent.click(rowButton(container, 2));
    await waitFor(() => expect(compaction).toHaveBeenCalledTimes(1));
    expect(screen.queryByText("late summary")).toBeNull();

    // Claude's summary lands at the turn's end, after the row was logged.
    summaryChars = 12;
    rerender(wrap(<LogTab agent={agent({ logVersion: 2 })} api={api} />));
    await screen.findByText("late summary");
    expect(log).toHaveBeenCalledTimes(2);
    expect(compaction).toHaveBeenCalledTimes(2);
  });

  it("keeps an open row's body when the log moves but the row did not", async () => {
    const log = vi.fn(async () => ({ compactions: [row(0), row(2)], archived: [] }));
    const compaction = vi.fn(async () => ({ ...row(2), summary: "the summary body" }));
    const api = mockApi({ log, compaction });
    const { container, rerender } = render(<LogTab agent={agent({ logVersion: 1 })} api={api} />);

    await waitFor(() => expect(rowButton(container, 2)).toBeTruthy());
    fireEvent.click(rowButton(container, 2));
    await screen.findByText("the summary body");
    rerender(wrap(<LogTab agent={agent({ logVersion: 2 })} api={api} />));
    await waitFor(() => expect(log).toHaveBeenCalledTimes(2));
    expect(compaction).toHaveBeenCalledTimes(1);
  });

  it("opens an archived thread's row when the focus names that thread", async () => {
    const log = vi.fn(async (_: string, threadId?: string) => ({
      compactions: threadId === "old" ? [row(0, { threadId: "old" }), row(1, { id: 900, threadId: "old" })] : [row(0), row(1)],
      archived: [{ threadId: "old", title: "Before", createdAt: 0, closedAt: 1, compactions: 1 }],
    }));
    const compaction = vi.fn(async (_: string, id: number) => ({ ...row(1, { id }), summary: `summary ${id}` }));
    const api = mockApi({ log, compaction });
    render(<LogTab agent={agent()} api={api} focus={{ thread: "old", epoch: 1 }} />);

    await screen.findByText("summary 900");
    expect(log).toHaveBeenLastCalledWith("a1", "old");
    expect(compaction).toHaveBeenCalledWith("a1", 900);
    expect(compaction).not.toHaveBeenCalledWith("a1", 101);
  });

  it("reads a focus on the live thread as the live log", async () => {
    const log = vi.fn(async () => ({ compactions: [row(0), row(1)], archived: [] }));
    const compaction = vi.fn(async (_: string, id: number) => ({ ...row(1), summary: `summary ${id}` }));
    render(<LogTab agent={agent()} api={mockApi({ log, compaction })} focus={{ thread: "t1", epoch: 1 }} />);

    await screen.findByText("summary 101");
    expect(log).toHaveBeenCalledWith("a1", undefined);
  });

  it("reloads the log for an archived thread when one is picked", async () => {
    const log = vi.fn(async (_: string, threadId?: string) => ({
      threadId,
      compactions: [row(0, { threadId: threadId ?? "t1" })],
      archived: [{ threadId: "old", title: "Before", createdAt: 0, closedAt: 1, compactions: 4 }],
    }));
    const api = mockApi({ log });
    render(<LogTab agent={agent()} api={api} />);

    const archived = await screen.findByRole("region", { name: "Archived threads" });
    fireEvent.click(within(archived).getAllByRole("button")[0]);
    await waitFor(() => expect(log).toHaveBeenLastCalledWith("a1", "old"));
  });

  it("turns raw request logging on for Claude, and offers no switch that does nothing elsewhere", async () => {
    const api = mockApi();
    const { unmount } = render(<LogTab agent={agent()} api={api} />);
    const sw = within(await screen.findByRole("region", { name: "Log raw request bodies" })).getByRole("switch");
    fireEvent.click(sw);
    await waitFor(() => expect(api.save).toHaveBeenCalledWith("a1", { rawLog: true }));
    unmount();

    for (const harness of ["codex", "pi"] as const) {
      const { unmount } = render(<LogTab agent={agent({ harness })} api={mockApi()} />);
      const region = await screen.findByRole("region", { name: "Log raw request bodies" });
      expect(within(region).getByRole("switch").hasAttribute("disabled")).toBe(true);
      unmount();
    }
  });
});

describe("PromptTab", () => {
  it("reverts by writing the old body as a new revision", async () => {
    const saveRevision = vi.fn(async () => ({ revision: rev(3, "old line\n"), restart: "now" as const }));
    const api = mockApi({ saveRevision });
    render(<PromptTab agent={agent()} api={api} />);

    const history = await screen.findByRole("region", { name: "History" });
    fireEvent.click(within(history).getByText("r1").closest("button")!);
    fireEvent.click(screen.getByRole("button", { name: "Revert to r1" }));
    fireEvent.click(await screen.findByRole("button", { name: "Revert" }));

    await waitFor(() => expect(saveRevision).toHaveBeenCalledWith("a1", "prompt", "old line\n", "Revert to r1", 2));
  });

  async function typeInto(text: string) {
    const box = await screen.findByRole("textbox", { name: "System prompt" });
    await waitFor(() => expect((box as HTMLTextAreaElement).value).toBe("new line\n"));
    fireEvent.change(box, { target: { value: text } });
    return box as HTMLTextAreaElement;
  }

  it("saves on the revision the edit started from, and says when another landed under it", async () => {
    const saveRevision = vi.fn(async () => {
      throw new Error("The prompt changed while you were editing: r3 is the latest. Review it, then save again.");
    });
    let list = [rev(1, "old line\n"), rev(2, "new line\n")];
    const api = mockApi({ saveRevision, revisions: vi.fn(async () => list) });
    const { rerender } = render(<PromptTab agent={agent()} api={api} />);
    const box = await typeInto("mine\n");
    expect(screen.queryByRole("alert")).toBeNull();

    // An agent's approved edit lands as r3 while the draft is open.
    list = [...list, rev(3, "theirs\n")];
    rerender(wrap(<PromptTab agent={agent({ promptRev: 3 })} api={api} />));
    const notice = await screen.findByRole("alert");
    expect(notice.textContent).toContain("r3");
    expect(box.value).toBe("mine\n");

    fireEvent.click(screen.getByRole("button", { name: /save as r4/i }));
    await waitFor(() => expect(saveRevision).toHaveBeenCalledWith("a1", "prompt", "mine\n", "", 2));
    expect(await screen.findByText(/r3 is the latest/)).toBeTruthy();

    fireEvent.click(within(notice).getByRole("button", { name: "View r3" }));
    expect(screen.getByRole("button", { name: "Editor" })).toBeTruthy();
  });

  it("drops a stale-revision refusal once the edit is kept over the new head", async () => {
    const saveRevision = vi.fn(async () => {
      throw new Error("The prompt changed while you were editing: r3 is the latest. Review it, then save again.");
    });
    let list = [rev(1, "old line\n"), rev(2, "new line\n")];
    const api = mockApi({ saveRevision, revisions: vi.fn(async () => list) });
    const { rerender } = render(<PromptTab agent={agent()} api={api} />);
    await typeInto("mine\n");
    list = [...list, rev(3, "theirs\n")];
    rerender(wrap(<PromptTab agent={agent({ promptRev: 3 })} api={api} />));
    const notice = await screen.findByRole("alert");
    fireEvent.click(screen.getByRole("button", { name: /save as r4/i }));
    expect(await screen.findByText(/r3 is the latest/)).toBeTruthy();

    fireEvent.click(within(notice).getByRole("button", { name: "Keep my edit" }));
    expect(screen.queryByText(/r3 is the latest/)).toBeNull();
  });

  it("saves on the new head once the edit is kept over it", async () => {
    const saveRevision = vi.fn(async () => ({ revision: rev(4, "mine\n"), restart: "now" as const }));
    let list = [rev(1, "old line\n"), rev(2, "new line\n")];
    const api = mockApi({ saveRevision, revisions: vi.fn(async () => list) });
    const { rerender } = render(<PromptTab agent={agent()} api={api} />);
    await typeInto("mine\n");
    list = [...list, rev(3, "theirs\n")];
    rerender(wrap(<PromptTab agent={agent({ promptRev: 3 })} api={api} />));

    fireEvent.click(within(await screen.findByRole("alert")).getByRole("button", { name: "Keep my edit" }));
    expect(screen.queryByRole("alert")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /save as r4/i }));
    await waitFor(() => expect(saveRevision).toHaveBeenCalledWith("a1", "prompt", "mine\n", "", 3));
  });

  it("treats a save that did not reach the thread as saved, and says why", async () => {
    toast.success.mockClear();
    const saveRevision = vi.fn(async () => ({
      revision: rev(3, "edited\n"),
      restart: "none" as const,
      applyError: "restart failed",
    }));
    const api = mockApi({ saveRevision });
    render(<PromptTab agent={agent()} api={api} />);
    await typeInto("edited\n");
    fireEvent.click(screen.getByRole("button", { name: /save as r3/i }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(expect.any(String), { description: "restart failed" }));
    expect(toast.success).not.toHaveBeenCalled();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows the wait while the agent has a restart pending, and drops it when the restart is done", async () => {
    const saveRevision = vi.fn(async () => ({ revision: rev(3, "edited\n"), restart: "pending" as const }));
    const api = mockApi({ saveRevision });
    const { rerender } = render(<PromptTab agent={agent()} api={api} />);

    await typeInto("edited\n");
    expect(screen.queryByRole("status")).toBeNull();
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "tighter" } });
    fireEvent.click(screen.getByRole("button", { name: /save as r3/i }));
    await waitFor(() => expect(saveRevision).toHaveBeenCalledWith("a1", "prompt", "edited\n", "tighter", 2));

    rerender(wrap(<PromptTab agent={agent({ promptRev: 3, restartPending: true })} api={api} />));
    const status = await screen.findByRole("status");
    expect(status.textContent).toContain("r3");
    expect(status.textContent).toContain("r2");

    rerender(wrap(<PromptTab agent={agent({ promptRev: 3, livePromptRev: 3 })} api={api} />));
    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  });

  it("shows no wait when the live thread is behind but no restart is pending", async () => {
    render(<PromptTab agent={agent({ livePromptRev: 1 })} api={mockApi()} />);
    await screen.findByRole("region", { name: "History" });
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("sends one save for a second Enter while the first is still out", async () => {
    const out = deferred<{ revision: AgentRevision; restart: "now" }>();
    const saveRevision = vi.fn(() => out.promise);
    render(<PromptTab agent={agent()} api={mockApi({ saveRevision })} />);
    await typeInto("edited\n");
    const reason = screen.getByRole("textbox", { name: "Reason" });
    fireEvent.keyDown(reason, { key: "Enter" });
    fireEvent.keyDown(reason, { key: "Enter" });
    expect(saveRevision).toHaveBeenCalledOnce();

    out.resolve({ revision: rev(3, "edited\n"), restart: "now" });
    await waitFor(() => expect(screen.queryByRole("textbox", { name: "Reason" })).toBeNull());
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps text typed while a save was out, as an edit over the revision it wrote", async () => {
    const out = deferred<{ revision: AgentRevision; restart: "now" }>();
    const saveRevision = vi.fn((): Promise<{ revision: AgentRevision; restart: "now" }> => out.promise);
    render(<PromptTab agent={agent()} api={mockApi({ saveRevision })} />);
    const box = await typeInto("mine\n");
    fireEvent.click(screen.getByRole("button", { name: /save as r3/i }));
    fireEvent.change(box, { target: { value: "mine, more\n" } });

    out.resolve({ revision: rev(3, "mine\n"), restart: "now" });
    const again = await screen.findByRole("button", { name: /save as r4/i });
    expect(box.value).toBe("mine, more\n");
    expect(screen.queryByRole("alert")).toBeNull();

    saveRevision.mockImplementation(async () => ({ revision: rev(4, "mine, more\n"), restart: "now" as const }));
    fireEvent.click(again);
    await waitFor(() => expect(saveRevision).toHaveBeenLastCalledWith("a1", "prompt", "mine, more\n", "", 3));
  });

  it("keeps an unsaved edit and its reason across a trip to another tab", async () => {
    const api = mockApi();
    const page = (tab: "prompt" | "log" | "compact") => (
      <AgentPage
        agent={agent()}
        loaded
        tab={tab}
        api={api}
        projects={[]}
        harnesses={[]}
        sidebarOpen
        onShowSidebar={vi.fn()}
        onBack={vi.fn()}
        onOpenThread={vi.fn()}
        onTab={vi.fn()}
        onAction={vi.fn()}
      />
    );
    const { rerender } = render(page("prompt"));
    await typeInto("mine\n");
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "tighter" } });

    rerender(wrap(page("log")));
    expect(screen.queryByRole("textbox", { name: "System prompt" })).toBeNull();
    rerender(wrap(page("prompt")));
    const box = (await screen.findByRole("textbox", { name: "System prompt" })) as HTMLTextAreaElement;
    expect(box.value).toBe("mine\n");
    expect((screen.getByRole("textbox", { name: "Reason" }) as HTMLInputElement).value).toBe("tighter");

    // The Compaction tab has its own text, not the prompt's edit.
    rerender(wrap(page("compact")));
    const compact = (await screen.findByRole("textbox", { name: "Compaction instructions" })) as HTMLTextAreaElement;
    await waitFor(() => expect(compact.value).toBe("new line\n"));
  });

  it("says when the history failed to refresh, and retries it", async () => {
    let list = [rev(1, "old line\n"), rev(2, "new line\n")];
    let fail = false;
    const revisions = vi.fn(async () => {
      if (fail) throw new Error("connection lost");
      return list;
    });
    const api = mockApi({ revisions });
    const { rerender } = render(<PromptTab agent={agent()} api={api} />);
    await screen.findByRole("region", { name: "History" });

    fail = true;
    list = [...list, rev(3, "theirs\n")];
    rerender(wrap(<PromptTab agent={agent({ promptRev: 3 })} api={api} />));
    expect(await screen.findByText(/did not refresh.*connection lost/)).toBeTruthy();

    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.queryByText(/did not refresh/)).toBeNull());
    expect(within(screen.getByRole("region", { name: "History" })).getByText("r3")).toBeTruthy();
  });

  it("fetches the new head when a save is refused as stale, and offers to keep the edit over it", async () => {
    let list = [rev(1, "old line\n"), rev(2, "new line\n")];
    let fail = false;
    const revisions = vi.fn(async () => {
      if (fail) throw new Error("connection lost");
      return list;
    });
    const saveRevision = vi.fn(async (_a: string, _k: string, _b: string, _r: string, base: number) => {
      if (base < 3) throw new Error("The prompt changed while you were editing: r3 is the latest. Review it, then save again.");
      return { revision: rev(4, "mine\n"), restart: "now" as const };
    });
    const api = mockApi({ revisions, saveRevision });
    const { rerender } = render(<PromptTab agent={agent()} api={api} />);
    await screen.findByRole("region", { name: "History" });

    // r3 lands, but the refetch it causes fails on a dropped link.
    fail = true;
    list = [...list, rev(3, "theirs\n")];
    rerender(wrap(<PromptTab agent={agent({ promptRev: 3 })} api={api} />));
    await screen.findByText(/did not refresh/);
    fail = false;

    await typeInto("mine\n");
    fireEvent.click(screen.getByRole("button", { name: /save as r3/i }));
    expect(await screen.findByText(/r3 landed while you were editing/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Keep my edit" }));
    fireEvent.click(screen.getByRole("button", { name: /save as r4/i }));
    await waitFor(() => expect(saveRevision).toHaveBeenLastCalledWith("a1", "prompt", "mine\n", "", 3));
  });

  it.each([
    ["desktop", "split"],
    ["phone", "unified"],
  ] as const)("diffs two revisions %s as %s", async (size, layout) => {
    viewport(size);
    const { container } = render(<PromptTab agent={agent()} api={mockApi()} focus={{ rev: 2 }} />);
    await waitFor(() => expect(container.querySelector("[data-diff-layout]")).toBeTruthy());
    expect(container.querySelector("[data-diff-layout]")!.getAttribute("data-diff-layout")).toBe(layout);
  });
});

describe("CompactionTab instructions", () => {
  const compactApi = (restart: "none" | "now" | "pending") =>
    mockApi({
      revisions: vi.fn(async () => [rev(1, "keep it short\n", "compact")]),
      saveRevision: vi.fn(async () => ({ revision: rev(2, "shorter\n", "compact"), restart })),
    });

  async function saveEdit(harness: string, restart: "none" | "now" | "pending") {
    const api = compactApi(restart);
    render(<CompactionTab agent={agent({ harness })} api={api} />);
    const box = (await screen.findByRole("textbox", { name: "Compaction instructions" })) as HTMLTextAreaElement;
    await waitFor(() => expect(box.value).toBe("keep it short\n"));
    fireEvent.change(box, { target: { value: "shorter\n" } });
    return { api, save: screen.getByRole("button", { name: /save as c2/i }) };
  }

  it("offers a restart with the save only where the harness reads them at start", async () => {
    expect((await saveEdit("codex", "pending")).save.textContent).toMatch(/restart/i);
    cleanup();
    expect((await saveEdit("claude", "none")).save.textContent).not.toMatch(/restart/i);
  });

  it("saves on the head it started from and reports the restart the save caused", async () => {
    toast.success.mockClear();
    const { api, save } = await saveEdit("codex", "now");
    fireEvent.click(save);
    await waitFor(() => expect(api.saveRevision).toHaveBeenCalledWith("a1", "compact", "shorter\n", "", 1));
    await waitFor(() => expect(toast.success).toHaveBeenCalledOnce());
    expect(toast.success.mock.calls[0][0]).toMatch(/restarted/);
  });

  it("does not claim a restart the save did not cause", async () => {
    toast.success.mockClear();
    const { save } = await saveEdit("claude", "none");
    fireEvent.click(save);
    await waitFor(() => expect(toast.success).toHaveBeenCalledOnce());
    expect(toast.success.mock.calls[0][0]).not.toMatch(/restart(ed|s) on/);
  });
});

describe("CompactionTab compact-at", () => {
  it("saves a one-tap threshold", async () => {
    const api = mockApi({ revisions: vi.fn(async () => [rev(1, "x\n", "compact")]) });
    render(<CompactionTab agent={agent()} api={api} />);
    fireEvent.click(await screen.findByRole("radio", { name: "120k" }));
    await waitFor(() => expect(api.save).toHaveBeenCalledWith("a1", { compactAt: 120_000 }));
  });

  it("sends 0 for the harness default", async () => {
    const api = mockApi({ revisions: vi.fn(async () => [rev(1, "x\n", "compact")]) });
    render(<CompactionTab agent={agent({ compactAt: 120_000 })} api={api} />);
    fireEvent.click(await screen.findByRole("radio", { name: "Default" }));
    await waitFor(() => expect(api.save).toHaveBeenCalledWith("a1", { compactAt: 0 }));
  });
});
