// The connection supervisor. Everything hard about being a presenter lives
// here: backoff, liveness probing, cursor tracking, reattach with afterSeq,
// resync handling, and command idempotency across reconnects.

import { applyEvent, emptyState } from "./apply";
import { checkBuild } from "./boot";
import type { Access, AuthFlowEvent, HarnessMeta, Item, Project, QuotaStatus, ServerFrame, ThreadMeta, ThreadState } from "./protocol";

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
  onAccess(access: Access): void;
}

// Ceilings, not delays: each wait is drawn uniformly below its step (full
// jitter), so a server restart does not bring every tab back in lockstep.
const BACKOFF = [300, 1000, 2000, 4000, 8000, 16000];

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
    this.events.onStatus("connecting");

    const ws = new WebSocket(this.url);
    this.ws = ws;

    ws.onopen = () => {
      // Everything here runs before the server will answer. A throw used to
      // leave the socket open with no hello sent — the client looked connected
      // and waited forever for a welcome that could not come.
      try {
        this.events.onStatus("online");
        this.raw({ type: "hello", protocolVersion: 1, clientId: clientId() });

        // Re-attach where we left off; the server sends only what we missed.
        if (this.threadId && !this.snapshotRequest) {
          this.raw({ type: "attach", threadId: this.threadId, afterSeq: this.cursor });
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

    ws.onclose = () => this.closed(this.backoff());

    ws.onerror = () => ws.close();
  }

  close() {
    this.closedByUs = true;
    this.unwatch();
    if (this.retryTimer !== null) window.clearTimeout(this.retryTimer);
    this.retryTimer = null;
    this.ws?.close();
  }

  private backoff(): number {
    return Math.random() * BACKOFF[Math.min(this.attempt++, BACKOFF.length - 1)];
  }

  /** The socket is gone: settle what waited on it and, unless we closed it, retry after `delay`. */
  private closed(delay: number) {
    if (this.stableTimer) window.clearTimeout(this.stableTimer);
    this.stableTimer = null;
    this.ws = null;
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
    this.closed(0);
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
    if (this.probeTimer !== null) window.clearTimeout(this.probeTimer);
    this.probeTimer = null;
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
    if (this.probeTimer !== null) window.clearTimeout(this.probeTimer);
    this.probeTimer = null;
    this.settleProbe(true);
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

  /** Attach to a thread, replacing any current attachment. */
  attach(threadId: string) {
    this.snapshotRequest?.abort();
    if (this.threadId && this.threadId !== threadId) {
      this.raw({ type: "detach", threadId: this.threadId });
    }
    this.threadId = threadId;
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
        this.raw({ type: "attach", threadId, afterSeq: state.seq });
      })
      .catch((error) => {
        if ((request.signal.aborted && !stalled) || this.threadId !== threadId) return;
        console.warn("HTTP snapshot failed; falling back to WebSocket", error);
        this.raw({ type: "attach", threadId });
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
    const request = this.fetchOlder(count).finally(() => (this.olderInFlight = null));
    this.olderInFlight = request;
    return request;
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

  private async fetchOlder(count?: "all"): Promise<ThreadState | null> {
    const s = this.state;
    const before = s?.itemsBefore ?? 0;
    if (!s || before === 0) return null;
    try {
      const query = count === "all" ? `before=${before}&count=all` : `before=${before}`;
      const response = await fetch(
        `/api/threads/${encodeURIComponent(s.threadId)}/items?${query}`,
      );
      if (!response.ok) throw new Error(`items page failed (${response.status})`);
      const page = (await response.json()) as { items: Item[]; itemsBefore: number };
      const cur = this.state;
      // The cursor is the contiguity check: live events only append, so it
      // moves only when a snapshot replaced the state (resync, reattach) — and
      // a page fetched against the old cursor would splice in the wrong place.
      if (!cur || cur.threadId !== s.threadId || (cur.itemsBefore ?? 0) !== before) return null;
      // A straggling event for a trimmed item is dropped rather than applied
      // (see apply.ts), but belt and braces: never prepend an id the window
      // already holds — duplicate ids break rendering outright.
      const have = new Set(cur.items.map((it) => it.id));
      const next: ThreadState = {
        ...cur,
        items: [...(page.items ?? []).filter((it) => !have.has(it.id)), ...cur.items],
        itemsBefore: page.itemsBefore,
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

  detach() {
    this.snapshotRequest?.abort();
    this.snapshotRequest = null;
    if (this.threadId) this.raw({ type: "detach", threadId: this.threadId });
    this.threadId = null;
    this.cursor = 0;
    this.state = null;
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
   * Send a command, resolving with its ack result. Safe to retry. Rejects with
   * CommandTimeout if, COMMAND_TIMEOUT after sending (and every such interval
   * after), the connection is down or fails a probe.
   */
  command(command: string, args: unknown): Promise<any> {
    const commandId = uuid();
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

  private raw(frame: Record<string, unknown>) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(frame));
    }
  }

  private handle(f: ServerFrame) {
    switch (f.type) {
      case "welcome":
        // Checked first: if this page is running a bundle the server has
        // replaced, nothing else it reports is worth acting on.
        checkBuild(f.build);
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

      case "snapshot":
        if (f.threadId !== this.threadId || !f.state) return;
        this.state = f.state;
        this.cursor = f.state.seq;
        this.events.onState(f.threadId, this.state);
        break;

      case "event": {
        if (f.threadId !== this.threadId || !f.event) return;
        const base = this.state ?? emptyState(f.threadId);
        const next = applyEvent(base, f.event);
        if (next === base) return; // duplicate; already applied
        this.state = next;
        this.cursor = next.seq;
        this.events.onState(f.threadId, next);
        break;
      }

      case "synchronized":
        if (f.threadId === this.threadId && this.state) {
          this.events.onState(f.threadId, this.state);
        }
        break;

      case "resync":
        // The server dropped our queue. Reattach from the applied cursor; if
        // we have fallen far enough behind it answers with a snapshot.
        if (f.threadId === this.threadId) {
          this.raw({ type: "attach", threadId: f.threadId, afterSeq: this.cursor });
        }
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
