// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { render, viewport, wrap } from "~/test/harness";
import type { AgentApi } from "~/lib/agentApi";
import type { Agent, AgentRevision, CompactionDetail, CompactionRow } from "~/protocol";

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

    await waitFor(() => expect(saveRevision).toHaveBeenCalledWith("a1", "prompt", "old line\n", "Revert to r1"));
  });

  it("says the restart waits when the save comes back pending", async () => {
    const saveRevision = vi.fn(async () => ({ revision: rev(3, "edited\n"), restart: "pending" as const }));
    const api = mockApi({ saveRevision });
    render(<PromptTab agent={agent()} api={api} />);

    const box = await screen.findByRole("textbox", { name: "System prompt" });
    await waitFor(() => expect((box as HTMLTextAreaElement).value).toBe("new line\n"));
    expect(screen.queryByRole("status")).toBeNull();
    fireEvent.change(box, { target: { value: "edited\n" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "tighter" } });
    fireEvent.click(screen.getByRole("button", { name: /save as r3/i }));

    await waitFor(() => expect(saveRevision).toHaveBeenCalledWith("a1", "prompt", "edited\n", "tighter"));
    expect((await screen.findByRole("status")).textContent).toContain("r3");
  });

  it("shows the wait when the live thread runs behind the newest prompt", async () => {
    render(<PromptTab agent={agent({ livePromptRev: 1 })} api={mockApi()} />);
    await screen.findByRole("region", { name: "History" });
    expect(screen.getByRole("status").textContent).toContain("r1");
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

describe("CompactionTab compact-at", () => {
  async function custom(harness: string) {
    const api = mockApi({ revisions: vi.fn(async () => [rev(1, "keep it short\n", "compact")]) });
    render(<CompactionTab agent={agent({ harness })} api={api} />);
    fireEvent.click(await screen.findByRole("radio", { name: "Custom" }));
    const input = screen.getByRole("textbox", { name: "Compact at, thousands of tokens" });
    const set = screen.getByRole("button", { name: "Set" }) as HTMLButtonElement;
    return { api, input, set };
  }

  it("refuses Claude Code a threshold under its floor", async () => {
    const { api, input, set } = await custom("claude");
    fireEvent.change(input, { target: { value: "80" } });
    expect(set.disabled).toBe(true);
    expect(screen.getByRole("alert")).toBeTruthy();
    fireEvent.submit(input.closest("form")!);
    expect(api.save).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: "150k" } });
    expect(set.disabled).toBe(false);
    fireEvent.click(set);
    await waitFor(() => expect(api.save).toHaveBeenCalledWith("a1", { compactAt: 150_000 }));
  });

  it("lets another harness go under Claude Code's floor", async () => {
    const { api, input, set } = await custom("codex");
    fireEvent.change(input, { target: { value: "80" } });
    fireEvent.click(set);
    await waitFor(() => expect(api.save).toHaveBeenCalledWith("a1", { compactAt: 80_000 }));
  });

  it("rejects text that is not a number", async () => {
    const { input, set } = await custom("codex");
    fireEvent.change(input, { target: { value: "lots" } });
    expect(set.disabled).toBe(true);
    expect(screen.getByRole("alert")).toBeTruthy();
  });

  it("sends 0 for the harness default", async () => {
    const api = mockApi({ revisions: vi.fn(async () => [rev(1, "x\n", "compact")]) });
    render(<CompactionTab agent={agent({ compactAt: 120_000 })} api={api} />);
    fireEvent.click(await screen.findByRole("radio", { name: "Default" }));
    await waitFor(() => expect(api.save).toHaveBeenCalledWith("a1", { compactAt: 0 }));
  });
});
