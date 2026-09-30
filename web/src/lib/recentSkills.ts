import type { ComposerItem } from "~/protocol";

// Recents are per project, not per thread and not global: which skills you
// reach for is a property of the work, and the same browser moves between
// projects whose catalogues barely overlap. Per browser rather than per user
// is the deliberate cheap choice — it matches how the client already keeps
// sidebar width, theme and panel state.
const KEY_PREFIX = "hy.recentSkills.v1:";
// No project (a thread whose meta has not landed yet) still gets a list, it
// just shares one drawer with every other project-less thread.
const NO_PROJECT = "~";
// How many are kept on disk. More than are shown, so that dropping one skill
// for a day does not lose it — it re-enters the list the moment the catalogue
// offers it again.
const STORED = 20;
// How many the empty transcript shows. Past about five it stops being a
// glanceable list and starts being a menu.
export const RECENT_LIMIT = 5;

function key(projectId: string | undefined): string {
  return `${KEY_PREFIX}${projectId || NO_PROJECT}`;
}

/** The insert texts this project reached for, most recent first. */
export function loadRecentSkills(projectId: string | undefined): string[] {
  try {
    const raw = localStorage.getItem(key(projectId));
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((entry): entry is string => typeof entry === "string").slice(0, STORED);
  } catch {
    // Corrupt entry, or storage blocked (Safari private mode). Recents are an
    // affordance, not state worth failing a render over.
    return [];
  }
}

/** Records one use, moving it to the front, and returns the new list. */
export function recordRecentSkill(projectId: string | undefined, insertText: string): string[] {
  const token = insertText.trim();
  if (!token) return loadRecentSkills(projectId);
  const next = [token, ...loadRecentSkills(projectId).filter((entry) => entry !== token)].slice(
    0,
    STORED,
  );
  try {
    localStorage.setItem(key(projectId), JSON.stringify(next));
  } catch {
    // Same as above: a full or blocked store costs the user the memory, not
    // the interaction.
  }
  return next;
}

/**
 * Resolves remembered insert texts against the catalogue this thread actually
 * has, newest first, and tops the list up from the catalogue when there is not
 * enough history to fill it — a brand new user has no recents at all, and an
 * empty transcript offering nothing is the dead space this replaces.
 *
 * Client actions (`/model` and friends) are never seeded: they are controls
 * that happen to live in the completion menu, not skills worth suggesting. A
 * remembered one is still resolved, because the user did reach for it.
 */
export function resolveRecentSkills(
  recent: string[],
  catalogue: ComposerItem[],
  limit = RECENT_LIMIT,
): ComposerItem[] {
  if (limit <= 0) return [];
  const byInsert = new Map<string, ComposerItem>();
  for (const item of catalogue) if (!byInsert.has(item.insertText)) byInsert.set(item.insertText, item);

  const picked: ComposerItem[] = [];
  const taken = new Set<string>();
  for (const token of recent) {
    if (picked.length >= limit) break;
    const item = byInsert.get(token);
    if (!item || taken.has(item.insertText)) continue;
    picked.push(item);
    taken.add(item.insertText);
  }
  // Skills first when there is nothing to remember: a catalogue is mostly
  // built-in commands (`/compact`, `/review`), and filling the list with those
  // would make the first-run nudge point at the harness rather than at the
  // work. Commands still get the leftover slots — they are legitimate first
  // tokens, and a remembered one is offered above either way.
  for (const pass of ["skill", "command"] as const) {
    for (const item of catalogue) {
      if (picked.length >= limit) break;
      if (item.kind !== pass) continue;
      if (taken.has(item.insertText) || item.behavior === "client-action") continue;
      picked.push(item);
      taken.add(item.insertText);
    }
  }
  return picked;
}
