/**
 * When a thread you are not looking at deserves a toast, and what it says.
 * The rule is a diff between two sightings of the same thread's meta, so it
 * fires on the change and never on a state the thread was already in.
 */

import type { AlertSound } from "~/lib/alertSound";
import type { ThreadMeta } from "~/protocol";
import { busy } from "~/threadStatus";

export type AlertKind = "finished" | "failed" | "permission" | "question";

export interface Alert {
  kind: AlertKind;
}

/**
 * What changed between the last sighting of a thread and this one that is
 * worth interrupting for, or null.
 *
 * A first sighting never alerts: on load, or for a thread just created, there
 * is no "before" to have changed from. A waiting state alerts on arrival only,
 * so a thread sitting on an approval says so once. A turn ending alerts only
 * from busy: a thread that leaves an approval or a question for needs_prompt
 * was answered by someone, maybe on another device, and is not news.
 */
export function alertFor(prev: ThreadMeta | undefined, next: ThreadMeta): Alert | null {
  if (!prev) return null;
  const was = prev.attention;
  switch (next.attention) {
    case "needs_permission":
      return was === "needs_permission" ? null : { kind: "permission" };
    case "needs_answer":
      return was === "needs_answer" ? null : { kind: "question" };
    case "failed":
      return was === "failed" ? null : { kind: "failed" };
    case "needs_prompt":
      if (!busy(prev)) return null;
      return { kind: next.turnFailed ? "failed" : "finished" };
    default:
      return null;
  }
}

export interface AlertToast {
  variant: "success" | "error" | "warning";
  title: string;
  /** The thread's name, as the tab title shows it. */
  description: string;
  sound: AlertSound;
  /** How long it stays up, in ms. */
  duration: number;
}

// A toast that asks for you stays up longer than one that only reports: on a
// phone it is often caught in a glance up from something else, and a thread
// blocked on an approval loses more to a missed toast than a finished one.
const NEEDS_YOU_MS = 10_000;
const REPORT_MS = 6_000;

const content: Record<AlertKind, Pick<AlertToast, "variant" | "title" | "sound" | "duration">> = {
  finished: { variant: "success", title: "Thread finished", sound: "done", duration: REPORT_MS },
  failed: { variant: "error", title: "Thread failed", sound: "done", duration: REPORT_MS },
  permission: {
    variant: "warning",
    title: "Approval needed",
    sound: "needs",
    duration: NEEDS_YOU_MS,
  },
  question: { variant: "warning", title: "Question waiting", sound: "needs", duration: NEEDS_YOU_MS },
};

export function alertToast(alert: Alert, thread: ThreadMeta): AlertToast {
  return { ...content[alert.kind], description: thread.title?.trim() || "Untitled thread" };
}
