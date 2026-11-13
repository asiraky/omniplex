import type { ThreadState } from "~/protocol";

/**
 * The part of a thread's state the transcript draws. Not jobs, usage, the
 * plan or the pending prompts: those tick far more often than the
 * conversation does, and the parts that show them select them on their own.
 * The schedules are in, for the continuation a usage limit arms and the turns
 * it sent. The questions waiting on the user are in, for the working line to
 * say so. Not seq either, which every frame bumps.
 */
export type TranscriptState = Pick<
  ThreadState,
  | "threadId"
  | "phase"
  | "closed"
  | "workspace"
  | "items"
  | "itemsBefore"
  | "turns"
  | "queuedPrompts"
  | "artefacts"
  | "scheduledPrompts"
  | "pendingPermissions"
  | "pendingElicitations"
>;

/**
 * The transcript's fields picked out of the whole state. A frame that changes
 * none of them leaves every one the same object (apply.ts copies only what an
 * event touches), so a shallow comparison of two picks says whether there is
 * anything new to draw.
 */
export function transcriptState(s: ThreadState | null): TranscriptState | null {
  return (
    s && {
      threadId: s.threadId,
      phase: s.phase,
      closed: s.closed,
      workspace: s.workspace,
      items: s.items,
      itemsBefore: s.itemsBefore,
      turns: s.turns,
      queuedPrompts: s.queuedPrompts,
      artefacts: s.artefacts,
      scheduledPrompts: s.scheduledPrompts,
      pendingPermissions: s.pendingPermissions,
      pendingElicitations: s.pendingElicitations,
    }
  );
}
