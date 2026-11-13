import { describe, expect, it } from "vitest";

import { emptyState } from "~/apply";
import type { ChangedFile, Item, Job, ThreadMeta, ThreadState, Turn, TurnDiff } from "~/protocol";

import { canEditFrom, filesChangedSince, restoreBlocker, rewindTarget } from "./rewind";

const turn = (id: string, rewindable = true, diff?: Partial<TurnDiff>): Turn => ({
  id,
  prompt: `ask ${id}`,
  done: true,
  rewindable,
  ...(diff ? { diff: { turnId: id, files: [], additions: 0, deletions: 0, ...diff } } : {}),
});

const file = (path: string, oldPath?: string): ChangedFile => ({
  path,
  ...(oldPath ? { oldPath } : {}),
  status: oldPath ? "renamed" : "modified",
  additions: 1,
  deletions: 0,
});

const prompt = (turnId: string, id = `prompt:${turnId}`): Item => ({
  id,
  kind: "message",
  role: "user",
  turnId,
  text: `ask ${turnId}`,
});

function thread(turns: Turn[], patch: Partial<ThreadState> = {}): ThreadState {
  return { ...emptyState("th1"), turns, items: turns.map((t) => prompt(t.id)), ...patch };
}

describe("edit from here is offered", () => {
  it("on a prompt that opened a turn it and the turn before can be rewound to", () => {
    const s = thread([turn("t1"), turn("t2"), turn("t3")]);
    expect(["t1", "t2", "t3"].map((id) => canEditFrom(s, prompt(id)))).toEqual([true, true, true]);
  });

  it("not on a prompt steered into a running turn", () => {
    const s = thread([turn("t1"), turn("t2")]);
    expect(canEditFrom(s, prompt("t2", "prompt:q1"))).toBe(false);
  });

  it("not on a turn without an anchor, but on the one after it", () => {
    const s = thread([turn("t1"), turn("t2", false), turn("t3"), turn("t4")]);
    expect(["t1", "t2", "t3", "t4"].map((id) => canEditFrom(s, prompt(id)))).toEqual([
      true,
      false,
      true,
      true,
    ]);
  });

  it("not while a turn runs, nor while a job does", () => {
    const turns = [turn("t1"), turn("t2")];
    expect(canEditFrom(thread(turns, { phase: "turn" }), prompt("t1"))).toBe(false);
    const job = (status: Job["status"], kind: Job["kind"] = "agent") =>
      ({ id: "j", depth: 0, kind, status, usage: {} }) as Job;
    expect(canEditFrom(thread(turns, { jobs: [job("running")] }), prompt("t1"))).toBe(false);
    expect(canEditFrom(thread(turns, { jobs: [job("completed")] }), prompt("t1"))).toBe(true);
    // Housekeeping is not work anyone waits on.
    expect(canEditFrom(thread(turns, { jobs: [job("running", "inert")] }), prompt("t1"))).toBe(true);
  });

  it("not on a closed thread", () => {
    expect(canEditFrom(thread([turn("t1")], { closed: true }), prompt("t1"))).toBe(false);
  });
});

describe("files a restore puts back", () => {
  it("counts the target turn's and every later turn's files once, both names of a rename", () => {
    const turns = [
      turn("t1", true, { files: [file("early.go")] }),
      turn("t2", true, { files: [file("a.go"), file("b.go")] }),
      turn("t3"),
      turn("t4", true, { files: [file("a.go"), file("c.go", "d.go")] }),
    ];
    expect(filesChangedSince(turns, "t2")).toEqual({ count: 4, atLeast: false });
    expect(filesChangedSince(turns, "t4")).toEqual({ count: 3, atLeast: false });
  });

  it("is a lower bound when a later diff was cut short or not measured", () => {
    const truncated = [turn("t1", true, { files: [file("a.go")], truncated: true })];
    expect(filesChangedSince(truncated, "t1")).toEqual({ count: 1, atLeast: true });
    const failed = [turn("t1"), turn("t2", true, { error: "git broke" })];
    expect(filesChangedSince(failed, "t1")).toEqual({ count: 0, atLeast: true });
  });

  it("ignores an earlier turn's cut-short diff", () => {
    const turns = [turn("t1", true, { truncated: true }), turn("t2", true, { files: [file("a.go")] })];
    expect(filesChangedSince(turns, "t2")).toEqual({ count: 1, atLeast: false });
  });
});

describe("a files restore", () => {
  const me = { id: "me", cwd: "/wt/feature", workspaceMode: "managed" };
  const other = (cwd: string, phase = "idle"): Pick<ThreadMeta, "id" | "cwd" | "phase"> => ({
    id: `o:${cwd}`,
    cwd,
    phase,
  });

  it("is offered in a worktree Omniplex made that no other open thread uses", () => {
    expect(restoreBlocker(me, [{ ...me, phase: "idle" }, other("/wt/feature-two"), other("/repo")])).toBeNull();
  });

  it("is refused when another open thread is in the same folder, inside it, or around it", () => {
    expect(restoreBlocker(me, [other("/wt/feature")])).toBe("shared");
    expect(restoreBlocker(me, [other("/wt/feature/sub")])).toBe("shared");
    expect(restoreBlocker(me, [other("/wt")])).toBe("shared");
    expect(restoreBlocker(me, [other("/wt/feature/")])).toBe("shared");
  });

  it("does not count the project checkout or a borrowed worktree around it", () => {
    expect(restoreBlocker(me, [{ ...other("/wt"), workspaceMode: "local" }])).toBeNull();
    expect(restoreBlocker(me, [{ ...other("/wt"), workspaceMode: "borrowed" }])).toBeNull();
    expect(restoreBlocker(me, [{ ...other("/wt/feature"), workspaceMode: "local" }])).toBe("shared");
  });

  it("does not count a closed thread", () => {
    expect(restoreBlocker(me, [other("/wt/feature", "closed")])).toBeNull();
  });

  it("is never offered in the project folder or a borrowed worktree", () => {
    expect(restoreBlocker({ ...me, workspaceMode: "local" }, [])).toBe("project");
    expect(restoreBlocker({ ...me, workspaceMode: "borrowed" }, [])).toBe("unmanaged");
    expect(restoreBlocker({ ...me, workspaceMode: undefined }, [])).toBe("unmanaged");
  });
});

describe("the rewind target", () => {
  it("counts the turns after it and gives back the text without its file markers", () => {
    const s = thread([turn("t1"), turn("t2"), turn("t3")]);
    const item = {
      ...prompt("t1"),
      text: "fix this\n<attached-files>\n- notes.txt (text/plain, 1 KB, artefact a1): /tmp/notes.txt\n</attached-files>",
    };
    const target = rewindTarget(s, item, { id: "th1", cwd: "/repo", workspaceMode: "local" }, []);
    expect(target).toMatchObject({ threadId: "th1", turnId: "t1", later: 2, restore: "project" });
    expect(target?.text).toBe("fix this");
    expect(rewindTarget(s, prompt("t3"), undefined, [])?.later).toBe(0);
  });
});
