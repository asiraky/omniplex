// Folding events into SessionState. This mirrors internal/projection exactly:
// the server sends a snapshot or a replay, then live events, and applying them
// here must reach the same state the server holds.

import type { Event, Item, Job, JobPayload, SessionState, TurnDiff } from "./protocol";
import { classifyJob, jobDone } from "./lib/jobs";

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
    jobs: [],
    plan: [],
    usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0 },
    pendingPermissions: [],
    pendingElicitations: [],
    queuedPrompts: [],
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

// applyJob folds one job.* row. Every row carries the linkage bundle, so a
// job whose start was never seen is created from whatever arrives first.
// Mirrors State.applyJob in internal/projection/state.go.
function applyJob(s: SessionState, ts: number, p: JobPayload, finished: boolean): SessionState {
  if (!p.jobId) return s;
  const jobs = [...s.jobs];
  let i = jobs.findIndex((j) => j.id === p.jobId);
  if (i < 0) {
    const turnId = s.turns.reduce<string | undefined>((acc, t) => (t.done ? acc : t.id), undefined);
    jobs.push({
      id: p.jobId,
      depth: 0,
      kind: p.kind ?? classifyJob(p.taskType),
      status: "running",
      usage: {},
      startedAt: ts,
      turnId,
    });
    i = jobs.length - 1;
  }
  const j: Job = { ...jobs[i], usage: { ...jobs[i].usage } };
  jobs[i] = j;
  if (p.toolCallId) j.toolCallId = p.toolCallId;
  if (p.parentJobId) {
    j.parentJobId = p.parentJobId;
    const parent = jobs.find((x) => x.id === p.parentJobId);
    if (parent) j.depth = parent.depth + 1;
  }
  if (p.kind) j.kind = p.kind;
  if (p.taskType) j.taskType = p.taskType;
  if (p.name) j.name = p.name;
  if (p.role) j.role = p.role;
  if (p.workflowName) j.workflowName = p.workflowName;
  if (p.activity) j.activity = p.activity;
  if (p.usage) {
    // Running totals: a row reporting less than we have is a partial tick.
    if (p.usage.totalTokens) j.usage.totalTokens = p.usage.totalTokens;
    if (p.usage.toolUses) j.usage.toolUses = p.usage.toolUses;
    if (p.usage.durationMs) j.usage.durationMs = p.usage.durationMs;
    if (p.usage.cost) j.usage.cost = p.usage.cost;
  }
  if (p.error) j.error = p.error;
  if (p.outputFile) j.outputFile = p.outputFile;
  if (p.backgrounded !== undefined) j.backgrounded = p.backgrounded;
  if (p.hidden) j.hidden = true;
  if (p.status) j.status = p.status;
  let items = s.items;
  if (finished) {
    if (!jobDone(j.status)) j.status = "completed";
    j.finishedAt ??= ts;
    // The spawning tool call, if still waiting on this job, is settled by it.
    if (j.toolCallId) {
      const settled =
        j.status === "failed" ? "failed" : j.status === "completed" ? "completed" : "cancelled";
      items = s.items.map((it) =>
        it.id === j.toolCallId && (it.status === "in_progress" || it.status === "pending")
          ? { ...it, status: settled as Item["status"] }
          : it,
      );
    }
  } else if (jobDone(j.status)) {
    j.finishedAt ??= ts;
  }
  return { ...s, jobs, items };
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
        // Starting is what takes a prompt out of the queue.
        queuedPrompts: p.queueId ? (s.queuedPrompts ?? []).filter((q) => q.queueId !== p.queueId) : s.queuedPrompts,
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

    case "prompt.queued":
      return {
        ...s,
        queuedPrompts: [...(s.queuedPrompts ?? []), { queueId: p.queueId, prompt: p.prompt, images: p.images, queuedAt: ev.timestamp }],
      };

    case "prompt.dequeued":
      return { ...s, queuedPrompts: (s.queuedPrompts ?? []).filter((q) => q.queueId !== p.queueId) };

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
        // Any tool of this turn left mid-flight was interrupted, not broken:
        // cancelled, never failed. A call standing for a live job is left
        // alone — the job's own finish settles it. Tools of other turns are
        // left alone too.
        items: match
          ? s.items.map((it) =>
              it.kind === "tool" &&
              (it.status === "in_progress" || it.status === "pending") &&
              (it.turnId === p.turnId || !it.turnId) &&
              !s.jobs.some((j) => !jobDone(j.status) && j.toolCallId === it.id)
                ? { ...it, status: "cancelled" as const }
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
      // A straggler for a call that was windowed out of the transcript — a
      // background tool finishing long after its turn scrolled away. Upserting
      // would append an orphan at the tail, and paging that history back in
      // would then prepend the original under the same id. The server has
      // already folded this event, so the page fetch delivers the updated
      // item; here only the cursor moves.
      if ((s.itemsBefore ?? 0) > 0 && !s.items.some((it) => it.id === p.toolCallId)) {
        return s;
      }
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

    case "job.started":
    case "job.updated":
      return applyJob(s, ev.timestamp, p as JobPayload, false);

    case "job.finished":
      return applyJob(s, ev.timestamp, p as JobPayload, true);

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
