import type { PermissionLevel, PermissionModeMeta } from "~/protocol";

/**
 * The three permission levels every harness maps its own modes onto, least
 * trusting first. `short` is for a chip, where the full name does not fit on
 * a phone.
 */
export const LEVELS: { id: PermissionLevel; label: string; short: string }[] = [
  { id: "ask", label: "Ask before changing anything", short: "Ask first" },
  { id: "edits", label: "Edit files, ask before commands", short: "Edit files" },
  { id: "all", label: "Do everything", short: "Do everything" },
];

/** The harness's mode for a level, or "" when it has none. */
export function modeForLevel(modes: PermissionModeMeta[], level: string | undefined): string {
  if (!level) return "";
  return modes.find((m) => m.level === level)?.id ?? "";
}
