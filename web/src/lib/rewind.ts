import { createContext, useContext } from "react";

import { parseAttachedFiles } from "~/lib/artefacts";
import { isLive } from "~/lib/jobs";
import type { Item, ThreadMeta, ThreadState, Turn } from "~/protocol";

/**
 * Opens the "Edit from here" confirmation for a prompt. Provided by the thread
 * view around the transcript; null wherever the thread cannot be rewound (its
 * harness cannot, or the composer is locked), which hides every such button.
 */
export const RewindContext = createContext<((item: Item) => void) | null>(null);

export function useRewind() {
  return useContext(RewindContext);
}

/**
 * Whether the thread can be cut back to before turns[i]. The turn needs its
 * own anchor, and so does the one before it: a harness that forks rather than
 * truncates resumes from the kept turn's last entry. Mirrors
 * projection.State.CanRewindTo, which the server enforces.
 */
export function canRewindTo(turns: Turn[], i: number): boolean {
  if (i < 0 || i >= turns.length || !turns[i].rewindable) return false;
  return i === 0 || !!turns[i - 1].rewindable;
}

/** The prompt that opened its turn. A prompt steered or injected into a
    running turn carries that turn's id under its own queue id, and is not one. */
export function opensTurn(item: Pick<Item, "id" | "turnId" | "role">): boolean {
  return item.role === "user" && !!item.turnId && item.id === `prompt:${item.turnId}`;
}

/**
 * Whether "Edit from here" is on offer for this prompt right now: it opened a
 * turn that can be rewound to, and nothing is running that the server would
 * refuse a rewind over (a turn, or a job beside one).
 */
export function canEditFrom(s: ThreadState | null, item: Pick<Item, "id" | "turnId" | "role">): boolean {
  if (!s || s.closed || s.phase === "turn" || !opensTurn(item)) return false;
  if (s.jobs.some(isLive)) return false;
  return canRewindTo(s.turns, s.turns.findIndex((t) => t.id === item.turnId));
}

/** How many files a restore puts back, as far as the turns' diffs can say. */
export interface FilesSince {
  count: number;
  /** A diff was cut short or could not be measured: there are at least `count`. */
  atLeast: boolean;
}

/**
 * The files the turns from `turnId` on changed: the target turn's own changes
 * too, since a restore goes back to the checkpoint taken as it started. A
 * rename touches both its names.
 */
export function filesChangedSince(turns: Turn[], turnId: string): FilesSince {
  const i = turns.findIndex((t) => t.id === turnId);
  const paths = new Set<string>();
  let atLeast = false;
  for (const t of i < 0 ? [] : turns.slice(i)) {
    const d = t.diff;
    if (!d) continue;
    if (d.truncated || d.error) atLeast = true;
    for (const f of d.files ?? []) {
      paths.add(f.path);
      if (f.oldPath) paths.add(f.oldPath);
    }
  }
  return { count: paths.size, atLeast };
}

/** Why a files restore is not on offer, or null when it is. */
export type RestoreBlocker = "project" | "unmanaged" | "shared";

function inside(path: string, dir: string): boolean {
  const d = dir.replace(/\/+$/, "");
  return path === d || path.startsWith(`${d}/`);
}

/**
 * Whether a rewind may also put the files back: only in a worktree Omniplex
 * made for this thread, and only while no other open thread works in the same
 * folder, inside it, or around it — a restore would rewrite that thread's
 * files under it. Mirrors the server, which has the last word.
 */
export function restoreBlocker(
  thread: Pick<ThreadMeta, "id" | "cwd" | "workspaceMode">,
  threads: Pick<ThreadMeta, "id" | "cwd" | "phase" | "attention">[],
): RestoreBlocker | null {
  if (thread.workspaceMode === "local") return "project";
  if (thread.workspaceMode !== "managed" || !thread.cwd) return "unmanaged";
  const cwd = thread.cwd.replace(/\/+$/, "");
  const shared = threads.some(
    (o) =>
      o.id !== thread.id &&
      o.phase !== "closed" &&
      o.attention !== "closed" &&
      !!o.cwd &&
      (inside(o.cwd, cwd) || inside(cwd, o.cwd)),
  );
  return shared ? "shared" : null;
}

/** Everything the confirmation says and does, worked out as it opens. */
export interface RewindTarget {
  threadId: string;
  turnId: string;
  /** The prompt's text, without its attached-file markers: what goes back
      into the composer. */
  text: string;
  /** How many turns after this one go with it. */
  later: number;
  files: FilesSince;
  restore: RestoreBlocker | null;
}

export function rewindTarget(
  s: ThreadState,
  item: Item,
  meta: Pick<ThreadMeta, "id" | "cwd" | "workspaceMode"> | undefined,
  threads: Pick<ThreadMeta, "id" | "cwd" | "phase" | "attention">[],
): RewindTarget | null {
  const turnId = item.turnId;
  const i = turnId ? s.turns.findIndex((t) => t.id === turnId) : -1;
  if (!turnId || i < 0) return null;
  return {
    threadId: s.threadId,
    turnId,
    text: parseAttachedFiles(item.text ?? "").text,
    later: s.turns.length - i - 1,
    files: filesChangedSince(s.turns, turnId),
    restore: meta ? restoreBlocker(meta, threads) : "unmanaged",
  };
}
