// Which project the new-session dialog opens on. Without this it opens on the
// server's first project — most recently updated, which is whichever project
// an agent last wrote to rather than the one the user last chose, so the pick
// has to be redone on nearly every session.
//
// Per browser, like sidebar width and panel state: it is a habit, not shared
// state, and the phone and the laptop are usually mid-different work.

const KEY = "omniplex.lastProject.v1";

/** The project id last started from, or "" when there is none stored. */
export function loadLastProject(): string {
  try {
    return localStorage.getItem(KEY) ?? "";
  } catch {
    // Storage can be denied outright (Safari private mode). The dialog still
    // opens, it just forgets.
    return "";
  }
}

export function saveLastProject(projectId: string) {
  try {
    localStorage.setItem(KEY, projectId);
  } catch {
    // Same: costs the memory, not the interaction.
  }
}

/** Prefer the open session's project, then the remembered project, then the first. */
export function initialProject(projects: { id: string }[], activeProjectId?: string): string {
  if (activeProjectId && projects.some((p) => p.id === activeProjectId)) return activeProjectId;
  const last = loadLastProject();
  if (last && projects.some((p) => p.id === last)) return last;
  return projects[0]?.id ?? "";
}
