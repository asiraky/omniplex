/**
 * What a sidebar row says about its thread, reduced to the one thing the eye
 * needs from a list: is there something here I have not seen?
 *
 * - `busy`: a turn, the workspace lifecycle, or a subagent or monitor the agent
 *   will come back from. Nobody needs to look; the row stays quiet. A live
 *   shell (a dev server) does not count — the server leaves it out.
 * - `new`: the agent has stopped and the log has moved past what anyone has
 *   read. A question or a permission request lands here too: whether the agent
 *   asked through a tool or in prose, it is the same "go and look".
 * - `failed`: the workspace could not be set up or cleaned up.
 * - `quiet`: read, idle, nobody waiting.
 */

import type { ThreadMeta } from "~/protocol";

export type RowStatus = "busy" | "new" | "failed" | "quiet";

/** A turn, the workspace lifecycle, or a subagent or monitor still out. */
export function busy(s: ThreadMeta) {
  return s.attention === "working" || s.attention === "background";
}

/**
 * The log has moved past what anyone has read, on any paired device.
 * lastViewedSeq is absent on a server that predates it; treating that as seq 0
 * would light every row, so an absent cursor reads as all-read.
 */
export function unread(s: ThreadMeta) {
  return s.lastViewedSeq !== undefined && s.headSeq > s.lastViewedSeq;
}

export function rowStatus(s: ThreadMeta): RowStatus {
  if (s.attention === "failed") return "failed";
  // A running thread's log moves with every token, so it is always unread;
  // announcing that would light every busy row. It says `new` once it stops.
  if (busy(s)) return "busy";
  if (unread(s)) return "new";
  return "quiet";
}

/** How many of these threads would show the `new` badge. The thread on screen
    is being read, so it never counts. */
export function newCount(threads: ThreadMeta[], activeId: string | null) {
  return threads.filter((s) => s.id !== activeId && rowStatus(s) === "new").length;
}

/** How loud a thread's status is: the colour of its dot on the hover card. */
export type AttentionTone = "busy" | "waiting" | "failed" | "quiet";

/**
 * What the thread is doing, in words. The row says it with a ring or a badge;
 * the hover card has room to say which kind of waiting it is. Null when the
 * server sends no attention signal, rather than guessing from the phase.
 */
export function attentionLabel(s: ThreadMeta): { label: string; tone: AttentionTone } | null {
  switch (s.attention) {
    case "working":
      return { label: "Working", tone: "busy" };
    case "background":
      return { label: "Background task running", tone: "busy" };
    case "needs_permission":
      return { label: "Waiting for permission", tone: "waiting" };
    case "needs_answer":
      return { label: "Waiting for your answer", tone: "waiting" };
    case "needs_prompt":
      return s.turnFailed
        ? { label: "Last turn failed", tone: "failed" }
        : { label: "Finished", tone: "quiet" };
    case "failed":
      return { label: "Workspace failed", tone: "failed" };
    case "closed":
      return { label: "Closed", tone: "quiet" };
    default:
      return null;
  }
}
