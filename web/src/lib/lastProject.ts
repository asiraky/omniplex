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

/**
 * The project the dialog should open on: the remembered one while it still
 * exists, else the server's first. Projects that have been deleted, or that
 * this device cannot see, fall back rather than leaving the dialog on nothing.
 */
export function initialProject(projects: { id: string }[]): string {
  const last = loadLastProject();
  if (last && projects.some((p) => p.id === last)) return last;
  return projects[0]?.id ?? "";
}
