// How the providers screen describes a harness CLI's last background
// self-update. Pure, so the branch it picks is testable without rendering.

import { formatAge } from "~/lib/usageFormat";
import type { HarnessUpdate } from "~/protocol";

export interface UpdateLine {
  /** "attention" for a failure, which needs looking at; "muted" otherwise. */
  tone: "muted" | "attention";
  text: string;
}

/**
 * One short line for the last update attempt, or null when none has run. A
 * failure wins over any versions it carries; equal versions read as a check
 * that found nothing to do.
 */
export function describeHarnessUpdate(update: HarnessUpdate | undefined, now: number): UpdateLine | null {
  if (!update) return null;
  const age = formatAge(update.at, now);
  if (update.error) {
    return { tone: "attention", text: `Auto-update failed · ${age}: ${update.error}` };
  }
  if (update.to && update.from !== update.to) {
    const what = update.from ? `${update.from} → ${update.to}` : `to ${update.to}`;
    return { tone: "muted", text: `Updated ${what} · ${age}` };
  }
  return { tone: "muted", text: `Up to date · checked ${age}` };
}
