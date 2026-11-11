import { useCallback, useMemo } from "react";

import { useStoredFlag, useStoredKeys, withKey } from "~/components/sidebar/useStoredKeys";
import type { Project } from "~/protocol";
import type { SidebarView } from "~/sidebarOrder";

// Which projects this device is hiding, and which of the groups it is showing
// are folded shut. Device-local like the sidebar's width: the phone is usually
// narrowed to the one thing being worked on and the desktop is not, and making
// that travel would mean one of them is always wrong.
const PROJECT_FILTER_KEY = "omniplex.projectFilter";
const COLLAPSED_KEY = "omniplex.projectCollapsed";
// Whether this device carves the list into project groups at all. Device-local
// for the same reason again: a phone narrowed to one project has nothing to
// group, and a desktop showing five may want the headers or may not.
const GROUPED_KEY = "omniplex.groupByProject";

/**
 * The project filter and the collapse state, threaded to the sidebar's header
 * and list together. They travel as one because they are one control surface:
 * the menu decides which groups exist and the headers decide which are open.
 */
export interface ProjectView extends SidebarView {
  onToggleGrouped: (on: boolean) => void;
  onToggle: (id: string, show: boolean) => void;
  onShowAll: () => void;
  onHideAll: () => void;
  onToggleCollapse: (key: string) => void;
}

/**
 * The sidebar's view state, held above it: the list renders it, and the
 * thread shortcuts walk the same order the list shows. Only the user's own
 * choices are stored, so a project added on a paired device arrives showing
 * rather than pre-hidden.
 */
export function useProjectView(projects: Project[]): ProjectView {
  const [hidden, setHidden] = useStoredKeys(PROJECT_FILTER_KEY);
  const [collapsed, setCollapsed] = useStoredKeys(COLLAPSED_KEY);
  const [grouped, setGrouped] = useStoredFlag(GROUPED_KEY, true);

  const onToggle = useCallback(
    (id: string, show: boolean) => setHidden((current) => withKey(current, id, !show)),
    [setHidden],
  );
  const onShowAll = useCallback(() => setHidden(new Set()), [setHidden]);
  const onHideAll = useCallback(
    () => setHidden(new Set(projects.map((p) => p.id))),
    [projects, setHidden],
  );
  const onToggleCollapse = useCallback(
    (key: string) => setCollapsed((current) => withKey(current, key, !current.has(key))),
    [setCollapsed],
  );

  // One object while none of it changes: the sidebar is memoised, and a fresh
  // view on every App render would re-render it for each.
  return useMemo(
    () => ({
      grouped,
      onToggleGrouped: setGrouped,
      hidden,
      collapsed,
      onToggle,
      onShowAll,
      onHideAll,
      onToggleCollapse,
    }),
    [grouped, setGrouped, hidden, collapsed, onToggle, onShowAll, onHideAll, onToggleCollapse],
  );
}
