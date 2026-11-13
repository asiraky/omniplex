// The terminal's connection, apart from the xterm that draws it. The server
// keeps a shell alive for a while after its socket drops and replays what it
// printed meanwhile, so a dropped socket is something to reconnect, not the
// end of the shell: this owns the reconnect, the reattach by terminal id, and
// the byte offset that says how much of the output the screen already has.

import { BACKOFF } from "~/client";

export type TerminalTarget =
  /** The user's shell in a thread's checkout. */
  | { thread: string }
  /** A harness's own sign-in flow for one provider instance. */
  | { login: string };

export type TermStatus = "connecting" | "online" | "reconnecting";

/**
 * "exited": the process ended. "gone": the server has no such terminal any
 * more (closed elsewhere, idle too long, or the server restarted). "failed":
 * the server could not start it.
 */
export type TermEnd = "exited" | "gone" | "failed";

export interface TermSocketEvents {
  onData(bytes: Uint8Array): void;
  /** What is on screen no longer lines up with the stream: clear it. */
  onReset(): void;
  onStatus(status: TermStatus): void;
  onEnded(reason: TermEnd): void;
}

/** Where a shell is, as its server saw it. Both absolute. */
export interface ShellLocation {
  /** Where the shell is now, which a cd since the start has moved. */
  cwd: string;
  /** Where it started: for a thread's shell, the checkout root. */
  root: string;
}

export interface TermSocket {
  /** Keystrokes. Dropped while disconnected. */
  send(data: string): void;
  /** Sent now if connected, and again on every connect. */
  resize(cols: number, rows: number): void;
  /** A fresh shell under the same id, after the last one ended. */
  restart(): void;
  /**
   * Where the shell is now, the directory a relative path it printed is
   * relative to, and where it started. null when the server cannot tell, is
   * not reachable, or takes longer than a click should wait. Calls made while
   * one is out share its answer.
   */
  where(): Promise<ShellLocation | null>;
  /** Hang up. The server keeps the shell; nothing is reported after this. */
  dispose(): void;
}

// The server's closes that end the terminal. Any other close is a dropped
// connection, and worth retrying.
const CLOSE_ENDS: Record<number, TermEnd> = { 4000: "exited", 4404: "gone", 4400: "failed" };

// How long a where question waits for the server. Past this the click has to
// go ahead without it.
const WHERE_TIMEOUT_MS = 3000;

/**
 * tail is how many lines the screen keeps (scrollback plus rows). A socket
 * with nothing on screen yet asks the server to replay only that many, not
 * its whole buffer. A getter, so a retry still at the start uses the size now.
 */
export function openTermSocket(
  id: string,
  target: TerminalTarget,
  events: TermSocketEvents,
  tail?: () => number,
): TermSocket {
  let ws: WebSocket | null = null;
  let disposed = false;
  // The stream offset of the next byte the screen has not seen.
  let offset = 0;
  // Whether the server has answered for this id since the last (re)start. Only
  // then is the session known to exist: a first connect that never reached the
  // server spawned nothing, and attach=1 would be told there is no terminal.
  let synced = false;
  // Set by restart until the server answers: an ended shell still held under
  // the id is to be replaced, not replayed.
  let fresh = false;
  let attempt = 0;
  let retryTimer: number | null = null;
  let stableTimer: number | null = null;
  let size: { cols: number; rows: number } | null = null;
  // The where question out on the wire, if any. The server answers in order
  // on the socket it was asked on, so a drop means no answer is coming.
  let whereAsk: {
    answer: Promise<ShellLocation | null>;
    settle: (at: ShellLocation | null) => void;
  } | null = null;
  // Questions asked on this socket and not yet answered. More than one means
  // an earlier one timed out, and the next answer is that one's, not the
  // current question's.
  let whereOwed = 0;

  const raw = (frame: Record<string, unknown>) => {
    if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(frame));
  };

  const url = () => {
    const q = new URLSearchParams({ id });
    if ("thread" in target) q.set("thread", target.thread);
    else q.set("login", target.login);
    if (offset > 0) q.set("since", String(offset));
    else {
      // Past the start, since already says what to send; tail would only
      // contradict it.
      const lines = tail?.();
      if (lines !== undefined && Number.isInteger(lines) && lines > 0) q.set("tail", String(lines));
    }
    if (synced) q.set("attach", "1");
    else if (fresh) q.set("fresh", "1");
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    return `${proto}//${location.host}/api/term?${q}`;
  };

  const clearTimers = () => {
    if (retryTimer !== null) window.clearTimeout(retryTimer);
    if (stableTimer !== null) window.clearTimeout(stableTimer);
    retryTimer = stableTimer = null;
  };

  const answerWhere = (at: ShellLocation | null) => {
    whereAsk?.settle(at);
    whereAsk = null;
  };

  // Detach before closing, so a socket we hung up on ourselves reports nothing.
  const hangUp = () => {
    answerWhere(null);
    if (!ws) return;
    ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
    ws.close();
    ws = null;
  };

  const connect = () => {
    retryTimer = null;
    const sock = new WebSocket(url());
    sock.binaryType = "arraybuffer";
    ws = sock;
    whereOwed = 0;

    sock.onopen = () => {
      events.onStatus("online");
      if (size) raw({ type: "resize", ...size });
      // A connection that survives 30s resets the backoff ladder.
      stableTimer = window.setTimeout(() => (attempt = 0), 30_000);
    };

    sock.onmessage = (e: MessageEvent) => {
      if (typeof e.data !== "string") {
        const bytes = new Uint8Array(e.data as ArrayBuffer);
        offset += bytes.byteLength;
        events.onData(bytes);
        return;
      }
      let frame: { type?: string; offset?: unknown; data?: unknown; root?: unknown };
      try {
        frame = JSON.parse(e.data);
      } catch {
        return;
      }
      if (frame.type === "cwd") {
        if (--whereOwed > 0) return;
        whereOwed = 0;
        const { data: cwd, root } = frame;
        // "" is the server not knowing. A shell with no start directory (a
        // sign-in) has nothing for a path to be under either.
        answerWhere(typeof cwd === "string" && cwd && typeof root === "string" && root ? { cwd, root } : null);
        return;
      }
      if (frame.type !== "sync" || typeof frame.offset !== "number") return;
      // A different offset means the stream does not continue from what is on
      // screen: a fresh shell, or we fell behind what the server still holds.
      if (frame.offset !== offset) events.onReset();
      offset = frame.offset;
      synced = true;
      fresh = false;
    };

    sock.onclose = (e: CloseEvent) => {
      if (stableTimer !== null) window.clearTimeout(stableTimer);
      stableTimer = null;
      ws = null;
      answerWhere(null);
      const end = CLOSE_ENDS[e.code];
      if (end) {
        events.onEnded(end);
        return;
      }
      events.onStatus("reconnecting");
      const delay = BACKOFF[Math.min(attempt++, BACKOFF.length - 1)];
      retryTimer = window.setTimeout(connect, delay);
    };

    sock.onerror = () => sock.close();
  };

  // Coming back online or to the foreground is the moment a phone can reach
  // the server again; waiting out the rest of a 16s backoff then is just lag.
  const retryNow = () => {
    if (retryTimer === null) return;
    window.clearTimeout(retryTimer);
    connect();
  };
  const onVisibility = () => {
    if (document.visibilityState === "visible") retryNow();
  };
  window.addEventListener("online", retryNow);
  document.addEventListener("visibilitychange", onVisibility);

  events.onStatus("connecting");
  connect();

  return {
    send(data) {
      // Never queued: keystrokes typed blind and replayed later could run
      // something the user would not have run against the screen they see.
      raw({ type: "input", data });
    },
    resize(cols, rows) {
      size = { cols, rows };
      raw({ type: "resize", cols, rows });
    },
    where() {
      if (whereAsk) return whereAsk.answer;
      if (disposed || ws?.readyState !== WebSocket.OPEN) return Promise.resolve(null);
      let settle!: (at: ShellLocation | null) => void;
      const answer = new Promise<ShellLocation | null>((resolve) => {
        const timer = window.setTimeout(() => answerWhere(null), WHERE_TIMEOUT_MS);
        settle = (at) => {
          window.clearTimeout(timer);
          resolve(at);
        };
      });
      whereAsk = { answer, settle };
      whereOwed++;
      raw({ type: "cwd" });
      return answer;
    },
    restart() {
      if (disposed) return;
      clearTimers();
      hangUp();
      offset = 0;
      synced = false;
      fresh = true;
      attempt = 0;
      events.onReset();
      events.onStatus("connecting");
      connect();
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      clearTimers();
      hangUp();
      window.removeEventListener("online", retryNow);
      document.removeEventListener("visibilitychange", onVisibility);
    },
  };
}

/** Kill the shell outright: the user closed its tab. */
export function endTerminal(id: string) {
  // A failed DELETE is fine: the server reaps the shell on its idle TTL.
  fetch(`/api/term?id=${encodeURIComponent(id)}`, { method: "DELETE" }).catch(() => {});
}
