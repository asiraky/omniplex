// Folding events into ThreadState. This mirrors internal/projection exactly:
// the server sends a snapshot or a replay, then live events, and applying them
// here must reach the same state the server holds.

import type {
  Artefact,
  CardOutcome,
  Event,
  ExtensionStatus,
  Item,
  Job,
  JobPayload,
  ThreadState,
  ToolContent,
  TurnDiff,
  TurnSettings,
} from "./protocol";
import { classifyJob, jobDone } from "./lib/jobs";
import { isTodoPlan, planItemId } from "./lib/plans";
import { appendTail } from "./lib/tail";

/** Splices a page of older items in front of the window. A straggling event
    for a trimmed item is dropped rather than applied, but belt and braces:
    never prepend an id the window already holds — duplicate ids break
    rendering outright. The page may have been read before the newest
    TodoWrite list landed, and every list in it is older than any the window
    holds: one here supersedes them all, as it did on the server. */
export function prependItems(items: Item[], older: Item[]): Item[] {
  const have = new Set(items.map((it) => it.id));
  const newer = items.some(isTodoPlan);
  const page = older
    .filter((it) => !have.has(it.id))
    .map((it) => (newer && isTodoPlan(it) && !it.superseded ? { ...it, superseded: true } : it));
  return [...page, ...items];
}

export function emptyState(threadId: string): ThreadState {
  return {
    threadId,
    seq: 0,
    cwd: "",
    harness: "",
    model: "",
    mode: "",
    effort: "",
    serviceTier: "",
    title: "",
    phase: "idle",
    closed: false,
    workspace: { phase: "" },
    items: [],
    turns: [],
    jobs: [],
    usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, cost: 0 },
    pendingPermissions: [],
    pendingElicitations: [],
    queuedPrompts: [],
    extensionStatuses: [],
  };
}

function upsert(state: ThreadState, id: string, mut: (it: Item) => void): Item[] {
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

/** Whether a tool call's status is terminal: it has stopped running, one way
    or another. Mirrors proto.ToolDone in internal/proto/event.go. */
function toolDone(status: Item["status"]): boolean {
  return status === "completed" || status === "failed" || status === "cancelled";
}

/** The tool calls standing for jobs still running. */
function liveJobCalls(jobs: Job[]): Set<string> {
  return new Set(jobs.flatMap((j) => (j.toolCallId && !jobDone(j.status) ? [j.toolCallId] : [])));
}

/** The call without its tail: absent rather than empty, as the server's
    omitempty leaves it. */
function dropTail(it: Item): Item {
  const { tail: _tail, ...rest } = it;
  return rest;
}

/** A tool call moved to a terminal status. The tail goes with it: it only
    ever stands for output still arriving. */
function settle(it: Item, status: Item["status"]): Item {
  return { ...dropTail(it), status };
}

// The server keeps only a head and tail of each tool output in its projection
// and serves the rest on demand (GET /api/threads/{id}/items/{itemId}/content).
// Live events and replays arrive capped too, but the reducer caps what it
// appends the same way regardless: entries accumulate across updates, and one
// long session's tool output must not sit in the tab's memory. Mirrors
// internal/projection. Go measures bytes and this measures UTF-16 units, so
// the two can cut at slightly different points; that is fine, since a capped
// preview is shorter than the cap in either unit and the full text is one
// fetch away.
export const TOOL_HEAD = 4096;
export const TOOL_TAIL = 4096;
export const TOOL_MAX_ENTRIES = 16;
// How much text one item's content keeps in all: an entry is kept while the
// entries before it hold less.
export const TOOL_MAX_TEXT = 32 * 1024;
const TOOL_SLACK = 128;
// How far a cut may move to land on a line break.
const TOOL_SNAP = 512;

const highSurrogate = (c: number) => c >= 0xd800 && c <= 0xdbff;
const lowSurrogate = (c: number) => c >= 0xdc00 && c <= 0xdfff;

/** The head and tail of a long string, or the string itself if it is short.
    Idempotent: a capped string is under the threshold. */
export function capToolText(s: string): string {
  if (s.length <= TOOL_HEAD + TOOL_TAIL + TOOL_SLACK) return s;
  // Each cut moves inward to a line break when one is near, so the preview
  // never shows part of a line as if it were whole; failing that, past half a
  // surrogate pair so no character is split.
  let head = TOOL_HEAD;
  const nl = s.lastIndexOf("\n", head - 1);
  if (nl >= head - TOOL_SNAP) head = nl + 1;
  else if (highSurrogate(s.charCodeAt(head - 1))) head--;
  let tail = s.length - TOOL_TAIL;
  const tnl = s.indexOf("\n", tail);
  if (tnl >= 0 && tnl < tail + TOOL_SNAP) tail = tnl + 1;
  else if (lowSurrogate(s.charCodeAt(tail))) tail++;
  return `${s.slice(0, head)}\n\n[… ${tail - head} characters truncated …]\n\n${s.slice(tail)}`;
}

function appendToolContent(it: Item, add: ToolContent[]) {
  const have = it.content ?? [];
  const room = Math.max(0, TOOL_MAX_ENTRIES - have.length);
  let cut = add.length > room;
  let used = have.reduce((n, c) => n + contentText(c), 0);
  const capped: ToolContent[] = [];
  for (const c of add.slice(0, room)) {
    // An image is a URL with no text, and a closed fold's strip shows only
    // what the preview kept: keep it.
    if (used >= TOOL_MAX_TEXT && contentText(c) > 0) {
      cut = true;
      continue;
    }
    const next = { ...c };
    for (const key of ["text", "oldText", "newText"] as const) {
      const v = next[key];
      if (v === undefined) continue;
      const short = capToolText(v);
      if (short !== v) {
        next[key] = short;
        cut = true;
      }
    }
    used += contentText(next);
    capped.push(next);
  }
  if (capped.length) it.content = [...have, ...capped];
  if (cut) it.truncated = true;
}

const contentText = (c: ToolContent) => (c.text?.length ?? 0) + (c.oldText?.length ?? 0) + (c.newText?.length ?? 0);

// markSettings draws the line where a turn starts on a model or effort other
// than the last turn's. It is drawn at the turn rather than at the switch, so
// a switch the harness takes only at the next turn, a run of switches, and a
// switch undone all read right. Mirrors internal/projection/state.go.
function markSettings(state: ThreadState, ev: Event): ThreadState {
  const now = settings(state);
  const was = state.ranWith;
  if (!was || sameSettings(was, now)) return { ...state, ranWith: now };
  return {
    ...state,
    ranWith: now,
    items: upsert(state, `model:${ev.seq}`, (it) => {
      it.kind = "notice";
      it.receivedAt ??= ev.timestamp;
      it.noticeKind = "model";
      it.model = now.model;
      it.effort = now.effort;
      it.from = was;
    }),
  };
}

function settings(state: ThreadState): TurnSettings {
  return { model: state.model, effort: state.effort };
}

function sameSettings(a: TurnSettings, b: TurnSettings): boolean {
  return a.model === b.model && a.effort === b.effort;
}

// applyArtefact folds artefact.shown: the artefact as it is now replaces what
// was known of it, and anything the agent showed gets a timeline item at the
// point it was shown. Uploads get no item; they show on the message that
// carried them. Mirrors State.applyArtefact in internal/projection/state.go.
type ArtefactShown = Omit<Artefact, "id" | "shownAt"> & { artefactId: string };

function applyArtefact(s: ThreadState, ts: number, seq: number, p: ArtefactShown): ThreadState {
  const { artefactId, ...rest } = p;
  const artefact: Artefact = {
    ...rest,
    id: artefactId,
    turnId: p.turnId || undefined,
    inline: p.inline || undefined,
    shownAt: ts,
  };
  const list = s.artefacts ?? [];
  const i = list.findIndex((a) => a.id === artefactId);
  const artefacts = i >= 0 ? list.map((a, j) => (j === i ? artefact : a)) : [...list, artefact];
  if (p.source === "upload") return { ...s, artefacts };
  return {
    ...s,
    artefacts,
    items: upsert(s, `artefact:${artefactId}@${seq}`, (it) => {
      it.kind = "artefact";
      it.turnId = p.turnId || undefined;
      it.receivedAt ??= ts;
      it.title = p.name;
      it.artefactId = artefactId;
      it.mediaType = p.mediaType;
      it.size = p.size;
      it.inline = p.inline || undefined;
    }),
  };
}

// revivesUnheldJob says whether ev brings back to life a job the snapshot left
// out: a Codex subagent reopened long after it finished. applyJob drops the
// row like any straggler, so the caller fetches the whole list, which the
// server has already folded the row into; otherwise the running agent would be
// missing from the roster, and its Stop with it, until the next attach.
export function revivesUnheldJob(s: ThreadState, ev: Event): boolean {
  if (ev.type !== "job.updated" || !dropsJobRow(s, ev)) return false;
  const p = (ev.payload ?? {}) as JobPayload;
  return !!p.status && !jobDone(p.status);
}

// dropsJobRow says whether applyJob drops ev as a straggler for a job s left
// out. A row dropped while the list is being fetched may postdate the
// server's read of it, so the caller holds it for applyJobRow.
export function dropsJobRow(s: ThreadState, ev: Event): boolean {
  if (!ev.type.startsWith("job.") || ev.type === "job.started" || !(s.jobsBefore ?? 0)) return false;
  const p = (ev.payload ?? {}) as JobPayload;
  return !!p.jobId && !s.jobs.some((j) => j.id === p.jobId);
}

// applyJobRow folds a job row the state's cursor is already past: one
// dropsJobRow dropped while the job list was on its way, applied over the
// list. Never a start, which is never dropped.
export function applyJobRow(s: ThreadState, ev: Event): ThreadState {
  const row = ev.type === "job.finished" ? "finished" : "updated";
  return applyJob(s, ev.timestamp, (ev.payload ?? {}) as JobPayload, row);
}

// applyExtensionStatus sets one key's status line, updating it in place so a
// chip that changes text does not jump along the header; empty text removes
// it. Mirrors State.applyExtensionStatus in internal/projection/state.go.
function applyExtensionStatus(list: ExtensionStatus[], key: string, text: string): ExtensionStatus[] {
  const i = list.findIndex((x) => x.key === key);
  if (!text) return i >= 0 ? list.filter((_, j) => j !== i) : list;
  if (i >= 0) return list.map((x, j) => (j === i ? { key, text } : x));
  return [...list, { key, text }];
}

// applyJob folds one job.* row. Every row carries the linkage bundle, so a
// job whose start was never seen is created from whatever arrives first.
// Mirrors State.applyJob in internal/projection/state.go.
function applyJob(s: ThreadState, ts: number, p: JobPayload, row: "started" | "updated" | "finished"): ThreadState {
  if (!p.jobId) return s;
  const jobs = [...s.jobs];
  let i = jobs.findIndex((j) => j.id === p.jobId);
  // A straggler for a job the snapshot left out — a finished one that was not
  // among the latest. Creating it from this row would put a stub with no name
  // or start in the roster, and fetching the full list would then hold the
  // real one beside it. The server has folded this row, so the list fetch
  // delivers it; here only the cursor moves. A start is always new work.
  if (i < 0 && row !== "started" && (s.jobsBefore ?? 0) > 0) return s;
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
  if (p.status) {
    // A finished job reported running again was reopened (a Codex subagent
    // taking a new turn): its elapsed clock runs again.
    if (row !== "finished" && !jobDone(p.status)) j.finishedAt = undefined;
    j.status = p.status;
  }
  let items = s.items;
  if (row === "finished") {
    if (!jobDone(j.status)) j.status = "completed";
    j.finishedAt ??= ts;
    // The spawning tool call, if still waiting on this job, is settled by it.
    // So is the job's own work left mid-flight (a stopped subagent's command):
    // its parent's turn finishing spared it for the job, and the job is over.
    // A real completion arriving later still lands. Mirrors
    // internal/projection/state.go.
    if (j.toolCallId) {
      const call = j.toolCallId;
      const settled =
        j.status === "failed" ? "failed" : j.status === "completed" ? "completed" : "cancelled";
      // A live grandchild's spawn call is still its job's to settle.
      const live = liveJobCalls(jobs);
      items = s.items.map((it) => {
        if (it.status !== "in_progress" && it.status !== "pending") return it;
        if (it.id === call) return settle(it, settled);
        if (it.kind === "tool" && it.parentId === call && !live.has(it.id)) return settle(it, "cancelled");
        return it;
      });
    }
  } else if (jobDone(j.status)) {
    j.finishedAt ??= ts;
  }
  return { ...s, jobs, items };
}

// applyEvent returns a new state. Events at or below the applied cursor are
// discarded, which is what makes at-least-once delivery safe.
/** Names a thread whose first prompt was pictures and no words. Mirrors
    proto.ImageTitle in Go. */
function imageTitle(n: number): string {
  if (n === 0) return "";
  return n === 1 ? "1 image" : `${n} images`;
}

// The permissions and questions a finished turn was blocked on are dropped:
// the harness stopped waiting once its turn was over. A request naming no turn
// is left alone, and so are cards, which outlive the turn. Mirrors
// internal/projection/state.go.
function settleRequests(
  s: ThreadState,
  turnId: string | undefined,
): Pick<ThreadState, "pendingPermissions" | "pendingElicitations"> {
  const pendingPermissions = s.pendingPermissions;
  const pendingElicitations = s.pendingElicitations ?? [];
  if (!turnId) return { pendingPermissions, pendingElicitations };
  return {
    pendingPermissions: pendingPermissions.filter((x) => x.turnId !== turnId),
    pendingElicitations: pendingElicitations.filter((x) => x.card || x.turnId !== turnId),
  };
}

// thread.rewound cuts the thread back to before a turn: the items from its
// first one on, that turn and every turn after it, and the jobs those turns
// spawned. Queued prompts, artefacts and the settings baseline are the
// thread's and stay. Mirrors State.rewind in internal/projection/state.go.
//
// `at` indexes the server's full item list, and the window here is not a
// contiguous copy of it (a settled parent's children are left out), so the
// cut is found by the turn's own first item, as the server found `at`
// (State.RewindAt). A turn that starts above the window takes the whole window
// with it; paging older items then carries on from `at`.
function rewind(s: ThreadState, turnId: string, at: number): ThreadState {
  const i = s.turns.findIndex((t) => t.id === turnId);
  const removed = new Set(i < 0 ? [turnId] : s.turns.slice(i).map((t) => t.id));
  // The turn's own first item, else (a turn that wrote none) the first of a
  // turn after it, as the server's cut falls.
  let first = s.items.findIndex((it) => it.turnId === turnId);
  if (first < 0) first = s.items.findIndex((it) => !!it.turnId && removed.has(it.turnId));
  const before = s.itemsBefore ?? 0;
  let items = s.items;
  let itemsBefore = s.itemsBefore;
  if (typeof at === "number" && at < before) {
    items = [];
    itemsBefore = Math.max(0, at);
  } else if (first >= 0) {
    items = s.items.slice(0, first);
  }
  if (i < 0) return { ...s, items, itemsBefore };
  return {
    ...s,
    items,
    itemsBefore,
    turns: s.turns.slice(0, i),
    jobs: s.jobs.filter((j) => !j.turnId || !removed.has(j.turnId)),
  };
}

export function applyEvent(state: ThreadState, ev: Event): ThreadState {
  if (ev.seq <= state.seq) return state;
  const s: ThreadState = { ...state, seq: ev.seq };
  const p = ev.payload ?? {};

  switch (ev.type) {
    case "thread.created":
      return { ...s, cwd: p.cwd, harness: p.harness, model: p.model ?? "", mode: p.mode ?? "", effort: p.effort ?? "", serviceTier: p.serviceTier ?? "", title: p.title ?? "" };

    case "thread.config_changed": {
      // A harness reporting its own settings as it (re)starts or takes a
      // prompt names the bare model id for the alias that was picked: not a
      // switch. One that lands while a model switch waits for its turn can
      // predate it, so it leaves the pick alone; otherwise it moves the
      // baseline's model with it. Mirrors internal/projection/state.go.
      const report = !!p.harnessSessionId;
      const pending = !!s.ranWith && s.ranWith.model !== s.model;
      const model = p.replaceSettings ? p.model ?? "" : p.model || s.model;
      const next: ThreadState = {
        ...s,
        model: report && pending ? s.model : model,
        mode: p.replaceSettings ? p.mode ?? "" : p.mode || s.mode,
        // "" is a value effort can be set to — the harness's own default —
        // so an event that carries it must win, which || would not let it.
        effort: p.effort ?? s.effort,
        // The same goes for the service tier: "" turns Fast off.
        serviceTier: p.serviceTier ?? s.serviceTier,
        title: p.title || s.title,
      };
      if (report && s.ranWith && !pending) return { ...next, ranWith: { ...s.ranWith, model: next.model } };
      return next;
    }

    case "thread.closed":
      // Nothing keeps a closed thread's extension statuses (state.go).
      return { ...s, closed: true, phase: "closed", extensionStatuses: [] };

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

    case "turn.started": {
      const marked = markSettings(s, ev);
      // A turn the harness started from a prompt it was holding names the
      // queue entry but not what it carried: the entry has the text and
      // the images, the event may have only the text.
      const held = p.queueId ? (marked.queuedPrompts ?? []).find((q) => q.queueId === p.queueId) : undefined;
      const prompt: string = p.prompt || held?.prompt || "";
      const images = p.images?.length ? p.images : held?.images;
      return {
        ...marked,
        phase: "turn",
        // A suggestion answers the turn before this one. Mirrors
        // internal/projection/state.go.
        suggestion: undefined,
        // A recovery prompt is the server talking to itself, so it never
        // names the thread.
        title: marked.title || (p.recovery ? "" : prompt.slice(0, 60) || imageTitle(images?.length ?? 0)),
        turns: [...marked.turns, { id: p.turnId, prompt, images, done: false, recovery: p.recovery, startedAt: ev.timestamp }],
        // Starting is what takes a prompt out of the queue.
        scheduledPrompts: (marked.scheduledPrompts ?? []).map(q => q.id === p.queueId ? {...q, status: "sent", turnId: p.turnId, revision: q.revision + 1} : q),
        queuedPrompts: p.queueId ? (marked.queuedPrompts ?? []).filter((q) => q.queueId !== p.queueId) : marked.queuedPrompts,
        // A harness-initiated turn has no prompt — nobody asked anything —
        // so there is no prompt item to add. A prompt that is nothing but
        // pictures still has one.
        items: prompt || images?.length
          ? upsert(marked, `prompt:${p.turnId}`, (it) => {
              it.kind = "message";
              it.receivedAt ??= ev.timestamp;
              it.role = "user";
              it.contentKind = "text";
              it.text = prompt;
              it.images = images;
              it.turnId = p.turnId;
            })
          : marked.items,
      };
    }

    case "prompt.scheduled":
      return { ...s, scheduledPrompts: [...(s.scheduledPrompts ?? []).filter(q => q.id !== p.id), p] };
    case "prompt.queued":
      return {
        ...s,
        queuedPrompts: [...(s.queuedPrompts ?? []), { queueId: p.queueId, prompt: p.prompt, images: p.images, queuedAt: ev.timestamp, held: p.held }],
      };

    case "prompt.sent":
      // The harness took a queued prompt mid-turn, so nothing holds it any
      // more. One it already read has left the queue, and there is nothing to
      // mark. Mirrors internal/projection/state.go.
      return {
        ...s,
        queuedPrompts: (s.queuedPrompts ?? []).map((q) =>
          q.queueId === p.queueId ? { ...q, sent: true, held: false } : q,
        ),
      };

    case "prompt.edited":
      // The new text and pictures replace the old outright: no images in the
      // edit means it has none now. Mirrors internal/projection/state.go.
      return {
        ...s,
        queuedPrompts: (s.queuedPrompts ?? []).map((q) =>
          q.queueId === p.queueId ? { ...q, prompt: p.prompt, images: p.images } : q,
        ),
      };

    case "prompt.held": {
      // A held prompt waits on the server for the person to resume the
      // queue, so the harness no longer has it, whatever it had before.
      const ids = new Set<string>(p.queueIds ?? []);
      return {
        ...s,
        queuedPrompts: (s.queuedPrompts ?? []).map((q) => (ids.has(q.queueId) ? { ...q, held: true, sent: false } : q)),
      };
    }

    case "prompt.released": {
      const ids = new Set<string>(p.queueIds ?? []);
      return {
        ...s,
        queuedPrompts: (s.queuedPrompts ?? []).map((q) => (ids.has(q.queueId) ? { ...q, held: false } : q)),
      };
    }

    case "prompt.injected": {
      // The harness read a prompt it was holding into the running turn: it
      // leaves the queue and joins the transcript where it was read, as the
      // user message it is. Mirrors internal/projection/state.go.
      const held = (s.queuedPrompts ?? []).find((q) => q.queueId === p.queueId);
      if (!held) return s;
      return {
        ...s,
        queuedPrompts: (s.queuedPrompts ?? []).filter((q) => q.queueId !== p.queueId),
        items: upsert(s, `prompt:${p.queueId}`, (it) => {
          it.kind = "message";
          it.receivedAt ??= ev.timestamp;
          it.role = "user";
          it.contentKind = "text";
          it.text = held.prompt;
          it.images = held.images;
          it.turnId = p.turnId;
        }),
      };
    }

    case "prompt.dequeued":
      return { ...s, queuedPrompts: (s.queuedPrompts ?? []).filter((q) => q.queueId !== p.queueId) };

    case "turn.finished": {
      // Only the finish of the turn that is actually open may take the
      // thread idle: a stale or duplicate finish must not report "user's
      // turn" while different work is running. Mirrors
      // internal/projection/state.go.
      const open = s.turns.reduce<string>((acc, t) => (t.done ? acc : t.id), "");
      const match = open === "" || p.turnId === open;
      const live = liveJobCalls(s.jobs);
      return {
        ...s,
        ...settleRequests(s, p.turnId),
        phase: match ? "idle" : s.phase,
        scheduledPrompts: (s.scheduledPrompts ?? []).map(q => q.turnId === p.turnId && p.stopReason === "error" ? {...q, status: "failed", error: p.error, revision: q.revision + 1} : q),
        turns: s.turns.map((t) =>
          t.id === p.turnId
            ? {
                ...t,
                done: true,
                stopReason: p.stopReason,
                error: p.error,
                failure: p.failure,
                finishedAt: ev.timestamp,
                // A finish with no anchor (a duplicate, or the server closing
                // the turn itself) does not take one away.
                ...(p.rewindable ? { rewindable: true } : {}),
              }
            : t,
        ),
        // Any tool of this turn left mid-flight was interrupted, not broken:
        // cancelled, never failed. A call standing for a live job keeps its
        // status — the job's own finish settles it — but not its tail, which
        // nothing streams to past the turn's end. A live job's own work (a
        // subagent's running command) is left alone entirely: it is still
        // running. Tools of other turns are left alone too. Mirrors
        // internal/projection/state.go.
        items: match
          ? s.items.map((it) => {
              if (it.kind !== "tool" || (it.turnId !== p.turnId && it.turnId)) return it;
              if (it.parentId && live.has(it.parentId)) return it;
              if (live.has(it.id)) return "tail" in it ? dropTail(it) : it;
              return it.status === "in_progress" || it.status === "pending" ? settle(it, "cancelled") : it;
            })
          : s.items,
      };
    }

    case "thread.rewound":
      return rewind(s, p.turnId, p.at);

    case "turn.diff":
      // What the turn changed, measured by the server once the harness had
      // stopped writing. A turn id this client has never seen is nothing to
      // fold: the event describes a turn that is not in this projection.
      return {
        ...s,
        turns: s.turns.map((t) => (t.id === p.turnId ? { ...t, diff: p as TurnDiff } : t)),
      };

    case "turn.suggested":
      // Only a suggestion for the latest turn is offered: one for an earlier
      // turn arrived after the human had already moved on. Mirrors
      // internal/projection/state.go.
      return s.turns.at(-1)?.id === p.turnId
        ? { ...s, suggestion: { turnId: p.turnId, text: p.text } }
        : s;

    case "message.chunk":
      return {
        ...s,
        // Streaming while idle means a turn is running that the log did not
        // announce. Trusting the activity over the phase keeps a lifecycle
        // desync from freezing the UI. A subagent's own output (parented to
        // its job's call) is not: it streams on after the parent's turn
        // ended, and the job surfaces carry it. Mirrors
        // internal/projection/state.go.
        phase: s.phase === "idle" && !p.parentToolCallId ? "turn" : s.phase,
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
        // Same defence as message.chunk: a tool starting is a turn running,
        // unless it is a subagent's.
        phase: s.phase === "idle" && !p.parentToolCallId ? "turn" : s.phase,
        items: upsert(s, p.toolCallId, (it) => {
          it.kind = "tool";
          it.receivedAt ??= ev.timestamp;
          it.turnId = p.turnId;
          it.toolKind = p.kind;
          it.title = p.title;
          it.status = p.status;
          if (toolDone(it.status)) delete it.tail;
          it.input = p.rawInput;
          // The server cut a long input string to a preview on the wire.
          if (p.truncated) it.truncated = true;
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
      //
      // The same goes for a subagent's call whose parent had its children
      // left out of the snapshot: the parent is in the window, its children
      // are not, and an upsert would add one child alone to a drill-down
      // that fetches the rest.
      if (!s.items.some((it) => it.id === p.toolCallId)) {
        if ((s.itemsBefore ?? 0) > 0) return s;
        const parent = p.parentToolCallId ? s.items.find((it) => it.id === p.parentToolCallId) : undefined;
        if ((parent?.childrenBefore ?? 0) > 0) return s;
      }
      return {
        ...s,
        // Same defence as message.chunk, but only for a tool going active: a
        // background tool's result straggling in after the turn ended must
        // not reopen "working". A subagent's tool is not the parent's turn
        // either. Mirrors internal/projection/state.go.
        phase:
          s.phase === "idle" && p.status === "in_progress" && !p.parentToolCallId ? "turn" : s.phase,
        items: upsert(s, p.toolCallId, (it) => {
          it.kind = "tool";
          it.receivedAt ??= ev.timestamp;
          if (p.status) it.status = p.status;
          if (p.title) it.title = p.title;
          if (p.rawInput) it.input = p.rawInput;
          if (p.truncated) it.truncated = true;
          if (p.parentToolCallId) it.parentId = p.parentToolCallId;
          if (p.content?.length) appendToolContent(it, p.content);
          // Output lands in the tail only while the call is running, judged
          // after this update's own status: a finished call has its output
          // in content, so its tail is dropped, and a delta straggling in
          // after the finish with it. Mirrors internal/projection/state.go.
          if (toolDone(it.status)) delete it.tail;
          else if (p.outputDelta) it.tail = appendTail(it.tail ?? "", p.outputDelta);
        }),
      };

    case "job.started":
      return applyJob(s, ev.timestamp, p as JobPayload, "started");

    case "job.updated":
      return applyJob(s, ev.timestamp, p as JobPayload, "updated");

    case "job.finished":
      return applyJob(s, ev.timestamp, p as JobPayload, "finished");

    case "plan.updated": {
      // A TodoWrite call is a list of its own, keyed by the call; a codex
      // plan is the turn's, revised in place. Each update carries every step,
      // so steps are replaced outright. Codex gives its explanation only on
      // the turn's first update, so a later one that omits it keeps it.
      // Mirrors internal/projection/state.go.
      // A plan anchored to nothing — the old shape, logged before plans had
      // a turn — has nowhere in the transcript to go.
      if (!p.turnId && !p.toolCallId) return s;
      const id = planItemId(p.turnId, p.toolCallId);
      const present = s.items.some((it) => it.id === id);
      // A straggler for a plan whose turn was windowed out, as for
      // tool_call.updated. The window keeps whole turns, so a turn with no
      // items here that is no longer running lies above the window: the page
      // fetch delivers the server's folded item. A running turn's first plan
      // is new, not a straggler, and is created.
      if (
        !present &&
        (s.itemsBefore ?? 0) > 0 &&
        !s.items.some((it) => it.turnId === p.turnId) &&
        !s.turns.some((t) => t.id === p.turnId && !t.done)
      ) {
        return s;
      }
      // A new TodoWrite list replaces every earlier one in this window.
      const base =
        p.toolCallId && !present
          ? {
              ...s,
              items: s.items.map((it) =>
                isTodoPlan(it) && !it.superseded ? { ...it, superseded: true } : it,
              ),
            }
          : s;
      return {
        ...s,
        items: upsert(base, id, (it) => {
          it.kind = "plan";
          it.receivedAt ??= ev.timestamp;
          it.turnId = p.turnId || undefined;
          it.steps = p.steps ?? [];
          if (p.explanation) it.explanation = p.explanation;
        }),
      };
    }

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

    case "artefact.shown":
      return applyArtefact(s, ev.timestamp, ev.seq, p as ArtefactShown);

    case "thread.account_changed":
      // The line where the thread moved to another account. Mirrors
      // internal/projection/state.go.
      return {
        ...s,
        items: upsert(s, `account:${ev.seq}`, (it) => {
          it.kind = "notice";
          it.receivedAt ??= ev.timestamp;
          it.noticeKind = "account";
          it.title = p.toName || p.to;
        }),
      };

    case "extension.notified":
      // Info is a toast and nothing more, raised by the live event rather
      // than here (this also runs on replay). A warning or an error is worth
      // a line the reader can find later; turnless like a compaction, since
      // the extension speaks for itself. Mirrors internal/projection/state.go.
      if (p.level !== "warning" && p.level !== "error") return s;
      return {
        ...s,
        items: upsert(s, `notify:${ev.seq}`, (it) => {
          it.kind = "notice";
          it.receivedAt ??= ev.timestamp;
          it.noticeKind = "notify";
          it.level = p.level;
          it.text = p.message;
        }),
      };

    case "extension.status":
      return { ...s, extensionStatuses: applyExtensionStatus(s.extensionStatuses ?? [], p.key, p.text ?? "") };

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
            ...(p.turnId ? { turnId: p.turnId } : {}),
          },
        ],
      };

    case "permission.resolved":
      return {
        ...s,
        pendingPermissions: s.pendingPermissions.filter((x) => x.requestId !== p.requestId),
      };

    case "elicitation.requested": {
      const pendingElicitations = [
        ...(s.pendingElicitations ?? []),
        {
          requestId: p.requestId,
          prompt: p.prompt,
          schema: p.schema ?? {},
          ...(p.turnId ? { turnId: p.turnId } : {}),
          ...(p.card ? { card: p.card } : {}),
        },
      ];
      if (!p.card) return { ...s, pendingElicitations };
      // A card is also a row in the transcript, where it stays once answered
      // to say what was done. Mirrors internal/projection/state.go.
      return {
        ...s,
        pendingElicitations,
        items: upsert(s, `card:${p.requestId}`, (it) => {
          it.kind = "card";
          it.receivedAt ??= ev.timestamp;
          it.turnId = p.turnId || undefined;
          it.card = p.card;
          it.status = "pending";
        }),
      };
    }

    case "elicitation.resolved": {
      const pendingElicitations = (s.pendingElicitations ?? []).filter((x) => x.requestId !== p.requestId);
      const id = `card:${p.requestId}`;
      // Only a card row this window holds: one paged out is updated on the
      // server, and an upsert here would append an orphan at the tail.
      if (!s.items.some((it) => it.id === id)) return { ...s, pendingElicitations };
      const outcome = p.value as CardOutcome | undefined;
      // The outcome's own result, else what the action implies: cardResult.
      const result =
        outcome?.result ?? (p.action === "accept" ? "saved" : p.action === "decline" ? "declined" : "cancelled");
      return {
        ...s,
        pendingElicitations,
        items: upsert(s, id, (it) => {
          it.status = result;
          if (outcome) it.outcome = outcome;
        }),
      };
    }

    default:
      return s;
  }
}
