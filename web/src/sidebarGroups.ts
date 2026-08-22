/**
 * Folding the flat session list into the user's label groups.
 *
 * Pure on purpose, like buildRows: the sidebar hands in whatever list it is
 * currently rendering — including the delete flow's frozen ordering — and gets
 * groups back, so grouping composes with the exit animation instead of
 * fighting it.
 *
 * The rules:
 * - A label earns a heading only by holding sessions. An empty label is still
 *   the user's structure, but it lives in the label manager, not as a row of
 *   sidebar chrome the user has to scroll past.
 * - Unlabelled sessions are not a category. They sit at the top, ungrouped and
 *   unheaded (`label: null`), exactly where they sat before labels existed. A
 *   session pointing at a label that no longer exists counts as unlabelled —
 *   the assignment broadcast can land a beat after the deletion broadcast.
 * - Within a group the incoming order holds (updated_at DESC upstream).
 * - If no label holds anything — none defined, or none used — there are no
 *   groups at all: the caller renders the flat list exactly as it always has.
 *   That is the whole promise of the feature to someone who ignores it.
 */

import type { Label, SessionMeta } from "~/protocol";

export interface SessionGroup {
  /** Null for the leading run of unlabelled sessions, which renders headerless. */
  label: Label | null;
  sessions: SessionMeta[];
}

export function buildGroups(sessions: SessionMeta[], labels: Label[]): SessionGroup[] | null {
  if (labels.length === 0) return null;

  const byLabel = new Map<string, SessionMeta[]>(labels.map((l) => [l.id, []]));
  const unlabelled: SessionMeta[] = [];
  for (const s of sessions) {
    const bucket = s.labelId ? byLabel.get(s.labelId) : undefined;
    if (bucket) bucket.push(s);
    else unlabelled.push(s);
  }

  const used = labels.filter((label) => byLabel.get(label.id)!.length > 0);
  // Nothing is actually filed: every heading would be empty, so there is no
  // structure to show. Hand back the flat list rather than a lone null group.
  if (used.length === 0) return null;

  const groups: SessionGroup[] = [];
  if (unlabelled.length > 0) groups.push({ label: null, sessions: unlabelled });
  for (const label of used) groups.push({ label, sessions: byLabel.get(label.id)! });
  return groups;
}
