// The connection supervisor. Everything hard about being a presenter lives
// here: backoff, cursor tracking, reattach with afterSeq, resync handling, and
// command idempotency across reconnects.

import { applyEvent, emptyState } from "./apply";
import { checkBuild } from "./boot";
import type { Access, HarnessMeta, Label, Project, ServerFrame, SessionMeta, SessionState } from "./protocol";

export type ConnectionStatus = "connecting" | "online" | "offline";

interface Pending {
  resolve: (v: any) => void;
  reject: (e: Error) => void;
  // Retained so the same commandId is retried after a reconnect rather than a
  // new one being minted, which would risk sending a prompt twice.
  frame: Record<string, unknown>;
}

export interface ClientEvents {
  onStatus(status: ConnectionStatus): void;
  onSessions(sessions: SessionMeta[]): void;
  onHarnesses(harnesses: HarnessMeta[], defaultCwd: string): void;
  onComposerItemsChanged(sessionId: string): void;
  onProjects(projects: Project[]): void;
  onLabels(labels: Label[]): void;
  onState(sessionId: string, state: SessionState): void;
  onAccess(access: Access): void;
}

const BACKOFF = [300, 1000, 2000, 4000, 8000, 16000];

export class Client {
  private ws: WebSocket | null = null;
  private attempt = 0;
  private closedByUs = false;
  private stableTimer: number | null = null;

  private pending = new Map<string, Pending>();

  // The attached session and its applied cursor. Reconnecting to a different
  // address is the same operation as reconnecting to the same one: attach with
  // afterSeq and receive the gap.
  private sessionId: string | null = null;
  private cursor = 0;
  private state: SessionState | null = null;

  constructor(private url: string, private events: ClientEvents) {}

  connect() {
    this.closedByUs = false;
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
        if (this.sessionId) {
          this.raw({ type: "attach", sessionId: this.sessionId, afterSeq: this.cursor });
        }
        // Re-send in-flight commands with their original ids. The server
        // replays stored results rather than executing twice.
        for (const [, p] of this.pending) this.raw(p.frame);

        // A connection that survives 30s resets the backoff ladder.
        this.stableTimer = window.setTimeout(() => (this.attempt = 0), 30_000);
      } catch (err) {
        console.error("handshake failed", err);
        ws.close();
      }
    };

    ws.onmessage = (e) => this.handle(JSON.parse(e.data as string) as ServerFrame);

    ws.onclose = () => {
      if (this.stableTimer) window.clearTimeout(this.stableTimer);
      this.ws = null;
      if (this.closedByUs) return;
      this.events.onStatus("offline");
      const delay = BACKOFF[Math.min(this.attempt++, BACKOFF.length - 1)];
      window.setTimeout(() => this.connect(), delay);
    };

    ws.onerror = () => ws.close();
  }

  close() {
    this.closedByUs = true;
    this.ws?.close();
  }

  /**
   * Adopt a state snapshot cached by a previous page of this tab (see
   * resume.ts), before connecting. The first attach then carries afterSeq, so
   * the server replays only what happened while the page was dead — and if
   * that gap has grown past replay range it answers with a snapshot, which
   * replaces the cache wholesale. Either way the reader starts on the cached
   * transcript instead of "Attaching…".
   */
  prime(state: SessionState) {
    this.sessionId = state.sessionId;
    this.cursor = state.seq;
    this.state = state;
  }

  /** Attach to a session, replacing any current attachment. */
  attach(sessionId: string) {
    if (this.sessionId && this.sessionId !== sessionId) {
      this.raw({ type: "detach", sessionId: this.sessionId });
    }
    this.sessionId = sessionId;
    this.cursor = 0;
    this.state = null;
    this.raw({ type: "attach", sessionId });
  }

  detach() {
    if (this.sessionId) this.raw({ type: "detach", sessionId: this.sessionId });
    this.sessionId = null;
    this.cursor = 0;
    this.state = null;
  }

  /** Send a command, resolving with its ack result. Safe to retry. */
  command(command: string, args: unknown): Promise<any> {
    const commandId = uuid();
    const frame = { type: "command", commandId, command, args };
    return new Promise((resolve, reject) => {
      this.pending.set(commandId, { resolve, reject, frame });
      this.raw(frame);
    });
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
        this.events.onSessions(f.sessions ?? []);
        this.events.onHarnesses(f.harnesses ?? [], f.cwd ?? "");
        this.events.onProjects(f.projects ?? []);
        this.events.onLabels(f.labels ?? []);
        if (f.access) {
          this.events.onAccess(f.access);
        }
        break;

      case "sessions":
        this.events.onSessions(f.sessions ?? []);
        break;

      // Harnesses can change after the welcome frame: a model list is read
      // from the harness in the background and lands seconds later. Without
      // this the picker would show the fallback list until a reconnect.
      case "harnesses":
        this.events.onHarnesses(f.harnesses ?? [], f.cwd ?? "");
        break;

      // The project registry changed somewhere — this device or a paired one.
      // The list travels whole; absent means the last project was removed, so
      // a project deleted on the phone leaves the laptop's picker too.
      case "projects":
        this.events.onProjects(f.projects ?? []);
        break;

      // Label definitions changed somewhere — this device or a paired one.
      // The list travels whole; absent means the last label was deleted.
      case "labels":
        this.events.onLabels(f.labels ?? []);
        break;

      case "composer_items_changed":
        if (f.sessionId === this.sessionId) this.events.onComposerItemsChanged(f.sessionId);
        break;

      case "snapshot":
        if (f.sessionId !== this.sessionId || !f.state) return;
        this.state = f.state;
        this.cursor = f.state.seq;
        this.events.onState(f.sessionId, this.state);
        break;

      case "event": {
        if (f.sessionId !== this.sessionId || !f.event) return;
        const base = this.state ?? emptyState(f.sessionId);
        const next = applyEvent(base, f.event);
        if (next === base) return; // duplicate; already applied
        this.state = next;
        this.cursor = next.seq;
        this.events.onState(f.sessionId, next);
        break;
      }

      case "synchronized":
        if (f.sessionId === this.sessionId && this.state) {
          this.events.onState(f.sessionId, this.state);
        }
        break;

      case "resync":
        // The server dropped our queue. Reattach from the applied cursor; if
        // we have fallen far enough behind it answers with a snapshot.
        if (f.sessionId === this.sessionId) {
          this.raw({ type: "attach", sessionId: f.sessionId, afterSeq: this.cursor });
        }
        break;

      case "ack": {
        const p = f.commandId ? this.pending.get(f.commandId) : undefined;
        if (!p) return;
        this.pending.delete(f.commandId!);
        if (f.error) p.reject(new Error(f.error));
        else p.resolve(f.result);
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
