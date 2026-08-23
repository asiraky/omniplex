// Folding events into SessionState. This mirrors internal/projection exactly:
// the server sends a snapshot or a replay, then live events, and applying them
// here must reach the same state the server holds.

import type { Event, Item, SessionState, TurnDiff } from "./protocol";

export function emptyState(sessionId: string): SessionState {
  return {
    sessionId,
    seq: 0,
    cwd: "",
    harness: "",
    model: "",
    mode: "",
    effort: "",
    title: "",
    phase: "idle",
    closed: false,
    workspace: { phase: "" },
    items: [],
    turns: [],
    plan: [],
    usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0 },
    pendingPermissions: [],
    pendingElicitations: [],
  };
}

function upsert(state: SessionState, id: string, mut: (it: Item) => void): Item[] {
  const i = state.items.findIndex((it) => it.id === id);
  if (i >= 0) {
    const next = { ...state.items[i] };
    mut(next);
    const items = state.items.slice();
    items[i] = next;
    return items;
  }
  const it: Item = { id, kind: "message" };
  mut(it);
  return [...state.items, it];
}

// applyEvent returns a new state. Events at or below the applied cursor are
// discarded, which is what makes at-least-once delivery safe.
/** Names a session whose first prompt was pictures and no words. Mirrors
    proto.ImageTitle in Go. */
function imageTitle(n: number): string {
  if (n === 0) return "";
  return n === 1 ? "1 image" : `${n} images`;
}

export function applyEvent(state: SessionState, ev: Event): SessionState {
  if (ev.seq <= state.seq) return state;
  const s: SessionState = { ...state, seq: ev.seq };
  const p = ev.payload ?? {};

  switch (ev.type) {
    case "session.created":
      return { ...s, cwd: p.cwd, harness: p.harness, model: p.model ?? "", mode: p.mode ?? "", effort: p.effort ?? "", title: p.title ?? "" };

    case "session.config_changed":
      return {
        ...s,
        model: p.model || s.model,
        mode: p.mode || s.mode,
        // "" is a value effort can be set to — the harness's own default —
        // so an event that carries it must win, which || would not let it.
        effort: p.effort ?? s.effort,
        title: p.title || s.title,
      };

    case "session.closed":
      return { ...s, closed: true, phase: "closed" };

    case "workspace.requested":
      return { ...s, phase: "provisioning", workspace: { phase: "provisioning", projectId: p.projectId, projectRoot: p.projectRoot, mode: p.mode, branch: p.branch, baseRef: p.baseRef, startedAt: ev.timestamp } };
    case "workspace.hook_started":
      return { ...s, phase: p.hook === "deprovision" ? "cleaning" : s.phase, workspace: { ...s.workspace, phase: p.hook === "deprovision" ? "cleaning" : s.workspace.phase, hook: p.hook, command: p.command } };
    case "workspace.hook_output":
      return { ...s, workspace: { ...s.workspace, output: (s.workspace.output ?? "") + (p.stream === "stderr" ? "[stderr] " : "") + (p.chunk ?? "") } };
    case "workspace.hook_finished":
      return { ...s, workspace: { ...s.workspace, exitCode: p.exitCode, durationMs: p.durationMs } };
    case "workspace.ready":
      return { ...s, cwd: p.cwd, phase: "idle", workspace: { ...s.workspace, phase: "ready", branch: p.branch, resources: p.resources, error: undefined } };
    case "workspace.failed":
      return { ...s, phase: "provision_failed", workspace: { ...s.workspace, phase: "provision_failed", error: p.error, exitCode: p.exitCode } };
    case "workspace.cleanup_started":
      return { ...s, phase: "cleaning", workspace: { ...s.workspace, phase: "cleaning", deleteAfterCleanup: !!p.purge } };
    case "workspace.cleanup_failed":
      return { ...s, phase: "cleanup_failed", workspace: { ...s.workspace, phase: "cleanup_failed", error: p.error, exitCode: p.exitCode } };
    case "workspace.cleanup_finished":
    case "workspace.released":
      return { ...s, workspace: { ...s.workspace, phase: "released" } };

    case "turn.started":
      return {
        ...s,
        phase: "turn",
        // A recovery prompt is the server talking to itself, so it never
        // names the session.
        title: s.title || (p.recovery ? "" : p.prompt?.slice(0, 60) || imageTitle(p.images?.length ?? 0)),
        turns: [...s.turns, { id: p.turnId, prompt: p.prompt, images: p.images, done: false, recovery: p.recovery, startedAt: ev.timestamp }],
        // A harness-initiated turn has no prompt — nobody asked anything —
        // so there is no prompt item to add. A prompt that is nothing but
        // pictures still has one.
        items: p.prompt || p.images?.length
          ? upsert(s, `prompt:${p.turnId}`, (it) => {
              it.kind = "message";
              it.receivedAt ??= ev.timestamp;
              it.role = "user";
              it.contentKind = "text";
              it.text = p.prompt;
              it.images = p.images;
              it.turnId = p.turnId;
            })
          : s.items,
      };

    case "turn.finished": {
      // Only the finish of the turn that is actually open may take the
      // session idle: a stale or duplicate finish must not report "user's
      // turn" while different work is running. Mirrors
      // internal/projection/state.go.
      const open = s.turns.reduce<string>((acc, t) => (t.done ? acc : t.id), "");
      const match = open === "" || p.turnId === open;
      return {
        ...s,
        phase: match ? "idle" : s.phase,
        turns: s.turns.map((t) =>
          t.id === p.turnId
            ? {
                ...t,
                done: true,
                stopReason: p.stopReason,
                error: p.error,
                failure: p.failure,
                finishedAt: ev.timestamp,
              }
            : t,
        ),
        // Any tool of this turn left mid-flight is no longer running; tools
        // of other turns are left alone.
        items: match
          ? s.items.map((it) =>
              it.kind === "tool" &&
              (it.status === "in_progress" || it.status === "pending") &&
              (it.turnId === p.turnId || !it.turnId)
                ? { ...it, status: "failed" as const }
                : it,
            )
          : s.items,
      };
    }

    case "turn.diff":
      // What the turn changed, measured by the server once the harness had
      // stopped writing. A turn id this client has never seen is nothing to
      // fold: the event describes a turn that is not in this projection.
      return {
        ...s,
        turns: s.turns.map((t) => (t.id === p.turnId ? { ...t, diff: p as TurnDiff } : t)),
      };

    case "message.chunk":
      return {
        ...s,
        // Streaming while idle means a turn is running that the log did not
        // announce. Trusting the activity over the phase keeps a lifecycle
        // desync from freezing the UI. Mirrors internal/projection/state.go.
        phase: s.phase === "idle" ? "turn" : s.phase,
        items: upsert(s, p.blockId, (it) => {
          it.kind = "message";
          it.receivedAt ??= ev.timestamp;
          it.role = p.role;
          it.contentKind = p.kind;
          it.turnId = p.turnId;
          if (p.parentToolCallId) it.parentId = p.parentToolCallId;
          it.text = (it.text ?? "") + p.delta;
        }),
      };

    case "tool_call.started":
      return {
        ...s,
        // Same defence as message.chunk: a tool starting is a turn running.
        phase: s.phase === "idle" ? "turn" : s.phase,
        items: upsert(s, p.toolCallId, (it) => {
          it.kind = "tool";
          it.receivedAt ??= ev.timestamp;
          it.turnId = p.turnId;
          it.toolKind = p.kind;
          it.title = p.title;
          it.status = p.status;
          it.input = p.rawInput;
          if (p.parentToolCallId) it.parentId = p.parentToolCallId;
        }),
      };

    case "tool_call.updated":
      return {
        ...s,
        // Same defence as message.chunk, but only for a tool going active: a
        // background tool's result straggling in after the turn ended must
        // not reopen "working". Mirrors internal/projection/state.go.
        phase: s.phase === "idle" && p.status === "in_progress" ? "turn" : s.phase,
        items: upsert(s, p.toolCallId, (it) => {
          it.kind = "tool";
          it.receivedAt ??= ev.timestamp;
          if (p.status) it.status = p.status;
          if (p.title) it.title = p.title;
          if (p.rawInput) it.input = p.rawInput;
          if (p.parentToolCallId) it.parentId = p.parentToolCallId;
          if (p.content?.length) it.content = [...(it.content ?? []), ...p.content];
        }),
      };

    case "plan.updated":
      return { ...s, plan: p.entries ?? [] };

    case "usage.updated":
      return { ...s, usage: p };

    case "context.compacted":
      // Anchored to the event's sequence so a replay lands the same item and
      // two boundaries never collide. No turn id: compaction is housekeeping,
      // so it stands on its own line rather than folding into a turn. Mirrors
      // internal/projection/state.go.
      return {
        ...s,
        items: upsert(s, `compact:${ev.seq}`, (it) => {
          it.kind = "notice";
          it.receivedAt ??= ev.timestamp;
          it.noticeKind = "compaction";
          it.trigger = p.trigger;
          it.preTokens = p.preTokens;
          it.postTokens = p.postTokens;
        }),
      };

    case "permission.requested":
      return {
        ...s,
        pendingPermissions: [
          ...s.pendingPermissions,
          {
            requestId: p.requestId,
            toolCallId: p.toolCallId,
            toolName: p.toolName,
            title: p.title,
            input: p.rawInput,
            options: p.options ?? [],
          },
        ],
      };

    case "permission.resolved":
      return {
        ...s,
        pendingPermissions: s.pendingPermissions.filter((x) => x.requestId !== p.requestId),
      };

	case "elicitation.requested":
		return {
			...s,
			pendingElicitations: [
				...(s.pendingElicitations ?? []),
				{ requestId: p.requestId, prompt: p.prompt, schema: p.schema ?? {} },
			],
		};

	case "elicitation.resolved":
		return {
			...s,
			pendingElicitations: (s.pendingElicitations ?? []).filter((x) => x.requestId !== p.requestId),
		};

    default:
      return s;
  }
}
