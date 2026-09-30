// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { SkillsCommand } from "./parts";
import { UpdateDialog } from "./UpdateDialog";
import type { Skill, UpdateSkill, UpdateStage } from "~/lib/skills";
import { render, wrap } from "~/test/harness";

const entry = (name: string, extra: Partial<UpdateSkill> = {}): UpdateSkill => ({
  name,
  dir: `/lib/${name}`,
  changed: false,
  files: [],
  ...extra,
});

const changed = (name: string): UpdateSkill =>
  entry(name, { changed: true, files: [{ path: "SKILL.md", status: "modified" }] });

const stage = (skills: UpdateSkill[], id = "up-1"): UpdateStage => ({ id, repo: "acme/skills", skills });

const placed = (dir: unknown): Skill => ({
  name: String(dir).split("/").pop() ?? "",
  description: "",
  dir: String(dir),
  scope: "user",
  paths: [],
  harnesses: ["claude"],
  editable: true,
});

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

const applyAll = (args: Record<string, unknown>) => ({ skills: (args.dirs as string[]).map(placed) });

function open(command: SkillsCommand) {
  const onOpenChange = vi.fn();
  const onUpdated = vi.fn();
  const element = (dir = "/lib/pdf") => (
    <UpdateDialog
      open
      onOpenChange={onOpenChange}
      command={command}
      scopeArgs={{ threadId: "t1" }}
      dir={dir}
      repo="acme/skills"
      onUpdated={onUpdated}
    />
  );
  const view = render(element());
  return { onOpenChange, onUpdated, view, element };
}

const updateAll = () => screen.queryByRole("button", { name: /^Update all/ });
const rowOf = (name: string) => within(screen.getByText(name).closest("li") as HTMLElement);

describe("UpdateDialog", () => {
  it("checks the source the moment it opens, from the skill it was opened on", async () => {
    const command = mockCommand({ stage_update: () => stage([entry("pdf")]), discard_staged: () => ({}) });
    open(command);
    await screen.findByText("pdf");
    expect(calls(command, "stage_update")).toEqual([{ threadId: "t1", dir: "/lib/pdf" }]);
  });

  it("offers an update only for a skill that changed and is still in the source", async () => {
    const command = mockCommand({
      stage_update: () => stage([entry("same"), changed("newer"), entry("dropped", { changed: true, gone: true })]),
      discard_staged: () => ({}),
    });
    open(command);
    await screen.findByText("newer");

    expect(rowOf("newer").queryByRole("button", { name: "Update newer" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Update same" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Update dropped" })).toBeNull();
    // One skill to update is its own button; "all" of one would be a second way to do the same thing.
    expect(updateAll()).toBeNull();
  });

  it("says when the only difference is edits made here, and still offers to put upstream back", async () => {
    const command = mockCommand({
      stage_update: () =>
        stage([
          entry("mine", { changed: true, local: true, files: [{ path: "notes.md", status: "modified" }] }),
          changed("newer"),
        ]),
      discard_staged: () => ({}),
    });
    open(command);
    await screen.findByText("mine");

    expect(rowOf("mine").getByText("edited here")).toBeTruthy();
    expect(rowOf("mine").getByText(/replaces your edits/)).toBeTruthy();
    expect(rowOf("mine").getByRole("button", { name: "Update mine" })).toBeTruthy();
    expect(rowOf("newer").queryByText("edited here")).toBeNull();
    expect(rowOf("newer").queryByText(/replaces your edits/)).toBeNull();
  });

  it("updates just the skill that was chosen and leaves the rest waiting", async () => {
    const command = mockCommand({
      stage_update: () => stage([changed("a"), changed("b"), changed("c")]),
      apply_update: applyAll,
      discard_staged: () => ({}),
    });
    const { onUpdated } = open(command);
    await screen.findByText("b");

    fireEvent.click(screen.getByRole("button", { name: "Update b" }));
    await waitFor(() => expect(onUpdated).toHaveBeenCalledWith([placed("/lib/b")]));
    expect(calls(command, "apply_update")).toEqual([{ threadId: "t1", id: "up-1", dirs: ["/lib/b"] }]);

    await waitFor(() => expect(screen.queryByRole("button", { name: "Update b" })).toBeNull());
    expect(screen.getByRole("button", { name: "Update a" })).toBeTruthy();

    // "All" now means the two that are left.
    fireEvent.click(updateAll()!);
    await waitFor(() => expect(calls(command, "apply_update")).toHaveLength(2));
    expect(calls(command, "apply_update")[1].dirs).toEqual(["/lib/a", "/lib/c"]);
    await waitFor(() => expect(screen.queryByRole("button", { name: /^Update/ })).toBeNull());
  });

  it("updates every changed skill at once, and none of the unchanged or gone ones", async () => {
    const command = mockCommand({
      stage_update: () =>
        stage([entry("same"), changed("a"), entry("dropped", { changed: true, gone: true }), changed("b")]),
      apply_update: applyAll,
      discard_staged: () => ({}),
    });
    const { onUpdated } = open(command);
    await screen.findByText("a");

    fireEvent.click(updateAll()!);
    await waitFor(() => expect(onUpdated).toHaveBeenCalled());
    expect(calls(command, "apply_update")).toEqual([{ threadId: "t1", id: "up-1", dirs: ["/lib/a", "/lib/b"] }]);
  });

  it("keeps a skill waiting when its update fails, and says why", async () => {
    let fail = true;
    const command = mockCommand({
      stage_update: () => stage([changed("a")]),
      apply_update: (args) => {
        if (fail) throw new Error("disk full");
        return applyAll(args);
      },
      discard_staged: () => ({}),
    });
    const { onUpdated } = open(command);
    fireEvent.click(await screen.findByRole("button", { name: "Update a" }));

    expect(await screen.findByText(/disk full/)).toBeTruthy();
    expect(onUpdated).not.toHaveBeenCalled();

    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Update a" }));
    await waitFor(() => expect(onUpdated).toHaveBeenCalled());
    expect(screen.queryByText(/disk full/)).toBeNull();
  });

  it("reads a changed file only when it is opened, and shows both sides of it", async () => {
    const command = mockCommand({
      stage_update: () => stage([changed("pdf")]),
      read_update_file: () => ({ old: "one\ntwo\n", new: "one\nTWO\n", binary: false }),
      discard_staged: () => ({}),
    });
    open(command);
    await screen.findByText("pdf");
    expect(calls(command, "read_update_file")).toEqual([]);

    fireEvent.click(screen.getByRole("button", { name: /SKILL\.md/ }));
    const diff = within(await screen.findByRole("group", { name: /SKILL\.md/ }));
    expect(calls(command, "read_update_file")).toEqual([
      { threadId: "t1", id: "up-1", dir: "/lib/pdf", path: "SKILL.md" },
    ]);
    expect(diff.getByText("two")).toBeTruthy();
    expect(diff.getByText("TWO")).toBeTruthy();
  });

  it("does not try to draw a binary file", async () => {
    const command = mockCommand({
      stage_update: () => stage([changed("pdf")]),
      read_update_file: () => ({ old: "", new: "", binary: true }),
      discard_staged: () => ({}),
    });
    open(command);
    fireEvent.click(await screen.findByRole("button", { name: /SKILL\.md/ }));
    await waitFor(() => expect(calls(command, "read_update_file")).toHaveLength(1));
    await waitFor(() => expect(screen.queryByRole("status", { name: "Loading" })).toBeNull());
    expect(screen.queryByRole("group", { name: /SKILL\.md/ })).toBeNull();
  });

  it("shows a failed check and checks again on request", async () => {
    let attempts = 0;
    const command = mockCommand({
      stage_update: () => {
        if (++attempts === 1) throw new Error("could not reach github.com");
        return stage([changed("pdf")]);
      },
      discard_staged: () => ({}),
    });
    open(command);

    expect(await screen.findByText(/could not reach github/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("button", { name: "Update pdf" })).toBeTruthy();
    expect(screen.queryByText(/could not reach github/)).toBeNull();
  });

  // The footer's button and the corner's cross: two ways out, one outcome.
  it.each([0, 1])("discards the fetched copy on close (way out %i)", async (which) => {
    const command = mockCommand({ stage_update: () => stage([changed("pdf")]), discard_staged: () => ({}) });
    const { onOpenChange } = open(command);
    await screen.findByText("pdf");

    const ways = screen.getAllByRole("button", { name: "Close" });
    expect(ways).toHaveLength(2);
    fireEvent.click(ways[which]);
    expect(calls(command, "discard_staged")).toEqual([{ threadId: "t1", id: "up-1" }]);
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("discards a check that lands after the dialog was cancelled", async () => {
    let land: (stage: UpdateStage) => void = () => {};
    const command = mockCommand({
      stage_update: () => new Promise<UpdateStage>((resolve) => (land = resolve)),
      discard_staged: () => ({}),
    });
    const { onOpenChange } = open(command);

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(calls(command, "discard_staged")).toEqual([]);

    land(stage([changed("pdf")], "late"));
    await waitFor(() => expect(calls(command, "discard_staged")).toEqual([{ threadId: "t1", id: "late" }]));
    expect(screen.queryByRole("button", { name: "Update pdf" })).toBeNull();
  });

  it("stays on the skill it was opened for when the list shifts underneath it", async () => {
    const command = mockCommand({ stage_update: () => stage([changed("pdf")]), discard_staged: () => ({}) });
    const { view, element } = open(command);
    await screen.findByText("pdf");

    await act(async () => {
      view.rerender(wrap(element("/lib/other")));
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(calls(command, "stage_update")).toEqual([{ threadId: "t1", dir: "/lib/pdf" }]);
  });
});
