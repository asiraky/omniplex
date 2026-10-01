import { spawn, execFile, type ChildProcess } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import type { LogFile } from "./log";
import { RestartPolicy, type RestartOptions } from "./restart";

export type ServerStatus =
  | { kind: "starting" }
  | { kind: "running"; port: number; origin: string }
  | { kind: "failed"; message: string }
  | { kind: "stopped" };

export interface ServerOptions {
  binary: string;
  env: NodeJS.ProcessEnv;
  log: LogFile;
  pickPort: () => Promise<number>;
  onStatus: (status: ServerStatus) => void;
  healthTimeoutMs?: number;
  stopTimeoutMs?: number;
  restart?: RestartOptions;
}

// The server's exit status when another Omniplex server already has its
// database (cmd/omniplex exitDataInUse). Restarting cannot fix that, and the
// likely culprit is one the user started from a terminal.
export const EXIT_DATA_IN_USE = 17;

// The packaged binary sits in Resources/bin; in development it is the one
// `npm run build:server` leaves at the repo root, or whatever
// OMNIPLEX_SERVER_BIN names.
export function serverBinaryPath(opts: {
  isPackaged: boolean;
  resourcesPath: string;
  appPath: string;
  platform: NodeJS.Platform;
  env: NodeJS.ProcessEnv;
}): string {
  const exe = opts.platform === "win32" ? "omniplex.exe" : "omniplex";
  if (opts.isPackaged) return path.join(opts.resourcesPath, "bin", exe);
  if (opts.env.OMNIPLEX_SERVER_BIN) return path.resolve(opts.env.OMNIPLEX_SERVER_BIN);
  return path.resolve(opts.appPath, "..", exe);
}

// Runs the Go server, waits for it to answer, and restarts it if it dies on
// its own. Stopping is always deliberate: SIGTERM, then SIGKILL if it has not
// gone after stopTimeoutMs.
export class ServerSupervisor {
  private child: ChildProcess | null = null;
  private generation = 0;
  private stopping = false;
  private restartTimer: NodeJS.Timeout | null = null;
  private readonly policy: RestartPolicy;
  private status: ServerStatus = { kind: "stopped" };

  constructor(private readonly opts: ServerOptions) {
    this.policy = new RestartPolicy(opts.restart);
  }

  current(): ServerStatus {
    return this.status;
  }

  start(): void {
    this.stopping = false;
    this.policy.reset();
    this.clearRestart();
    void this.spawnOnce();
  }

  // Retry after giving up: a fresh restart budget, and a fresh port choice in
  // case something took ours.
  retry(): void {
    if (this.child) return;
    this.start();
  }

  async stop(): Promise<void> {
    this.stopping = true;
    this.clearRestart();
    const child = this.child;
    if (child && child.exitCode === null && child.signalCode === null) {
      this.opts.log.line(`stopping server (pid ${child.pid})`);
      await terminate(child, this.opts.stopTimeoutMs ?? 5000, this.opts.log);
    }
    this.child = null;
    this.setStatus({ kind: "stopped" });
  }

  private async spawnOnce(): Promise<void> {
    const gen = ++this.generation;
    this.setStatus({ kind: "starting" });

    if (!fs.existsSync(this.opts.binary)) {
      this.opts.log.line(`server binary missing: ${this.opts.binary}`);
      this.setStatus({ kind: "failed", message: `The Omniplex server is missing from ${this.opts.binary}.` });
      return;
    }

    let port: number;
    try {
      port = await this.opts.pickPort();
    } catch (err) {
      this.setStatus({ kind: "failed", message: `No free port to listen on: ${String(err)}` });
      return;
    }
    if (gen !== this.generation || this.stopping) return;

    // stdin stays open for as long as this process lives: the server exits
    // when it closes, so a crash here cannot orphan it on the port.
    const args = ["-private", "-exit-on-stdin-close", "-port", String(port)];
    this.opts.log.line(`starting ${this.opts.binary} ${args.join(" ")}`);
    const child = spawn(this.opts.binary, args, {
      env: this.opts.env,
      stdio: ["pipe", this.opts.log.fd, this.opts.log.fd],
      windowsHide: true,
    });
    this.child = child;
    this.policy.started();

    let exited = false;
    const onGone = (reason: string, code: number | null = null) => {
      if (exited) return;
      exited = true;
      if (this.child === child) this.child = null;
      if (this.stopping || gen !== this.generation) return;
      this.opts.log.line(`server exited unexpectedly (${reason})`);
      if (code === EXIT_DATA_IN_USE) {
        this.setStatus({
          kind: "failed",
          message:
            "Another Omniplex server is already running on this computer, probably one started from a terminal. Stop it, then try again.",
        });
        return;
      }
      const delay = this.policy.onExit();
      if (delay === null) {
        this.setStatus({ kind: "failed", message: `The server stopped (${reason}) and kept failing to restart.` });
        return;
      }
      this.opts.log.line(`restarting in ${delay}ms`);
      this.setStatus({ kind: "starting" });
      this.restartTimer = setTimeout(() => void this.spawnOnce(), delay);
    };
    child.once("error", (err) => onGone(err.message));
    child.once("exit", (code, signal) => onGone(signal ? `signal ${signal}` : `exit code ${code}`, code));

    const origin = `http://127.0.0.1:${port}`;
    const healthy = await waitForHealth(origin, this.opts.healthTimeoutMs ?? 30000, () => exited);
    if (gen !== this.generation || this.stopping || exited) return;
    if (!healthy) {
      // Alive but not answering: count it as a crash and let the exit path
      // decide whether to try again.
      this.opts.log.line(`server did not answer on ${origin}/api/health; killing it`);
      await terminate(child, 2000, this.opts.log);
      return;
    }
    this.opts.log.line(`server ready on ${origin} (pid ${child.pid})`);
    this.setStatus({ kind: "running", port, origin });
  }

  private setStatus(status: ServerStatus): void {
    this.status = status;
    this.opts.onStatus(status);
  }

  private clearRestart(): void {
    if (this.restartTimer) clearTimeout(this.restartTimer);
    this.restartTimer = null;
  }
}

export async function waitForHealth(origin: string, timeoutMs: number, gone: () => boolean): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline && !gone()) {
    try {
      const wait = Math.max(1, Math.min(2000, deadline - Date.now()));
      const res = await fetch(`${origin}/api/health`, { signal: AbortSignal.timeout(wait) });
      if (res.ok) {
        const body = (await res.json()) as { ok?: unknown };
        if (body.ok === true) return true;
      }
    } catch {
      // Not listening yet.
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  return false;
}

function terminate(child: ChildProcess, timeoutMs: number, log: LogFile): Promise<void> {
  return new Promise((resolve) => {
    if (child.exitCode !== null || child.signalCode !== null) return resolve();
    let giveUp: NodeJS.Timeout | undefined;
    const done = () => {
      clearTimeout(timer);
      clearTimeout(giveUp);
      resolve();
    };
    child.once("exit", done);
    const timer = setTimeout(() => {
      log.line(`server (pid ${child.pid}) ignored the stop request for ${timeoutMs}ms; killing it`);
      forceKill(child);
      giveUp = setTimeout(done, 2000);
    }, timeoutMs);
    // Closing stdin is the stop request the server listens for on every
    // platform; Windows has no SIGTERM a console-less process can catch.
    child.stdin?.end();
    if (process.platform !== "win32") child.kill("SIGTERM");
  });
}

function forceKill(child: ChildProcess): void {
  if (process.platform === "win32" && child.pid) {
    execFile("taskkill", ["/pid", String(child.pid), "/T", "/F"], () => undefined);
  } else {
    child.kill("SIGKILL");
  }
}
