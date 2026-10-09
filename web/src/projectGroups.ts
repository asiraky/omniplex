/**
 * Narrowing the sidebar to a set of projects, and carving what is left into
 * groups when grouping is on and there is more than one project to see.
 *
 * Pure, like `labelFilter`: the sidebar hands in whatever list it is currently
 * rendering, including the delete flow's frozen ordering and its departing
 * row, and gets a shorter list or a set of groups back. Grouping never
 * reorders within a group, so a row still folds away exactly where it stood.
 *
 * The rules:
 * - The filter names what is *hidden*, not what is shown, so a project added
 *   on another device arrives visible rather than pre-hidden.
 * - Rows inside a group keep the list's order, which is the user's own
 *   (`threadOrder`): a drag inside a group is the only thing that moves one.
 * - Groups follow the project registry, in the server's order — not the order
 *   their threads happen to appear in. The user drags threads, not groups, and
 *   moving a thread to the top of its project must not drag the whole project
 *   up past the others. A thread whose project cannot be resolved comes after
 *   every real project, in first-appearance order: there is no registry entry
 *   to place it by.
 * - A group with no threads does not exist. Nothing else has to remember to
 *   suppress its header, because there is no group to have one.
 * - One group is not a grouping. Whether that is because one project was
 *   selected or because three were and only one has any threads is not a
 *   distinction worth drawing: what matters is what is on screen. Grouping
 *   switched off (a per-device choice) gives one flat list in the user's
 *   order, with each row naming its project.
 * - A thread whose project cannot be resolved falls back to its cwd, exactly
 *   as the row already does. Threads cannot be created without a project and
 *   a project owning threads cannot be deleted, so this is the pre-project
 *   shape rather than a state the UI can reach — it costs one line to keep it
 *   from disappearing, and no menu entry.
 */

import type { Project, ThreadMeta } from "~/protocol";

/** One project's worth of the sidebar, in the order the list already had. */
export interface ProjectGroup {
  /** Stable across renders: the project id, or the cwd standing in for one. */
  key: string;
  name: string;
  threads: ThreadMeta[];
}

/** What a thread with no resolvable project is filed under — the last two
    path segments of its cwd, which is what its row shows anyway. */
function cwdName(thread: ThreadMeta): string {
  return thread.cwd.split("/").slice(-2).join("/");
}

export function visibleByProject(
  threads: ThreadMeta[],
  projects: Project[],
  hidden: Set<string>,
): ThreadMeta[] {
  if (hidden.size === 0 || projects.length === 0) return threads;
  // A hidden id for a project that no longer exists hides nothing, the way a
  // deleted label's does: otherwise deleting a hidden project would strand
  // threads behind a checkbox that is no longer in the menu.
  const live = new Set(projects.map((p) => p.id));
  const off = new Set([...hidden].filter((id) => live.has(id)));
  if (off.size === 0) return threads;
  return threads.filter((s) => !(s.projectId && off.has(s.projectId)));
}

/**
 * The threads, carved by project: groups in registry order, rows in the
 * list's own order.
 *
 * Returns one group per project that actually has threads here. A caller with
 * a single group in hand has nothing to group and should render the threads
 * flat — `groups.length > 1` is the whole test, and it is the same test
 * whether the filter narrowed the list or the threads simply were not there.
 */
export function groupThreads(threads: ThreadMeta[], projects: Project[]): ProjectGroup[] {
  const byId = new Map(projects.map((p) => [p.id, p]));
  const groups = new Map<string, ProjectGroup>();

  for (const s of threads) {
    const project = s.projectId ? byId.get(s.projectId) : undefined;
    // Keyed on the whole checkout, named by its tail: two checkouts ending
    // in the same two segments are still two different things.
    const key = project ? project.id : `cwd:${s.cwd}`;
    const existing = groups.get(key);
    if (existing) existing.threads.push(s);
    else groups.set(key, { key, name: project ? project.name : cwdName(s), threads: [s] });
  }

  // The registry decides the order of everything it knows; a Map keeps
  // insertion order, so what is left over keeps the order it first appeared.
  const ordered: ProjectGroup[] = [];
  for (const p of projects) {
    const g = groups.get(p.id);
    if (!g) continue;
    ordered.push(g);
    groups.delete(p.id);
  }
  return [...ordered, ...groups.values()];
}
