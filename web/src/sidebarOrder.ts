/**
 * The sidebar's list as sections, and the threads in the order it shows them.
 *
 * One function decides the sections and both the sidebar and the keyboard
 * read it: the list renders them, and previous/next and ⌘1–9 walk them
 * flattened. Two copies of "pinned first, then flat or grouped" would drift,
 * and a shortcut that skips a row the user can see is worse than none.
 *
 * Pure, like `projectGroups` and `pinned`, and it runs them in the same order
 * the list always has: the project filter, then the pinned split, then
 * grouping over what is left. Nothing here reorders, so a row still folds away
 * exactly where it stood.
 */

import { splitPinned } from "~/pinned";
import { groupThreads, visibleByProject } from "~/projectGroups";
import type { Agent, Project, ThreadMeta } from "~/protocol";
import { sidebarThreads } from "~/threadTree";

// The collapse keys of the two sections that are not projects: Pinned, and
// the rest of the list under it when that is flat. Project keys are uuids or
// `cwd:` paths, so neither can collide with one.
export const PINNED_KEY = "pinned";
export const UNPINNED_KEY = "unpinned";
export const ASSISTANT_KEY = "assistant";
export const AGENTS_KEY = "agents";

/** The part of the sidebar's view state that decides what is listed. */
export interface SidebarView {
  /** Project ids switched off in the header menu. */
  hidden: Set<string>;
  /** Section keys folded shut. */
  collapsed: Set<string>;
  /** Carve the list into project groups; off, it is one list in the user's order. */
  grouped: boolean;
}

export interface SidebarSection {
  /** The collapse key; null for the bare list, which has no header to fold it by. */
  key: string | null;
  name: string;
  threads: ThreadMeta[];
  /** Rows name their project everywhere but under that project's own header. */
  showProject: boolean;
  /**
   * The agents listed in this section, above its threads: a project group's
   * own, or every agent in the flat list's Agents section. Absent where a
   * section has no agents to offer (Pinned, Assistant, the flat threads).
   */
  agents?: Agent[];
  /** A project group's project, which its "New agent" + creates in. */
  projectId?: string;
}

/**
 * The sections the list renders, top to bottom. Empty when the filter hides
 * every thread and agent. A section never comes back empty: it leaves with
 * its last row.
 *
 * An agent's threads are the agent's, reached through its row, so they are
 * never rows of their own here, and the keyboard's walk skips them too.
 */
export function sidebarSections(
  threads: ThreadMeta[],
  projects: Project[],
  view: Pick<SidebarView, "hidden" | "grouped">,
  agents: Agent[] = [],
): SidebarSection[] {
  // A delegated thread lives inside the thread that delegated it, which
  // stands for it here, so it has no row while that thread is listed. An
  // agent's threads sit under its row instead, their delegates with them.
  const shown = visibleByProject(
    sidebarThreads(threads).filter((t) => !t.agentId),
    projects,
    view.hidden,
  );
  const shownAgents = visibleAgents(agents, projects, view.hidden);
  const { pinned, rest: unpinned } = splitPinned(shown);
  // Assistant chats belong to no project, so they get a section of their own
  // under Pinned however the rest is laid out.
  const assistant = unpinned.filter((t) => !t.projectId);
  const rest = unpinned.filter((t) => t.projectId);
  const groups = view.grouped
    ? groupThreads(rest, projects, new Set(shownAgents.map((a) => a.projectId)))
    : [];

  // Pinned threads come from every project, so their rows name theirs, the
  // way a flat list's do. Nothing pinned on screen is no section at all.
  const top: SidebarSection[] = [];
  if (pinned.length > 0) top.push({ key: PINNED_KEY, name: "Pinned", threads: pinned, showProject: true });
  if (assistant.length > 0) {
    top.push({ key: ASSISTANT_KEY, name: "Assistant", threads: assistant, showProject: false });
  }

  // One group is not a grouping, however it came to be the only one: the
  // header would name the only thing there is, on every row, forever.
  // Grouping switched off is the same flat list, and there the row naming its
  // project is the point. Under Pinned the flat rest still needs a header of
  // its own, or it would read as more of the pinned rows.
  if (groups.length > 1) {
    return [
      ...top,
      ...groups.map((g) => ({
        key: g.key,
        name: g.name,
        threads: g.threads,
        showProject: false,
        // A group whose key is a cwd has no project to make an agent in.
        ...(projects.some((p) => p.id === g.key) && {
          projectId: g.key,
          agents: shownAgents.filter((a) => a.projectId === g.key),
        }),
      })),
    ];
  }
  // Flat, the agents are one section of their own above the threads, and the
  // threads then need a header to read as something else.
  // Its + makes the next one where these are, when they are all in one place.
  if (shownAgents.length > 0) {
    const where = new Set(shownAgents.map((a) => a.projectId));
    top.push({
      key: AGENTS_KEY,
      name: "Agents",
      threads: [],
      agents: shownAgents,
      showProject: true,
      ...(where.size === 1 && { projectId: shownAgents[0].projectId }),
    });
  }
  if (rest.length === 0) return top;
  if (top.length === 0) return [{ key: null, name: "Threads", threads: rest, showProject: true }];
  return [...top, { key: UNPINNED_KEY, name: "Threads", threads: rest, showProject: true }];
}

/** The agents the project filter leaves showing. */
export function visibleAgents(agents: Agent[], projects: Project[], hidden: Set<string>): Agent[] {
  if (hidden.size === 0) return agents;
  const live = new Set(projects.map((p) => p.id));
  return agents.filter((a) => !(hidden.has(a.projectId) && live.has(a.projectId)));
}

/** The sections' rows in order, leaving out folded ones unless asked not to. */
export function sectionRows(
  sections: SidebarSection[],
  collapsed: Set<string>,
  includeCollapsed = false,
): ThreadMeta[] {
  return sections.flatMap((s) =>
    !includeCollapsed && s.key !== null && collapsed.has(s.key) ? [] : s.threads,
  );
}

/** The threads in the exact order the sidebar shows them. */
export function sidebarOrder(
  threads: ThreadMeta[],
  projects: Project[],
  view: SidebarView,
  opts: { includeCollapsed?: boolean } = {},
): ThreadMeta[] {
  return sectionRows(sidebarSections(threads, projects, view), view.collapsed, opts.includeCollapsed);
}
