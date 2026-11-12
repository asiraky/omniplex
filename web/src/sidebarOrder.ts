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
import type { Project, ThreadMeta } from "~/protocol";

// The collapse keys of the two sections that are not projects: Pinned, and
// the rest of the list under it when that is flat. Project keys are uuids or
// `cwd:` paths, so neither can collide with one.
export const PINNED_KEY = "pinned";
export const UNPINNED_KEY = "unpinned";

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
}

/**
 * The sections the list renders, top to bottom. Empty when the filter hides
 * every thread. A section never comes back empty: it leaves with its last row.
 */
export function sidebarSections(
  threads: ThreadMeta[],
  projects: Project[],
  view: Pick<SidebarView, "hidden" | "grouped">,
): SidebarSection[] {
  const shown = visibleByProject(threads, projects, view.hidden);
  const { pinned, rest } = splitPinned(shown);
  const groups = view.grouped ? groupThreads(rest, projects) : [];

  // Pinned threads come from every project, so their rows name theirs, the
  // way a flat list's do. Nothing pinned on screen is no section at all.
  const top: SidebarSection[] =
    pinned.length > 0 ? [{ key: PINNED_KEY, name: "Pinned", threads: pinned, showProject: true }] : [];

  // One group is not a grouping, however it came to be the only one: the
  // header would name the only thing there is, on every row, forever.
  // Grouping switched off is the same flat list, and there the row naming its
  // project is the point. Under Pinned the flat rest still needs a header of
  // its own, or it would read as more of the pinned rows.
  if (groups.length > 1) {
    return [
      ...top,
      ...groups.map((g) => ({ key: g.key, name: g.name, threads: g.threads, showProject: false })),
    ];
  }
  if (rest.length === 0) return top;
  if (top.length === 0) return [{ key: null, name: "Threads", threads: rest, showProject: true }];
  return [...top, { key: UNPINNED_KEY, name: "Threads", threads: rest, showProject: true }];
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
