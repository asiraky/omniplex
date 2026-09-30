import { useMemo } from "react";

import type { Item, ThreadState } from "~/protocol";
import { buildRows } from "~/rows";

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

/** What the transcript derives from the thread's state before it draws a row. */
export function useTranscriptModel(state: ThreadState) {
  // The provisioner is holding the transcript while it works, or while it
  // waits for an answer about a failure. Anything else — ready, released, or
  // never provisioned — leaves the empty state to speak.
  const workspaceOccupied =
    state.phase === "creating" ||
    state.phase === "provisioning" ||
    state.phase === "cleaning" ||
    state.phase === "provision_failed" ||
    state.phase === "cleanup_failed";
  const empty = state.items.length === 0 && !workspaceOccupied;

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

  // Sending a prompt should not leave it jammed against the composer with the
  // answer arriving in the sliver below. The newest prompt is lifted to the top
  // of the view instead, so the reply streams into the open space under it and
  // its first lines are readable the moment they land.
  const lastPromptID = useMemo(() => newestPrompt(ownItems), [ownItems]);

  const rows = useMemo(
    () => buildRows(ownItems, state.turns, state.phase),
    [ownItems, state.turns, state.phase],
  );

  // Only the newest turn can be continued, and only once nothing is running:
  // an error further back was already answered by whatever came after it.
  const interrupted = useMemo(() => {
    if (state.closed || state.phase === "turn") return undefined;
    const last = state.turns[state.turns.length - 1];
    return last?.done && last.stopReason === "error" ? last : undefined;
  }, [state.turns, state.phase, state.closed]);
  // A switch made since the limit hit: the notice it left is the newest item.
  const lastItem = state.items[state.items.length - 1];
  const switchedTo =
    lastItem?.kind === "notice" && lastItem.noticeKind === "account" ? lastItem.title : undefined;

  // What each turn changed, to be shown under the turn that changed it. A turn
  // that changed nothing has no entry, and gets no card.
  const turnDiffs = useMemo(
    () => new Map(state.turns.filter((t) => t.diff).map((t) => [t.id, t.diff!])),
    [state.turns],
  );
  const lastTurnID = state.turns[state.turns.length - 1]?.id;

  // Keyed by cause, not merely by "this was recovered": the note the reader
  // gets is a claim about what happened to their work, and only a restart is
  // allowed to claim one.
  const recoveredTurns = useMemo(
    () =>
      new Map(
        state.turns
          .filter((t) => t.recovery)
          .map((t) => [t.id, t.recovery!.cause ?? "restart"] as const),
      ),
    [state.turns],
  );

  return {
    empty,
    liveAgentId,
    lastPromptID,
    rows,
    interrupted,
    switchedTo,
    turnDiffs,
    lastTurnID,
    recoveredTurns,
  };
}
