/**
 * Threads that delegated to other threads, read off the thread list alone.
 *
 * A child is an ordinary thread whose `parentId` names the thread that
 * delegated it. It has no sidebar row of its own while that parent is in the
 * list: it lives inside the parent, and the parent's row stands for it. A
 * child whose parent has gone is a row like any other, so its work is never
 * out of reach.
 *
 * Everything here is derived from the list the server already sends; nothing
 * asks it for more.
 */

import type { ThreadMeta } from "~/protocol";
import { busy } from "~/threadStatus";

export interface ThreadTree {
  byId: Map<string, ThreadMeta>;
  /** Each listed thread's listed children, oldest first. */
  children: Map<string, ThreadMeta[]>;
}

export function threadTree(threads: ThreadMeta[]): ThreadTree {
  const byId = new Map<string, ThreadMeta>();
  for (const t of threads) byId.set(t.id, t);
  const children = new Map<string, ThreadMeta[]>();
  for (const t of threads) {
    const p = t.parentId;
    if (!p || p === t.id || !byId.has(p)) continue;
    const list = children.get(p);
    if (list) list.push(t);
    else children.set(p, [t]);
  }
  for (const list of children.values()) list.sort((a, b) => a.createdAt - b.createdAt);
  return { byId, children };
}

/**
 * The id of the sidebar row that stands for a thread: its topmost listed
 * ancestor, or itself when it has none. A parent chain that loops back on
 * itself has no top, so every thread on or under the loop is its own row
 * rather than hidden under nothing.
 */
export function rowOf(tree: ThreadTree, id: string): string {
  const seen = new Set<string>([id]);
  let at = tree.byId.get(id);
  while (at?.parentId && at.parentId !== at.id) {
    const up = tree.byId.get(at.parentId);
    if (!up) break;
    if (seen.has(up.id)) return id;
    seen.add(up.id);
    at = up;
  }
  return at?.id ?? id;
}

/** The threads that get a sidebar row: every one that is not shown under a
    listed parent. Order is kept. */
export function sidebarThreads(threads: ThreadMeta[], tree = threadTree(threads)): ThreadMeta[] {
  if (tree.children.size === 0) return threads;
  return threads.filter((t) => rowOf(tree, t.id) === t.id);
}

export interface Descendant {
  thread: ThreadMeta;
  /** 0 for a direct child. */
  depth: number;
}

/** Everything a thread delegated, all the way down, each parent before its
    children: a flat list that renders as a tree by indenting on `depth`. */
export function descendants(tree: ThreadTree, id: string): Descendant[] {
  const out: Descendant[] = [];
  const seen = new Set<string>([id]);
  const walk = (parent: string, depth: number) => {
    for (const c of tree.children.get(parent) ?? []) {
      if (seen.has(c.id)) continue;
      seen.add(c.id);
      out.push({ thread: c, depth });
      walk(c.id, depth + 1);
    }
  };
  walk(id, 0);
  return out;
}

/**
 * The thread is waiting on a person: a permission request, a question, or a
 * failure (its last turn, or its workspace). A busy thread whose last turn
 * failed has already moved on from it.
 */
export function needsYou(t: ThreadMeta): boolean {
  if (t.attention === "needs_permission" || t.attention === "needs_answer") return true;
  if (t.attention === "failed") return true;
  return !!t.turnFailed && !busy(t);
}

/**
 * What each sidebar row says about the threads under it: which rows hold the
 * open thread, and which hold one that needs the user. Worked out once per
 * list, so a row is handed two booleans rather than the tree.
 */
export function rowMarks(
  tree: ThreadTree,
  activeId: string | null,
): { childActive: Set<string>; childNeedsYou: Set<string> } {
  const childActive = new Set<string>();
  const childNeedsYou = new Set<string>();
  if (tree.children.size === 0) return { childActive, childNeedsYou };
  for (const t of tree.byId.values()) {
    const row = rowOf(tree, t.id);
    if (row === t.id) continue;
    if (t.id === activeId) childActive.add(row);
    if (needsYou(t)) childNeedsYou.add(row);
  }
  return { childActive, childNeedsYou };
}

/** What a thread's delegated work amounts to, for its jobs strip. */
export interface DelegatedCounts {
  /** Descendants still working, in a turn or on background work. */
  live: number;
  /** Descendants waiting on the user. */
  needsYou: number;
}

export function delegatedCounts(tree: ThreadTree, id: string | null): DelegatedCounts {
  const c: DelegatedCounts = { live: 0, needsYou: 0 };
  if (!id) return c;
  for (const { thread } of descendants(tree, id)) {
    if (needsYou(thread)) c.needsYou++;
    else if (busy(thread)) c.live++;
  }
  return c;
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/**
 * The jobs strip's line: what needs the user first, then the jobs, then the
 * threads still working. "1 thread needs you, 2 agents, 1 thread". Empty when
 * there is nothing to say.
 */
export function stripLabel(jobsLabel: string, counts: DelegatedCounts): string {
  const parts: string[] = [];
  if (counts.needsYou) parts.push(`${plural(counts.needsYou, "thread", "threads")} need${counts.needsYou === 1 ? "s" : ""} you`);
  if (jobsLabel) parts.push(jobsLabel);
  if (counts.live) parts.push(plural(counts.live, "thread", "threads"));
  return parts.join(", ");
}

export type ChildStatus = "Working" | "Needs approval" | "Needs answer" | "Failed" | "Done" | "Stopped";

/** A delegated thread's state in a word or two, for the jobs panel. */
export function childStatus(t: ThreadMeta): ChildStatus {
  switch (t.attention) {
    case "needs_permission":
      return "Needs approval";
    case "needs_answer":
      return "Needs answer";
    case "working":
    case "background":
      return "Working";
    case "failed":
      return "Failed";
    case "closed":
      return "Stopped";
    default:
      return t.turnFailed ? "Failed" : "Done";
  }
}
