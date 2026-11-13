import { useLayoutEffect, useMemo, useRef } from "react";

import type { Item, QueuedPrompt } from "~/protocol";
import { buildRows, reuseRows, type Row } from "~/rows";

import { liveActivity } from "./liveActivity";
import type { Recovered } from "./Message";
import type { TranscriptState } from "./transcriptState";

function newestAgentText(items: Item[]): Item | undefined {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === "message" && it.role === "agent" && (it.text ?? "").trim() !== "") return it;
  }
  return undefined;
}

function newestPrompt(items: Item[]): string | undefined {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === "message" && it.role === "user") return it.id;
  }
  return undefined;
}

/**
 * A turn stopped at a usage limit holds the queue on the server until a new
 * turn starts, and arms a continuation for when the limit resets. The hold is
 * for what was queued before the limit, and lifts once the thread moves to
 * another account: the limit was the old one's. Mirrors the server's
 * projection.State.QueueHeld and HeldByLimit.
 */
export function limitHold(state: Pick<TranscriptState, "scheduledPrompts" | "turns" | "items">) {
  const continuation = state.scheduledPrompts?.find(
    (p) => p.kind === "limit" && (p.status === "pending" || p.status === "ready"),
  );
  const last = state.turns[state.turns.length - 1];
  const finishedAt = last?.finishedAt ?? 0;
  let holding = last?.done === true && last.failure === "limit";
  for (let i = state.items.length - 1; holding && i >= 0; i--) {
    const it = state.items[i];
    if ((it.receivedAt ?? 0) < finishedAt) break;
    if (it.kind === "notice" && it.noticeKind === "account") holding = false;
  }
  const held: "reset" | "send" | undefined = holding ? (continuation ? "reset" : "send") : undefined;
  const heldFor = (q: QueuedPrompt) => ((q.queuedAt ?? 0) <= finishedAt ? held : undefined);
  return { continuation, heldFor };
}

function newestBesidesNotify(items: Item[]): Item | undefined {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (!(it.kind === "notice" && it.noticeKind === "notify")) return it;
  }
  return undefined;
}

/** What the transcript derives from the thread's state before it draws a row. */
export function useTranscriptModel(
  state: Pick<
    TranscriptState,
    | "phase"
    | "closed"
    | "items"
    | "itemsBefore"
    | "turns"
    | "scheduledPrompts"
    | "pendingPermissions"
    | "pendingElicitations"
  >,
) {
  // The provisioner is holding the transcript while it works, or while it
  // waits for an answer about a failure. Anything else — ready, released, or
  // never provisioned — leaves the empty state to speak.
  const workspaceOccupied =
    state.phase === "creating" ||
    state.phase === "provisioning" ||
    state.phase === "cleaning" ||
    state.phase === "provision_failed" ||
    state.phase === "cleanup_failed";
  // An empty window with history above it is not an empty thread: a rewind to
  // a turn above the window leaves exactly that while the page above loads.
  const empty = state.items.length === 0 && !(state.itemsBefore ?? 0) && !workspaceOccupied;

  // Only the final agent block is still growing; everything above it is
  // settled and renders in full. A block the harness opened but never filled is
  // not it: nothing of it is on screen, so treating it as the growing one would
  // leave the turn looking idle while the agent works. And a block whose turn
  // has finished is not it either — the newest text being settled means
  // nothing is streaming, however the phase got confused.
  // Work done inside a subagent narrates in the subagents surface, not here:
  // interleaving three agents' tool calls into one column reads as noise.
  const ownItems = useMemo(() => state.items.filter((it) => !it.parentId), [state.items]);

  const liveAgentId = useMemo(() => {
    const newest = newestAgentText(ownItems);
    if (!newest) return undefined;
    const turnId = newest.turnId;
    const turn = turnId ? state.turns.find((t) => t.id === turnId) : undefined;
    return turn?.done ? undefined : newest.id;
  }, [ownItems, state.turns]);

  // What the working line at the tail says, while a turn runs.
  const asking = (state.pendingPermissions?.length ?? 0) + (state.pendingElicitations?.length ?? 0) > 0;
  const activity = useMemo(
    () => liveActivity(ownItems, state.turns, state.phase, asking),
    [ownItems, state.turns, state.phase, asking],
  );

  // Sending a prompt should not leave it jammed against the composer with the
  // answer arriving in the sliver below. The newest prompt is lifted to the top
  // of the view instead, so the reply streams into the open space under it and
  // its first lines are readable the moment they land.
  const lastPromptID = useMemo(() => newestPrompt(ownItems), [ownItems]);

  // Rows that would draw the same as last time are the same objects as last
  // time, so a memoised row skips the frame (see reuseRows). The previous rows
  // are only a cache to match against: whatever the ref holds, reuseRows hands
  // back rows equal to a fresh build, so reading it here cannot render anything
  // wrong — a stale value only costs a missed reuse. It is written after commit
  // rather than here, so a render React throws away (StrictMode's second pass,
  // an interrupted transition) never becomes what the next one matches against.
  const prevRows = useRef<Row[]>([]);
  const rows = useMemo(
    () => reuseRows(prevRows.current, buildRows(ownItems, state.turns, state.phase)),
    [ownItems, state.turns, state.phase],
  );
  useLayoutEffect(() => {
    prevRows.current = rows;
  }, [rows]);

  // Only the newest turn can be continued, and only once nothing is running:
  // an error further back was already answered by whatever came after it.
  const interrupted = useMemo(() => {
    if (state.closed || state.phase === "turn") return undefined;
    const last = state.turns[state.turns.length - 1];
    return last?.done && last.stopReason === "error" ? last : undefined;
  }, [state.turns, state.phase, state.closed]);
  // A switch made since the limit hit: the notice it left is the newest item.
  // An extension's notice does not count as anything since: extensions speak
  // up as the switched session starts, which would hide the switch.
  const lastItem = newestBesidesNotify(state.items);
  const switchedTo =
    lastItem?.kind === "notice" && lastItem.noticeKind === "account" ? lastItem.title : undefined;

  // What each turn changed, to be shown under the turn that changed it. A turn
  // that changed nothing has no entry, and gets no card.
  const turnDiffs = useMemo(
    () => new Map(state.turns.filter((t) => t.diff).map((t) => [t.id, t.diff!])),
    [state.turns],
  );
  // Each turn's 1-based place in the thread, which is what the diff panel
  // calls it. The thread state holds every turn, not a page of them, so the
  // count is the real one.
  const turnNumbers = useMemo(() => new Map(state.turns.map((t, i) => [t.id, i + 1])), [state.turns]);
  const lastTurnID = state.turns[state.turns.length - 1]?.id;

  // Keyed by cause, not merely by "this was recovered": the note the reader
  // gets is a claim about what happened to their work, and only a restart is
  // allowed to claim one. The continuation armed at a usage limit is the
  // server's prompt too; the schedule that sent it names its turn.
  const recoveredTurns = useMemo(() => {
    const causes = new Map<string, Recovered>(
      state.turns.filter((t) => t.recovery).map((t) => [t.id, t.recovery!.cause ?? "restart"]),
    );
    for (const p of state.scheduledPrompts ?? []) {
      if (p.kind === "limit" && p.status === "sent" && p.turnId) causes.set(p.turnId, "limit");
    }
    return causes;
  }, [state.turns, state.scheduledPrompts]);

  const { continuation, heldFor } = limitHold(state);

  return {
    activity,
    continuation,
    heldFor,
    empty,
    liveAgentId,
    lastPromptID,
    rows,
    interrupted,
    switchedTo,
    turnDiffs,
    turnNumbers,
    lastTurnID,
    recoveredTurns,
  };
}
