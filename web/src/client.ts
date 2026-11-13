// The connection supervisor. Everything hard about being a presenter lives
// here: backoff, liveness probing, cursor tracking, reattach with afterSeq,
// resync handling, and command idempotency across reconnects.

import { applyEvent, applyJobRow, dropsJobRow, emptyState, prependItems, revivesUnheldJob } from "./apply";
import { checkBuild } from "./boot";
import type { ThreadSearchResult } from "./lib/threadFind";
import type {
  Access,
  AuthFlowEvent,
  Event,
  HarnessMeta,
  Item,
  Job,
  Project,
  QuotaStatus,
  ServerFrame,
  ThreadMeta,
  ThreadState,
  TurnStatus,
} from "./protocol";
import { ThreadCache } from "./threadCache";

export type ConnectionStatus = "connecting" | "online" | "offline";

interface Pending {
  resolve: (v: any) => void;
  reject: (e: Error) => void;
  // Retained so the same commandId is retried after a reconnect rather than a
  // new one being minted, which would risk sending a prompt twice.
  frame: Record<string, unknown>;
  timer: number;
}

/**
 * A command the server never answered: the socket was down, or proved dead,
 * when its deadline came. It may still have run — a retry should carry the
 * same commandId so the server's ledger dedupes it.
 */
export class CommandTimeout extends Error {
  constructor(
    readonly command: string,
    readonly commandId: string,
  ) {
    super("No answer from the server. Check the connection and try again.");
    this.name = "CommandTimeout";
  }
}

export interface ClientEvents {
  onStatus(status: ConnectionStatus): void;
  onThreads(threads: ThreadMeta[]): void;
  onHarnesses(harnesses: HarnessMeta[]): void;
  onComposerItemsChanged(threadId: string): void;
  onProjects(projects: Project[]): void;
  onQuotas(quotas: QuotaStatus[]): void;
  onState(threadId: string, state: ThreadState): void;
  /** The attached thread's transient turn status; null clears it. */
  onTurnStatus(threadId: string, status: TurnStatus | null): void;
  onAccess(access: Access): void;
  /** An event that happened just now, as opposed to one replayed to catch
      up: for side effects (a toast) that must not repeat on every attach. */
  onLiveEvent(threadId: string, event: Event): void;
}

// Reconnect ceilings in ms, not delays: each wait is drawn uniformly below its
// step (full jitter), so a server restart does not bring every tab back in
// lockstep. The terminal socket climbs the same ladder.
export const BACKOFF = [300, 1000, 2000, 4000, 8000, 16000];

// A woken phone often holds a socket that looks open and is dead, and the OS
// can take minutes to say so. Any frame within PROBE_TIMEOUT of a ping proves
// it alive; silence drops it and reconnects at once, which afterSeq makes
// cheap. HEARTBEAT is how often a visible page asks.
const PROBE_TIMEOUT = 3_000;
const HEARTBEAT = 25_000;

// How long a command waits before the connection is asked whether it is still
// there. A live one keeps it waiting — the server bounds its own work and
// always acks — so only a socket that is down or dead fails a command.
const COMMAND_TIMEOUT = 15_000;

// The HTTP snapshot that opens a thread. Past this, attach over the socket
// instead, which carries the same state.
const SNAPSHOT_TIMEOUT = 10_000;

export class Client {
  private ws: WebSocket | null = null;
  private attempt = 0;
  private closedByUs = false;
  private stableTimer: number | null = null;
  private retryTimer: number | null = null;

  private probeTimer: number | null = null;
  private probeWaiters: ((alive: boolean) => void)[] = [];
  private heartbeat: number | null = null;
  private watching = false;
  private lastHeard = 0;
  private openedAt = 0;

  private pending = new Map<string, Pending>();

  // Auth flows are ephemeral narration bound to this one connection, so they
  // deliberately bypass the thread reducer and any persisted state: a dialog
  // subscribes by flowId and the frames go straight to it. Events can start
  // arriving before the auth_begin ack resolves (and thus before the dialog
  // knows its flowId), so unclaimed frames wait in a small backlog.
  private authFlowListeners = new Map<string, (ev: AuthFlowEvent) => void>();
  private authFlowBacklog = new Map<string, AuthFlowEvent[]>();

  // The attached thread and its applied cursor. Reconnecting to a different
  // address is the same operation as reconnecting to the same one: attach with
  // afterSeq and receive the gap.
  private threadId: string | null = null;
  private cursor = 0;
  private state: ThreadState | null = null;
  private snapshotRequest: AbortController | null = null;
  // Attaches sent and not yet answered, per thread. The server answers each
  // with exactly one "synchronized" (or one error), so while this is nonzero
  // a replay is still coming, and an event that skips ahead is a straggler
  // the replay will cover rather than a loss to reattach for. Counted, not
  // flagged: switch A→B→A quickly and the first attach's marker is still on
  // the wire, and must not end the second one's wait.
  private attachesInFlight = new Map<string, number>();
  // Threads recently left, so coming back paints at once and attaches with
  // afterSeq instead of refetching the snapshot.
  private recentThreads = new ThreadCache();

  // What this page last said it is showing, for the server's "is anyone
  // watching this thread" check that decides whether to push. Kept so every
  // new connection can say it again: the server forgets it with the socket.
  private presence: { visible: boolean; threadId: string } | null = null;

  constructor(private url: string, private events: ClientEvents) {}

  connect() {
    this.closedByUs = false;
    this.watch();
    this.open();
  }

  private open() {
    this.retryTimer = null;
    this.openedAt = Date.now();
    this.events.onStatus("connecting");

    const ws = new WebSocket(this.url);
    this.ws = ws;

    ws.onopen = () => {
      // Everything here runs before the server will answer. A throw used to
      // leave the socket open with no hello sent — the client looked connected
      // and waited forever for a welcome that could not come.
      try {
        this.raw({ type: "hello", protocolVersion: 1, clientId: clientId() });

        // Re-attach where we left off; the server sends only what we missed.
        if (this.threadId && !this.snapshotRequest) {
          this.sendAttach(this.threadId, this.cursor);
        }
        // Re-send in-flight commands with their original ids. The server
        // replays stored results rather than executing twice.
        for (const [, p] of this.pending) this.raw(p.frame);
        if (this.presence) this.raw({ type: "presence", ...this.presence });

        // A connection that survives 30s resets the backoff ladder.
        this.stableTimer = window.setTimeout(() => (this.attempt = 0), 30_000);
      } catch (err) {
        console.error("handshake failed", err);
        ws.close();
      }
    };

    ws.onmessage = (e) => {
      this.heard();
      this.handle(JSON.parse(e.data as string) as ServerFrame);
    };

    ws.onclose = () => this.lost(this.backoff());

    ws.onerror = () => ws.close();
  }

  close() {
    this.closedByUs = true;
    this.unwatch();
    if (this.retryTimer !== null) window.clearTimeout(this.retryTimer);
    this.retryTimer = null;
    // Their deadlines would only fail them into a page that has moved on.
    for (const [, p] of this.pending) window.clearTimeout(p.timer);
    // Nor may a snapshot land in it.
    this.snapshotRequest?.abort();
    this.snapshotRequest = null;
    this.ws?.close();
  }

  private backoff(): number {
    return Math.random() * BACKOFF[Math.min(this.attempt++, BACKOFF.length - 1)];
  }

  /** The socket is gone: settle what waited on it and, unless we closed it, retry after `delay` (0: now). */
  private lost(delay: number) {
    if (this.stableTimer) window.clearTimeout(this.stableTimer);
    this.stableTimer = null;
    this.ws = null;
    // Its attaches will never be answered; the next socket sends its own.
    this.attachesInFlight.clear();
    // A probe of the old socket must not fire against its replacement.
    this.clearProbe();
    this.settleProbe(false);
    if (this.closedByUs) return;
    this.dropAuthFlows();
    this.events.onStatus("offline");
    if (delay === 0) this.open();
    else this.retryTimer = window.setTimeout(() => this.open(), delay);
  }

  /**
   * Abandon a socket that has stopped answering and reconnect now. Its
   * handlers come off first: closing a dead socket waits on a close handshake
   * that will never come, and its onclose could land long after the
   * replacement is up.
   */
  private drop() {
    const ws = this.ws;
    if (!ws) return;
    ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
    ws.close();
    this.lost(0);
  }

  // Wake signals. A page coming back to the screen, the network coming back,
  // or the page being restored from the back-forward cache are exactly when a
  // socket is most likely dead without knowing it.
  private readonly wake = () => {
    if (document.visibilityState !== "visible") {
      this.stopHeartbeat();
      return;
    }
    this.startHeartbeat();
    if (this.retryTimer !== null) {
      // Waiting out a backoff step that was sized for a server that was down,
      // not for a phone that was asleep. Try now.
      window.clearTimeout(this.retryTimer);
      this.open();
    } else if (this.ws?.readyState === WebSocket.CONNECTING) {
      // An attempt begun before the network changed can hang on the old one
      // until the browser gives up on it. One that has had a probe's worth of
      // time is replaced rather than waited on.
      if (Date.now() - this.openedAt > PROBE_TIMEOUT) this.drop();
    } else {
      this.probe();
    }
  };

  private watch() {
    if (this.watching) return;
    this.watching = true;
    document.addEventListener("visibilitychange", this.wake);
    window.addEventListener("online", this.wake);
    window.addEventListener("pageshow", this.wake);
    if (document.visibilityState === "visible") this.startHeartbeat();
  }

  private unwatch() {
    this.watching = false;
    document.removeEventListener("visibilitychange", this.wake);
    window.removeEventListener("online", this.wake);
    window.removeEventListener("pageshow", this.wake);
    this.stopHeartbeat();
    this.clearProbe();
  }

  private startHeartbeat() {
    if (this.heartbeat === null) this.heartbeat = window.setInterval(() => this.probe(), HEARTBEAT);
  }

  private stopHeartbeat() {
    if (this.heartbeat !== null) window.clearInterval(this.heartbeat);
    this.heartbeat = null;
  }

  /**
   * Ask the server for any sign of life. `then` hears whether one came: true on
   * the next frame, false if the socket is not open or stayed silent past
   * PROBE_TIMEOUT — in which case it has already been dropped for a fresh one.
   * Concurrent asks share one ping.
   */
  private probe(then?: (alive: boolean) => void) {
    if (this.ws?.readyState !== WebSocket.OPEN) {
      then?.(false);
      return;
    }
    if (then) this.probeWaiters.push(then);
    if (this.probeTimer !== null) return;
    this.raw({ type: "ping" });
    this.probeTimer = window.setTimeout(() => {
      this.probeTimer = null;
      this.drop();
    }, PROBE_TIMEOUT);
  }

  // Any frame answers a probe, not just the pong: on a slow link the pong can
  // sit behind a replay, and the replay arriving says the same thing.
  private heard() {
    this.lastHeard = Date.now();
    this.clearProbe();
    this.settleProbe(true);
  }

  private clearProbe() {
    if (this.probeTimer !== null) window.clearTimeout(this.probeTimer);
    this.probeTimer = null;
  }

  private settleProbe(alive: boolean) {
    const waiters = this.probeWaiters;
    this.probeWaiters = [];
    for (const w of waiters) w(alive);
  }

  /**
   * Adopt a state snapshot cached by a previous page of this tab (see
   * resume.ts), before connecting. The first attach then carries afterSeq, so
   * the server replays only what happened while the page was dead — and if
   * that gap has grown past replay range it answers with a snapshot, which
   * replaces the cache wholesale. Either way the reader starts on the cached
   * transcript instead of "Attaching…".
   */
  prime(state: ThreadState) {
    this.threadId = state.threadId;
    this.cursor = state.seq;
    this.state = state;
  }

  /**
   * Attach to a thread, replacing any current attachment. A thread in the
   * cache is published from it immediately and attached with afterSeq; any
   * other is fetched over HTTP first. Never called for the thread already
   * attached (see useThreadSelection's select): a primed page's attach is
   * already on the way, and a second would replay the gap twice.
   *
   * Safe before connect(): the fetch needs nothing from the socket, which is
   * how a page boots (useWire) with the snapshot and the handshake in flight
   * together. Whichever lands second sends the attach: the socket's open
   * waits out a pending fetch, and a fetch that beats the socket leaves the
   * attach to its open.
   */
  attach(threadId: string) {
    this.leave();
    this.threadId = threadId;

    const cached = this.recentThreads.get(threadId);
    if (cached) {
      this.state = cached;
      this.cursor = cached.seq;
      this.events.onState(threadId, cached);
      this.sendAttach(threadId, cached.seq);
      return;
    }

    this.cursor = 0;
    this.state = null;
    const request = new AbortController();
    this.snapshotRequest = request;
    const started = performance.now();
    // Aborted either way, so the flag is what tells a stall (fall back to the
    // socket) from being superseded (do nothing).
    let stalled = false;
    const deadline = window.setTimeout(() => {
      stalled = true;
      request.abort();
    }, SNAPSHOT_TIMEOUT);
    fetch(`/api/threads/${encodeURIComponent(threadId)}`, {
      signal: request.signal,
    })
      .then(async (response) => {
        if (!response.ok) throw new Error(`snapshot failed (${response.status})`);
        return (await response.json()) as ThreadState;
      })
      .then((state) => {
        if (request.signal.aborted || this.threadId !== threadId) return;
        this.state = state;
        this.cursor = state.seq;
        this.events.onState(threadId, state);
        performance.measure("omniplex.thread_snapshot", { start: started });
        this.sendAttach(threadId, state.seq);
      })
      .catch((error) => {
        if ((request.signal.aborted && !stalled) || this.threadId !== threadId) return;
        console.warn("HTTP snapshot failed; falling back to WebSocket", error);
        this.sendAttach(threadId);
      })
      .finally(() => {
        window.clearTimeout(deadline);
        if (this.snapshotRequest === request) this.snapshotRequest = null;
      });
  }

  /**
   * Fetch the page of items above the loaded window and prepend it, publishing
   * the merged state. `count: "all"` pulls everything left — the copy-transcript
   * path, which needs the whole timeline at once. Resolves with the merged
   * state, or null when there was nothing to do or the page went stale.
   *
   * Staleness is judged by the cursor: the window is a contiguous suffix, live
   * events only ever append to it, so the cursor moves only when a snapshot
   * replaces the state (resync, reattach) — and a page fetched against the old
   * cursor must then be dropped rather than spliced into the wrong place.
   */
  loadOlder(count?: "all"): Promise<ThreadState | null> {
    if (this.olderInFlight) return count === "all" ? this.olderInFlight : Promise.resolve(null);
    return this.occupyOlder(this.fetchOlder(count));
  }

  /**
   * Page the window in down to the start of the turn holding `index`, in one
   * request, publishing the merged state. Resolves with the state (unfetched
   * when `index` is already loaded), or null when the page went stale or
   * failed — the same cursor check as loadOlder.
   */
  async loadTo(index: number): Promise<ThreadState | null> {
    // A scroll page landing first may already cover the index, and splicing
    // ours in under it would break the window's contiguity: wait it out, then
    // read the cursor afresh. Looped, as another may have claimed the slot
    // before this resumed.
    while (this.olderInFlight) await this.olderInFlight;
    const s = this.state;
    if (!s) return null;
    const before = s.itemsBefore ?? 0;
    if (index >= before) return s;
    return this.occupyOlder(this.fetchPage(s, `before=${before}&from=${Math.max(0, index)}`));
  }

  /** Hold the older-page slot for `request` until it settles. */
  private occupyOlder(request: Promise<ThreadState | null>): Promise<ThreadState | null> {
    const held = request.finally(() => {
      if (this.olderInFlight === held) this.olderInFlight = null;
    });
    this.olderInFlight = held;
    return held;
  }

  /**
   * Pull every remaining older page in, for the paths that need the whole
   * timeline at once (copying it out). Resolves null on failure — the caller
   * must not pretend a truncated timeline is the whole thing.
   */
  async loadAll(): Promise<ThreadState | null> {
    // Loop rather than trusting one round trip: an ordinary scroll page may be
    // in flight when this starts, and loadOlder then resolves with *that*
    // page's merge, which is not yet everything.
    while (this.state && (this.state.itemsBefore ?? 0) > 0) {
      if ((await this.loadOlder("all")) === null) return null;
    }
    return this.state;
  }

  private fetchOlder(count?: "all"): Promise<ThreadState | null> {
    const s = this.state;
    const before = s?.itemsBefore ?? 0;
    if (!s || before === 0) return Promise.resolve(null);
    return this.fetchPage(s, count === "all" ? `before=${before}&count=all` : `before=${before}`);
  }

  /** Fetch an items page above `s`'s window and prepend it; see loadOlder. */
  private async fetchPage(s: ThreadState, query: string): Promise<ThreadState | null> {
    const before = s.itemsBefore ?? 0;
    try {
      const response = await fetch(
        `/api/threads/${encodeURIComponent(s.threadId)}/items?${query}`,
      );
      if (!response.ok) throw new Error(`items page failed (${response.status})`);
      const page = (await response.json()) as { items: Item[]; itemsBefore: number; jobs?: Job[] };
      const cur = this.state;
      // The cursor is the contiguity check: live events only append, so it
      // moves only when a snapshot replaced the state (resync, reattach) — and
      // a page fetched against the old cursor would splice in the wrong place.
      if (!cur || cur.threadId !== s.threadId || (cur.itemsBefore ?? 0) !== before) return null;
      // The page brings the jobs its spawn items need. One already held is
      // kept as it is: live events have been folding into it since the
      // snapshot, so it can be fresher than the page's copy.
      const haveJobs = new Set(cur.jobs.map((j) => j.id));
      const added = (page.jobs ?? []).filter((j) => !haveJobs.has(j.id));
      const next: ThreadState = {
        ...cur,
        items: prependItems(cur.items, page.items ?? []),
        itemsBefore: page.itemsBefore,
        // Older work, so it goes above what the snapshot carried.
        jobs: added.length ? [...added, ...cur.jobs] : cur.jobs,
        jobsBefore: cur.jobsBefore ? Math.max(0, cur.jobsBefore - added.length) : cur.jobsBefore,
      };
      this.state = next;
      this.events.onState(next.threadId, next);
      return next;
    } catch (error) {
      console.warn("loading older items failed", error);
      return null;
    }
  }
  private olderInFlight: Promise<ThreadState | null> | null = null;

  /**
   * Search the attached thread's messages and plans for `q`: the newest
   * matches, oldest first, with the true count. Throws when no thread is
   * attached or the server refuses (a query under SEARCH_MIN_LENGTH).
   */
  async search(q: string, signal: AbortSignal): Promise<ThreadSearchResult> {
    const threadId = this.threadId;
    if (!threadId) throw new Error("no thread attached");
    const response = await fetch(
      `/api/threads/${encodeURIComponent(threadId)}/search?q=${encodeURIComponent(q)}`,
      { signal },
    );
    if (!response.ok) throw new Error(`search failed (${response.status})`);
    const body = (await response.json()) as Partial<ThreadSearchResult>;
    return { matches: body.matches ?? [], total: body.total ?? 0 };
  }

  /**
   * Fetch every job the snapshot left out (`jobsBefore`) and merge them in,
   * publishing the merged state. Resolves null on failure or when the thread
   * changed underneath; concurrent asks share one request.
   */
  loadAllJobs(): Promise<ThreadState | null> {
    if (this.jobsInFlight) return this.jobsInFlight.request;
    this.droppedJobRows = [];
    const controller = new AbortController();
    const request = this.fetchJobs(controller.signal).finally(() => {
      if (this.jobsInFlight?.controller === controller) this.jobsInFlight = null;
    });
    this.jobsInFlight = { request, controller };
    return request;
  }

  private async fetchJobs(signal: AbortSignal): Promise<ThreadState | null> {
    const s = this.state;
    if (!s) return null;
    try {
      const response = await fetch(`/api/threads/${encodeURIComponent(s.threadId)}/jobs`, { signal });
      if (!response.ok) throw new Error(`jobs failed (${response.status})`);
      const body = (await response.json()) as { jobs?: Job[] };
      const cur = this.state;
      // Unlike an items page there is no position to get wrong: the merge is a
      // union by id, so a snapshot landing meanwhile is harmless. Only another
      // thread's list would be, and leaving the thread aborts this.
      if (signal.aborted || !cur) return null;
      // The client's copy of a job wins: live events have folded into it
      // since the server answered. Jobs the server did not list yet (started
      // after it read) stay, after the rest.
      const mine = new Map(cur.jobs.map((j) => [j.id, j]));
      const listed = new Set<string>();
      const jobs = (body.jobs ?? []).map((j) => {
        listed.add(j.id);
        return mine.get(j.id) ?? j;
      });
      for (const j of cur.jobs) if (!listed.has(j.id)) jobs.push(j);
      // Rows for jobs the client did not hold were dropped, and those that
      // landed after the server read are in neither copy: fold them in now.
      const next = this.droppedJobRows.reduce(applyJobRow, { ...cur, jobs, jobsBefore: 0 });
      this.droppedJobRows = [];
      this.state = next;
      this.events.onState(next.threadId, next);
      return next;
    } catch (error) {
      if (!signal.aborted) console.warn("loading all jobs failed", error);
      return null;
    }
  }
  private jobsInFlight: { request: Promise<ThreadState | null>; controller: AbortController } | null = null;
  // Job rows applyEvent dropped while jobsInFlight; see fetchJobs.
  private droppedJobRows: Event[] = [];

  detach() {
    this.leave();
    this.threadId = null;
    this.cursor = 0;
    this.state = null;
  }

  /**
   * Let go of the attached thread: tell the server, keep its state for a
   * quick return to it, and drop what was being fetched for it. A jobs fetch
   * left running, beaten by a return from the cache, would fold the other
   * thread's rows into this one's list.
   */
  private leave() {
    this.snapshotRequest?.abort();
    this.snapshotRequest = null;
    this.clearTurnStatus();
    if (this.threadId) this.raw({ type: "detach", threadId: this.threadId });
    if (this.state && this.state.threadId === this.threadId) this.recentThreads.put(this.state);
    this.jobsInFlight?.controller.abort();
    this.jobsInFlight = null;
    this.droppedJobRows = [];
  }

  /** Ask for the thread's history after afterSeq, or all of it when absent. */
  private sendAttach(threadId: string, afterSeq?: number) {
    this.clearTurnStatus();
    const sent = this.raw({ type: "attach", threadId, afterSeq });
    if (sent) this.attachesInFlight.set(threadId, (this.attachesInFlight.get(threadId) ?? 0) + 1);
  }

  /** One attach for the thread has been answered, by "synchronized" or an error. */
  private attachAnswered(threadId: string) {
    const n = this.attachesInFlight.get(threadId) ?? 0;
    if (n <= 1) this.attachesInFlight.delete(threadId);
    else this.attachesInFlight.set(threadId, n - 1);
  }

  /** Whether a replay is still coming for the attached thread. */
  private catchingUp(): boolean {
    return this.threadId !== null && this.attachesInFlight.has(this.threadId);
  }

  /**
   * Tell the server whether this page is on screen and which thread it shows
   * ("" for none). Sent now if the socket is up, and again on every reconnect.
   */
  setPresence(visible: boolean, threadId: string) {
    const p = this.presence;
    if (p && p.visible === visible && p.threadId === threadId) return;
    this.presence = { visible, threadId };
    this.raw({ type: "presence", visible, threadId });
  }

  /**
   * Send a command, resolving with its ack result. Safe to retry: passing a
   * failed command's id back runs it at most once. Rejects with
   * CommandTimeout if, COMMAND_TIMEOUT after sending (and every such interval
   * after), the connection is down or fails a probe.
   */
  command(command: string, args: unknown, commandId = uuid()): Promise<any> {
    const frame = { type: "command", commandId, command, args };
    return new Promise((resolve, reject) => {
      const p: Pending = { resolve, reject, frame, timer: 0 };
      const arm = () => {
        p.timer = window.setTimeout(
          () =>
            this.probe((alive) => {
              if (this.pending.get(commandId) !== p) return;
              if (alive) return arm();
              this.pending.delete(commandId);
              reject(new CommandTimeout(command, commandId));
            }),
          COMMAND_TIMEOUT,
        );
      };
      arm();
      this.pending.set(commandId, p);
      this.raw(frame);
      // Gone quiet, so the socket may be dead: find out now rather than at the
      // deadline. A dead one is replaced and the command resent on it.
      if (Date.now() - this.lastHeard > PROBE_TIMEOUT) this.probe();
    });
  }

  /**
   * Subscribe to a sign-in flow's events. Frames that arrived before the
   * subscriber (the auth_begin ack races the first events) are replayed on
   * subscribe. Returns the unsubscribe.
   */
  onAuthFlow(flowId: string, listener: (ev: AuthFlowEvent) => void): () => void {
    this.authFlowListeners.set(flowId, listener);
    const backlog = this.authFlowBacklog.get(flowId);
    this.authFlowBacklog.delete(flowId);
    for (const ev of backlog ?? []) listener(ev);
    return () => {
      if (this.authFlowListeners.get(flowId) === listener) {
        this.authFlowListeners.delete(flowId);
      }
    };
  }

  /**
   * Flows die with the socket — the server cancels them when the connection
   * drops, and their events go only to the connection that began them. Tell
   * any watching dialog honestly rather than leaving it spinning forever.
   */
  private dropAuthFlows() {
    const listeners = [...this.authFlowListeners];
    this.authFlowListeners.clear();
    this.authFlowBacklog.clear();
    for (const [flowId, listener] of listeners) {
      listener({ flowId, error: "Connection lost — start the sign-in again." });
    }
  }

  // A turn status is only ever as good as the attachment that carried it. The
  // server re-sends the live one after `synchronized` on every attach, so
  // dropping ours whenever we leave, (re)attach or take a snapshot means a
  // status the server has since cleared can never stick.
  private clearTurnStatus() {
    if (this.threadId) this.events.onTurnStatus(this.threadId, null);
  }

  private raw(frame: Record<string, unknown>): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) return false;
    this.ws.send(JSON.stringify(frame));
    return true;
  }

  private handle(f: ServerFrame) {
    switch (f.type) {
      case "welcome":
        // Checked first: if this page is running a bundle the server has
        // replaced, nothing else it reports is worth acting on.
        checkBuild(f.build);
        // Online once the server has answered, not when the socket opened: on
        // a slow link the gap is seconds, and "Reconnecting…" should last it.
        this.events.onStatus("online");
        this.events.onThreads(f.threads ?? []);
        this.events.onHarnesses(f.harnesses ?? []);
        this.events.onProjects(f.projects ?? []);
        this.events.onQuotas(f.quotas ?? []);
        if (f.access) {
          this.events.onAccess(f.access);
        }
        break;

      case "threads":
        this.events.onThreads(f.threads ?? []);
        break;

      // Harnesses can change after the welcome frame: a model list is read
      // from the harness in the background and lands seconds later. Without
      // this the picker would show the fallback list until a reconnect.
      case "harnesses":
        this.events.onHarnesses(f.harnesses ?? []);
        break;

      // The project registry changed somewhere — this device or a paired one.
      // The list travels whole; absent means the last project was removed, so
      // a project deleted on the phone leaves the laptop's picker too.
      case "projects":
        this.events.onProjects(f.projects ?? []);
        break;

      // Usage limits changed somewhere: a live rate-limit push from a
      // running thread, or a refresh any device asked for. The whole list,
      // so one provider moving never blanks another.
      case "quotas":
        this.events.onQuotas(f.quotas ?? []);
        break;

      case "composer_items_changed":
        if (f.threadId === this.threadId) this.events.onComposerItemsChanged(f.threadId);
        break;

      // Why the running turn has gone quiet. Transient, so it bypasses the
      // reducer: it is never logged and never part of thread state. No status,
      // or one with no state, clears it.
      case "turn_status":
        if (f.threadId !== this.threadId) return;
        this.events.onTurnStatus(f.threadId, f.turnStatus?.state ? f.turnStatus : null);
        break;

      // A snapshot and a replay are both followed by `synchronized`, which
      // publishes. Holding off until then renders a reconnect or a return to
      // a cached thread once, not once per event of the gap — on a phone that
      // is the difference between a tap landing and the page locking up.
      //
      // A snapshot means the gap was past replay range, so whatever we held
      // for this thread, a cached copy included, is superseded wholesale; it
      // is this that gets cached when we leave.
      case "snapshot":
        if (f.threadId !== this.threadId || !f.state) return;
        this.state = f.state;
        this.cursor = f.state.seq;
        // The snapshot is newer than any row held for a jobs fetch.
        this.droppedJobRows = [];
        this.clearTurnStatus();
        break;

      case "replay": {
        if (f.threadId !== this.threadId || !f.events?.length) return;
        let next = this.state ?? emptyState(f.threadId);
        let revived = false;
        for (const ev of f.events) {
          revived ||= revivesUnheldJob(next, ev);
          if (this.jobsInFlight && ev.seq > next.seq && dropsJobRow(next, ev)) this.droppedJobRows.push(ev);
          next = applyEvent(next, ev);
        }
        this.state = next;
        this.cursor = next.seq;
        if (revived) void this.loadAllJobs();
        break;
      }

      // A live event, published as it lands.
      case "event": {
        if (f.threadId !== this.threadId || !f.event) return;
        // seq is gapless per thread, so anything past the next one is not
        // ours to apply yet. With an attach outstanding it is a straggler from
        // an earlier attachment of this thread, and the replay behind it
        // brings the events it skipped. Otherwise something was lost: ask again.
        if (this.state && f.event.seq > this.state.seq + 1) {
          if (!this.catchingUp()) this.sendAttach(f.threadId, this.cursor);
          return;
        }
        const base = this.state ?? emptyState(f.threadId);
        const next = applyEvent(base, f.event);
        if (next === base) return; // duplicate; already applied
        if (this.jobsInFlight && dropsJobRow(base, f.event)) this.droppedJobRows.push(f.event);
        this.state = next;
        this.cursor = next.seq;
        this.events.onState(f.threadId, next);
        if (revivesUnheldJob(base, f.event)) void this.loadAllJobs();
        // Catch-up comes in "replay" frames, so this one is news, unless it
        // straggled in from an earlier attachment while a later one is still
        // being answered.
        if (!this.catchingUp()) this.events.onLiveEvent(f.threadId, f.event);
        break;
      }

      // The end of a catch-up: publish everything it applied, once.
      case "synchronized":
        if (!f.threadId) return;
        this.attachAnswered(f.threadId);
        if (f.threadId === this.threadId && this.state) this.events.onState(f.threadId, this.state);
        break;

      case "resync":
        // The server dropped our queue. Reattach from the applied cursor; if
        // we have fallen far enough behind it answers with a snapshot.
        // A clear made while our queue was dropped never reached us; the
        // attach drops the status too, and the server re-sends a live one.
        if (f.threadId === this.threadId) this.sendAttach(f.threadId, this.cursor);
        break;

      case "ack": {
        const p = f.commandId ? this.pending.get(f.commandId) : undefined;
        if (!p) return;
        this.pending.delete(f.commandId!);
        window.clearTimeout(p.timer);
        if (f.error) p.reject(new Error(f.error));
        else p.resolve(f.result);
        break;
      }

      // A sign-in flow narrating. Routed straight to the dialog that began it,
      // never through the reducer: nothing about a flow belongs in thread
      // state, and secrets must never end up anywhere persistable.
      case "auth_event": {
        const ev = f.authFlow;
        if (!ev?.flowId) return;
        const listener = this.authFlowListeners.get(ev.flowId);
        if (listener) {
          listener(ev);
        } else {
          const backlog = this.authFlowBacklog.get(ev.flowId) ?? [];
          // A flow nobody ever claims must not accumulate forever.
          if (backlog.length < 100) backlog.push(ev);
          this.authFlowBacklog.set(ev.flowId, backlog);
        }
        break;
      }

      case "error":
        console.warn("server error:", f.error);
        // A failed attach answers with this instead of "synchronized".
        if (f.threadId) this.attachAnswered(f.threadId);
        break;
    }
  }
}

export function uuid(): string {
  // crypto.randomUUID exists only in a secure context. http://localhost counts
  // as one, but http://<host>.ts.net does not — so this is undefined on exactly
  // the origins we tell people to use from a phone. getRandomValues carries no
  // such restriction, so derive a v4 UUID from it when the shortcut is absent.
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();

  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
  bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function clientId(): string {
  // Safari throws on localStorage when the user has blocked storage, and a
  // throw here would escape the open handler and abandon the handshake. An
  // ephemeral id is a far better outcome than no connection.
  const key = "omniplex.clientId";
  try {
    const existing = localStorage.getItem(key);
    if (existing) return existing;
    const id = uuid();
    localStorage.setItem(key, id);
    return id;
  } catch {
    return uuid();
  }
}

export function wsURL(): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/ws`;
}
