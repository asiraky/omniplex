/**
 * Narrowing the sidebar to a set of projects, and carving what is left into
 * groups when — and only when — there is more than one project to see.
 *
 * Pure, like `labelFilter`: the sidebar hands in whatever list it is currently
 * rendering, including the delete flow's frozen ordering and its departing
 * row, and gets a shorter list or a set of groups back. Grouping never
 * reorders within a group, so a row still folds away exactly where it stood.
 *
 * The rules:
 * - The filter names what is *hidden*, not what is shown, so a project added
 *   on another device arrives visible rather than pre-hidden.
 * - Group order is the order the projects first appear in the list. The server
 *   sends sessions most-recently-updated first, so that is "last used" for
 *   free, with no second sort to disagree with the first.
 * - A group with no sessions does not exist. Nothing else has to remember to
 *   suppress its header, because there is no group to have one.
 * - One group is not a grouping. Whether that is because one project was
 *   selected or because three were and only one has any sessions is not a
 *   distinction worth drawing: what matters is what is on screen.
 * - A session whose project cannot be resolved falls back to its cwd, exactly
 *   as the row already does. Sessions cannot be created without a project and
 *   a project owning sessions cannot be deleted, so this is the pre-project
 *   shape rather than a state the UI can reach — it costs one line to keep it
 *   from disappearing, and no menu entry.
 */

import type { Project, SessionMeta } from "~/protocol";

/** One project's worth of the sidebar, in the order the list already had. */
export interface ProjectGroup {
  /** Stable across renders: the project id, or the cwd standing in for one. */
  key: string;
  name: string;
  sessions: SessionMeta[];
}

/** What a session with no resolvable project is filed under — the last two
    path segments of its cwd, which is what its row shows anyway. */
function cwdName(session: SessionMeta): string {
  return session.cwd.split("/").slice(-2).join("/");
}

export function visibleByProject(
  sessions: SessionMeta[],
  projects: Project[],
  hidden: Set<string>,
): SessionMeta[] {
  if (hidden.size === 0 || projects.length === 0) return sessions;
  // A hidden id for a project that no longer exists hides nothing, the way a
  // deleted label's does: otherwise deleting a hidden project would strand
  // sessions behind a checkbox that is no longer in the menu.
  const live = new Set(projects.map((p) => p.id));
  const off = new Set([...hidden].filter((id) => live.has(id)));
  if (off.size === 0) return sessions;
  return sessions.filter((s) => !(s.projectId && off.has(s.projectId)));
}

/**
 * The sessions, carved by project, in order of last use.
 *
 * Returns one group per project that actually has sessions here. A caller with
 * a single group in hand has nothing to group and should render the sessions
 * flat — `groups.length > 1` is the whole test, and it is the same test
 * whether the filter narrowed the list or the sessions simply were not there.
 */
export function groupSessions(sessions: SessionMeta[], projects: Project[]): ProjectGroup[] {
  const byId = new Map(projects.map((p) => [p.id, p]));
  const groups = new Map<string, ProjectGroup>();

  for (const s of sessions) {
    const project = s.projectId ? byId.get(s.projectId) : undefined;
    const key = project ? project.id : `cwd:${cwdName(s)}`;
    const existing = groups.get(key);
    if (existing) {
      existing.sessions.push(s);
      continue;
    }
    // First appearance sets the order, and the list arrives most-recently-
    // updated first, so the project holding the newest session leads.
    groups.set(key, { key, name: project ? project.config.name : cwdName(s), sessions: [s] });
  }

  return [...groups.values()];
}
