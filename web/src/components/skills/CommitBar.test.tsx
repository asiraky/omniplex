// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { CommitBar } from "./CommitBar";
import type { SkillsCommand, SkillsContext } from "./parts";
import { RECORD_FILE } from "~/lib/skillFlows";
import type { GitChange, GitStatus, SkillsList } from "~/lib/skills";
import { render, wrap } from "~/test/harness";

const change = (name: string, status: GitChange["status"] = "modified"): GitChange => ({ name, status, files: 1 });

const git = (changes: GitChange[]): GitStatus => ({ root: "/home/u/dotfiles", branch: "main", changes });

const LIST: SkillsList = { skills: [], subagents: [] };

type Handler = (args: Record<string, unknown>) => unknown;

function mockCommand(handlers: Record<string, Handler>) {
  return vi.fn(async (name: string, args: Record<string, unknown>) => {
    const handler = handlers[name];
    if (!handler) throw new Error(`unexpected command ${name}`);
    return handler(args);
  }) as unknown as SkillsCommand & ReturnType<typeof vi.fn>;
}

const calls = (command: ReturnType<typeof vi.fn>, name: string) =>
  command.mock.calls.filter(([n]) => n === name).map(([, args]) => args as Record<string, unknown>);

const SCOPE = { threadId: "t1" };

const context = (command: SkillsCommand, list: SkillsList | null = LIST): SkillsContext => ({
  command,
  scopeArgs: SCOPE,
  list,
  setup: undefined,
  projectAvailable: true,
  refresh: vi.fn(),
  upsert: vi.fn(),
  newSkill: vi.fn(),
});

const bar = () => screen.queryByRole("region", { name: "Uncommitted changes" });
const toggle = () => screen.getByRole("button", { expanded: false });
const message = () => screen.getByLabelText("Commit message") as HTMLInputElement;
/** Wait for the line that reports a commit: it carries the short sha. */
const committed = (sha: string) => waitFor(() => expect(bar()?.textContent).toContain(sha));

describe("CommitBar", () => {
  it("stays out of the way when the library is not in a git repo", async () => {
    const command = mockCommand({ skills_git_status: () => ({ git: null }) });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(calls(command, "skills_git_status")).toHaveLength(1));
    expect(bar()).toBeNull();
  });

  it("stays out of the way when there is nothing to commit", async () => {
    const command = mockCommand({ skills_git_status: () => ({ git: git([]) }) });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(calls(command, "skills_git_status")).toHaveLength(1));
    expect(bar()).toBeNull();
  });

  it("stays out of the way when the check fails", async () => {
    const command = mockCommand({
      skills_git_status: () => {
        throw new Error("unknown command");
      },
    });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(calls(command, "skills_git_status")).toHaveLength(1));
    expect(bar()).toBeNull();
  });

  it("waits for the list before asking, then asks again every time the list is replaced", async () => {
    let changes = [change("pdf", "added")];
    const command = mockCommand({ skills_git_status: () => ({ git: git(changes) }) });
    const view = render(<CommitBar ctx={context(command, null)} />);
    expect(calls(command, "skills_git_status")).toEqual([]);

    view.rerender(wrap(<CommitBar ctx={context(command)} />));
    await waitFor(() => expect(bar()?.textContent).toContain("1 uncommitted"));
    expect(calls(command, "skills_git_status")).toEqual([SCOPE]);

    // A write folded into the list: same skills, new object.
    changes = [change("pdf", "added"), change("docx", "added")];
    view.rerender(wrap(<CommitBar ctx={context(command, { ...LIST })} />));
    await waitFor(() => expect(bar()?.textContent).toContain("2 uncommitted"));
    expect(calls(command, "skills_git_status")).toHaveLength(2);
  });

  it("does not ask again for a render that changed nothing", async () => {
    const command = mockCommand({ skills_git_status: () => ({ git: git([change("pdf")]) }) });
    const view = render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(bar()).toBeTruthy());
    view.rerender(wrap(<CommitBar ctx={context(command)} />));
    expect(calls(command, "skills_git_status")).toHaveLength(1);
  });

  it("opens with every entry ticked and a message written from them", async () => {
    const command = mockCommand({
      skills_git_status: () => ({ git: git([change("pdf", "added"), change("old", "removed"), change(RECORD_FILE)]) }),
    });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(bar()).toBeTruthy());
    expect(screen.queryByLabelText("Commit message")).toBeNull();

    fireEvent.click(toggle());
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes.map((b) => b.getAttribute("aria-checked"))).toEqual(["true", "true", "true"]);
    expect(message().value).toBe("skills: add pdf; remove old");
  });

  it("commits only the ticked names, with a message that followed the ticks", async () => {
    const command = mockCommand({
      skills_git_status: () => ({ git: git([change("pdf", "added"), change("old", "removed")]) }),
      commit_skills: () => ({ commit: "abc1234", git: git([change("old", "removed")]) }),
    });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(bar()).toBeTruthy());
    fireEvent.click(toggle());

    fireEvent.click(screen.getByRole("checkbox", { name: /^old/ }));
    expect(message().value).toBe("skills: add pdf");
    fireEvent.click(screen.getByRole("button", { name: "Commit" }));

    await waitFor(() => expect(calls(command, "commit_skills")).toHaveLength(1));
    expect(calls(command, "commit_skills")[0]).toEqual({ threadId: "t1", names: ["pdf"], message: "skills: add pdf" });

    // The sha shows, and what was left out is still waiting.
    await committed("abc1234");
    expect(bar()?.textContent).toContain("1 uncommitted");
    expect(screen.getAllByRole("checkbox")).toHaveLength(1);
  });

  it("sends the message as typed, trimmed, and stops following the ticks once it is", async () => {
    const command = mockCommand({
      skills_git_status: () => ({ git: git([change("pdf", "added"), change("docx", "added")]) }),
      commit_skills: () => ({ commit: "abc1234", git: git([]) }),
    });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(bar()).toBeTruthy());
    fireEvent.click(toggle());

    fireEvent.change(message(), { target: { value: "  my own words  " } });
    fireEvent.click(screen.getByRole("checkbox", { name: /^docx/ }));
    expect(message().value).toBe("  my own words  ");
    fireEvent.click(screen.getByRole("button", { name: "Commit" }));

    await waitFor(() => expect(calls(command, "commit_skills")).toHaveLength(1));
    expect(calls(command, "commit_skills")[0]).toMatchObject({ names: ["pdf"], message: "my own words" });
  });

  it("will not commit nothing, or without a message", async () => {
    const command = mockCommand({ skills_git_status: () => ({ git: git([change("pdf", "added")]) }) });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(bar()).toBeTruthy());
    fireEvent.click(toggle());
    const commit = screen.getByRole("button", { name: "Commit" }) as HTMLButtonElement;
    expect(commit.disabled).toBe(false);

    fireEvent.change(message(), { target: { value: "   " } });
    expect(commit.disabled).toBe(true);
    fireEvent.change(message(), { target: { value: "back" } });
    expect(commit.disabled).toBe(false);

    fireEvent.click(screen.getByRole("checkbox", { name: /^pdf/ }));
    expect(commit.disabled).toBe(true);
    fireEvent.submit(message().closest("form")!);
    expect(calls(command, "commit_skills")).toEqual([]);
  });

  it("closes to the sha alone when everything was committed, until it is dismissed", async () => {
    const command = mockCommand({
      skills_git_status: () => ({ git: git([change("pdf", "added")]) }),
      commit_skills: () => ({ commit: "abc1234", git: git([]) }),
    });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(bar()).toBeTruthy());
    fireEvent.click(toggle());
    fireEvent.click(screen.getByRole("button", { name: "Commit" }));

    await committed("abc1234");
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.queryByLabelText("Commit message")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(bar()).toBeNull();
  });

  it("shows why a commit failed and keeps the entries to try again", async () => {
    let fail = true;
    const command = mockCommand({
      skills_git_status: () => ({ git: git([change("pdf", "added")]) }),
      commit_skills: () => {
        if (fail) throw new Error("author identity unknown");
        return { commit: "abc1234", git: git([]) };
      },
    });
    render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(bar()).toBeTruthy());
    fireEvent.click(toggle());
    fireEvent.click(screen.getByRole("button", { name: "Commit" }));

    expect(await screen.findByText(/author identity unknown/)).toBeTruthy();
    expect(screen.getAllByRole("checkbox")).toHaveLength(1);

    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Commit" }));
    await committed("abc1234");
    expect(screen.queryByText(/author identity unknown/)).toBeNull();
  });

  it("does not let a status asked for before a commit bring the committed entries back", async () => {
    let land: (res: { git: GitStatus }) => void = () => {};
    let asked = 0;
    const command = mockCommand({
      skills_git_status: () => {
        if (++asked === 1) return { git: git([change("pdf", "added")]) };
        return new Promise<{ git: GitStatus }>((resolve) => (land = resolve));
      },
      commit_skills: () => ({ commit: "abc1234", git: git([]) }),
    });
    const view = render(<CommitBar ctx={context(command)} />);
    await waitFor(() => expect(bar()).toBeTruthy());

    // A write starts a second check that is slow to answer.
    view.rerender(wrap(<CommitBar ctx={context(command, { ...LIST })} />));
    await waitFor(() => expect(asked).toBe(2));

    fireEvent.click(toggle());
    fireEvent.click(screen.getByRole("button", { name: "Commit" }));
    await committed("abc1234");

    // A turn of the event loop, so the late answer has had every chance to land.
    await act(async () => {
      land({ git: git([change("pdf", "added")]) });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(bar()?.textContent).not.toContain("uncommitted");
  });
});
